// backup never loads .env or shared credential profiles and never stops services.
package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/adcwb/ai-creative-studio/backend/internal/application"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "backup operation failed; inspect maintenance state and retained staging before retry; no automatic rollback or retry performed")
		os.Exit(1)
	}
}
func run() error {
	mode := flag.String("mode", "verify", "backup, verify, restore, seal, open or retention")
	source := flag.String("source", "", "backup bundle for verify/restore")
	dest := flag.String("destination", "", "new bundle or restore directory")
	stopped := flag.Bool("confirm-stopped", false, "operator confirms ALL API/worker instances stopped")
	dump := flag.String("pg-dump", "pg_dump", "matching PostgreSQL pg_dump executable")
	restore := flag.String("pg-restore", "pg_restore", "matching PostgreSQL pg_restore executable")
	keepLast := flag.Int("keep-last", 7, "minimum newest authenticated encrypted backups to retain")
	minAge := flag.Int("min-age-hours", 168, "minimum age before retention deletion; at least 24 hours")
	applyRetention := flag.Bool("apply-retention", false, "delete only the exact reviewed retention plan")
	expectedPlan := flag.String("expected-plan-sha256", "", "required reviewed retention plan digest when applying")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *mode == "seal" || *mode == "open" || *mode == "retention" {
		key, e := base64.StdEncoding.DecodeString(os.Getenv("BACKUP_ENCRYPTION_KEY_BASE64"))
		if e != nil || len(key) != 32 {
			return errors.New("explicit 32-byte backup encryption key required")
		}
		defer clear(key)
		var output any
		switch *mode {
		case "seal":
			output, e = application.SealBackup(ctx, *source, *dest, key)
		case "open":
			output, e = application.OpenBackup(ctx, *source, *dest, key)
		case "retention":
			if (*applyRetention && *expectedPlan == "") || (!*applyRetention && *expectedPlan != "") {
				return errors.New("apply and reviewed plan hash must be supplied together")
			}
			output, e = application.RetainBackups(ctx, *source, key, *keepLast, *minAge, *expectedPlan)
		}
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(output)
	}
	var report application.PostgresBackupReport
	var err error
	if *mode == "verify" {
		_, report, err = application.VerifyPostgresBackup(ctx, *source)
	} else {
		if !*stopped || *dest == "" {
			return fmt.Errorf("maintenance confirmation and new destination required")
		}
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			return fmt.Errorf("explicit DATABASE_URL required")
		}
		// Isolate pgx from inherited libpq profiles as well as the child tools.
		for _, pair := range os.Environ() {
			name, _, _ := strings.Cut(pair, "=")
			if strings.HasPrefix(name, "PG") {
				os.Unsetenv(name)
			}
		}
		private, e := os.MkdirTemp("", "studio-backup-connect-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(private)
		passfile := filepath.Join(private, "empty.pgpass")
		if e = os.WriteFile(passfile, nil, 0600); e != nil {
			return e
		}
		u, e := url.Parse(dsn)
		if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
			return fmt.Errorf("explicit PostgreSQL URI required")
		}
		query := u.Query()
		allowed := map[string]bool{"host": true, "port": true, "user": true, "password": true, "dbname": true, "sslmode": true, "sslrootcert": true, "sslcert": true, "sslkey": true, "sslpassword": true, "connect_timeout": true, "application_name": true, "target_session_attrs": true}
		for key, v := range query {
			if !allowed[key] || len(v) != 1 {
				return fmt.Errorf("unsupported PostgreSQL URI option")
			}
		}
		if query.Get("sslmode") == "" {
			return fmt.Errorf("explicit sslmode required")
		}

		query.Set("passfile", passfile)
		u.RawQuery = query.Encode()
		db, e := sql.Open("pgx", u.String())
		if e != nil {
			return e
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		tools := application.PostgresBackupTools{Dump: *dump, Restore: *restore, LibraryPath: os.Getenv("BACKUP_PG_LIBRARY_PATH")}
		switch *mode {
		case "backup":
			var store application.BlobStore
			switch os.Getenv("BLOB_STORE") {
			case "shared-file":
				root := os.Getenv("BLOB_FILE_ROOT")
				st, e := os.Lstat(root)
				if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("existing shared object directory required")
				}
				fs, e := application.NewFileBlobStore(root)
				if e != nil {
					return e
				}
				defer fs.Close()
				store = fs
			case "s3":
				store, err = application.S3BlobStoreFromEnv()
			default:
				return fmt.Errorf("shared-file or s3 required")
			}
			if err != nil {
				return err
			}
			report, err = application.BackupPostgresObjects(ctx, db, dsn, store, *dest, tools, true)
		case "restore":
			report, err = application.RestorePostgresBackup(ctx, db, dsn, *source, *dest, tools, true)
		default:
			return fmt.Errorf("invalid mode")
		}
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
