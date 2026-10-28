// model-key-rotate only rewrites existing encrypted model credentials offline.
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
	"path/filepath"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/application"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func run() error {
	dir := flag.String("data-dir", "", "existing stopped file-mode DATA_DIR")
	stopped := flag.Bool("confirm-stopped", false, "assert ALL API/worker instances and restart controllers stopped")
	apply := flag.Bool("apply", false, "apply atomic rotation; default validates without mutation")
	flag.Parse()
	if !*stopped || flag.NArg() != 0 {
		return errors.New("explicit offline arguments required")
	}
	keys := make([][]byte, 3)
	for i, name := range []string{"MODEL_ROTATION_OLD_KEK_BASE64", "MODEL_ROTATION_NEW_KEK_BASE64", "MODEL_ROTATION_CONFIRM_KEK_BASE64"} {
		key, e := base64.StdEncoding.DecodeString(os.Getenv(name))
		if e != nil || len(key) != 32 {
			return errors.New("explicit valid rotation keys required")
		}
		keys[i] = key
		defer clear(key)
	}
	var report application.ModelRotationReport
	var e error
	switch os.Getenv("STORAGE_BACKEND") {
	case "", "file":
		report, e = application.RotateFileModelKey(*dir, keys[0], keys[1], keys[2], *stopped, *apply)
	case "postgres":
		if os.Getenv("DATABASE_URL") == "" {
			return errors.New("explicit database required")
		}
		// Explicit URI only; never fall back to inherited PG variables or the
		// operator's pg_service/.pgpass files.
		dsn, cleanup, err := rotationConnection(os.Getenv("DATABASE_URL"))
		if err != nil {
			return err
		}
		defer cleanup()
		for _, pair := range os.Environ() {
			name, _, _ := strings.Cut(pair, "=")
			if strings.HasPrefix(name, "PG") {
				os.Unsetenv(name)
			}
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return errors.New("invalid database configuration")
		}
		defer db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		report, e = application.RotatePostgresModelKey(ctx, db, keys[0], keys[1], keys[2], *stopped, *apply)
	default:
		return errors.New("unsupported storage backend")
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "model key rotation failed; keep all services stopped and verify state before retrying")
		os.Exit(1)
	}
}

// rotationConnection intentionally does not accept arbitrary libpq options.
// The returned string remains private and must never be printed in an error.
func rotationConnection(raw string) (string, func(), error) {
	fail := func() (string, func(), error) { return "", func() {}, errors.New("explicit PostgreSQL URI required") }
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Fragment != "" || u.Opaque != "" {
		return fail()
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return fail()
	}
	allowed := map[string]bool{"host": true, "port": true, "user": true, "password": true, "sslmode": true, "sslrootcert": true, "sslcert": true, "sslkey": true, "sslpassword": true, "connect_timeout": true, "application_name": true, "target_session_attrs": true}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 {
			return fail()
		}
	}
	user := q.Get("user")
	if user == "" && u.User != nil {
		user = u.User.Username()
	}
	if user == "" || (u.Hostname() == "" && q.Get("host") == "") || strings.Trim(u.Path, "/") == "" || q.Get("sslmode") == "" {
		return fail()
	}
	private, e := os.MkdirTemp("", "studio-key-rotate-connect-")
	if e != nil {
		return fail()
	}
	cleanup := func() { os.RemoveAll(private) }
	passfile := filepath.Join(private, "empty.pgpass")
	if os.WriteFile(passfile, nil, 0600) != nil {
		cleanup()
		return fail()
	}
	q.Set("passfile", passfile)
	u.RawQuery = q.Encode()
	return u.String(), cleanup, nil
}
