package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxDatabaseBackupBytes int64 = 1 << 30
const maxBackupObjectBytes int64 = 100 << 30

type PostgresBackupTools struct{ Dump, Restore, LibraryPath string }
type PostgresBackupFile struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type PostgresBackupManifest struct {
	Version     int                 `json:"version"`
	CreatedAt   time.Time           `json:"created_at"`
	Database    PostgresBackupFile  `json:"database"`
	Objects     map[string]BlobInfo `json:"objects"`
	QueueStates map[string]int64    `json:"queue_states"`
	CostStates  map[string]int64    `json:"cost_states"`
}
type PostgresBackupReport struct {
	Objects        int    `json:"objects"`
	ObjectBytes    int64  `json:"object_bytes"`
	DatabaseBytes  int64  `json:"database_bytes"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Verified       bool   `json:"verified"`
}

// Credentials are supplied only to the child environment, never argv, logs or
// reports. URI-only connection parameters avoid inventing an unsafe keyword DSN
// parser or reading pg_service/.pgpass from the operator's home directory.
func backupPostgresEnv(dsn, privateDir, libraryPath string) ([]string, error) {
	u, e := url.Parse(dsn)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("backup requires an explicit PostgreSQL URI")
	}
	values := map[string]string{"PGHOST": u.Hostname(), "PGPORT": u.Port(), "PGDATABASE": strings.TrimPrefix(u.Path, "/")}
	if u.User != nil {
		values["PGUSER"] = u.User.Username()
		if p, ok := u.User.Password(); ok {
			values["PGPASSWORD"] = p
		}
	}
	allowed := map[string]string{"host": "PGHOST", "port": "PGPORT", "user": "PGUSER", "password": "PGPASSWORD", "dbname": "PGDATABASE", "sslmode": "PGSSLMODE", "sslrootcert": "PGSSLROOTCERT", "sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY", "sslpassword": "PGSSLPASSWORD", "connect_timeout": "PGCONNECT_TIMEOUT", "application_name": "PGAPPNAME", "target_session_attrs": "PGTARGETSESSIONATTRS"}
	for key, v := range u.Query() {
		name, ok := allowed[key]
		if !ok || len(v) != 1 {
			return nil, errors.New("unsupported backup connection option")
		}
		values[name] = v[0]
	}
	if values["PGHOST"] == "" || values["PGDATABASE"] == "" || values["PGUSER"] == "" || !regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,62}$`).MatchString(values["PGDATABASE"]) {
		return nil, errors.New("backup URI requires explicit host, database and user")
	}
	if values["PGSSLMODE"] == "" {
		return nil, errors.New("backup URI requires explicit sslmode")
	}
	// A real empty private file avoids fallback credentials and libpq warnings.
	passfile := filepath.Join(privateDir, "empty.pgpass")
	if e = os.WriteFile(passfile, nil, 0600); e != nil {
		return nil, e
	}
	servicefile := filepath.Join(privateDir, "empty.pgservice")
	if e = os.WriteFile(servicefile, nil, 0600); e != nil {
		return nil, e
	}
	// Disable implicit ~/.postgresql client keys/certificates too.
	for _, name := range []string{"PGSSLCERT", "PGSSLKEY", "PGSSLROOTCERT"} {
		if values[name] == "" {
			values[name] = filepath.Join(privateDir, "absent-"+name)
		}
	}
	env := []string{"PATH=/usr/bin:/bin", "PGPASSFILE=" + passfile, "PGSERVICEFILE=" + servicefile, "PGSYSCONFDIR=" + privateDir}
	if libraryPath != "" {
		env = append(env, "LD_LIBRARY_PATH="+libraryPath)
	}
	for k, v := range values {
		if strings.ContainsRune(v, 0) {
			return nil, errors.New("invalid backup connection option")
		}
		env = append(env, k+"="+v)
	}
	return env, nil
}

type boundedBackupWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedBackupWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.remaining {
		return 0, errors.New("database backup exceeds bound")
	}
	n, e := w.writer.Write(b)
	w.remaining -= int64(n)
	return n, e
}
func backupHashFile(path string, limit int64) (PostgresBackupFile, error) {
	f, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return PostgresBackupFile{}, e
	}
	defer f.Close()
	r, e := f.Open(filepath.Base(path))
	if e != nil {
		return PostgresBackupFile{}, e
	}
	defer r.Close()
	st, e := r.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return PostgresBackupFile{}, ErrBlobIntegrity
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(r, limit+1))
	if e != nil || n > limit {
		return PostgresBackupFile{}, ErrBlobIntegrity
	}
	return PostgresBackupFile{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}
func backupStateCounts(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table, column string) (map[string]int64, error) {
	rows, e := q.QueryContext(ctx, "SELECT "+column+",count(*) FROM "+table+" GROUP BY "+column)
	if e != nil {
		return nil, errors.New("backup state inventory unavailable")
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var state string
		var n int64
		if e = rows.Scan(&state, &n); e != nil {
			return nil, e
		}
		result[state] = n
	}
	return result, rows.Err()
}

// BackupPostgresObjects is offline. ConfirmStopped is a mandatory operator
// assertion, not a claim that the advisory barrier stops queue Claim/heartbeat.
// The exported PG snapshot binds pg_dump and immutable object metadata together.
func BackupPostgresObjects(ctx context.Context, db *sql.DB, dsn string, store BlobStore, destination string, tools PostgresBackupTools, confirmStopped bool) (PostgresBackupReport, error) {
	report := PostgresBackupReport{}
	if !confirmStopped || store == nil {
		return report, errors.New("stop every API and worker and confirm maintenance window")
	}
	if _, e := os.Lstat(destination); !os.IsNotExist(e) {
		return report, errors.New("backup destination must be new")
	}
	stage, e := os.MkdirTemp(filepath.Dir(destination), ".postgres-backup-")
	if e != nil {
		return report, e
	}
	defer os.RemoveAll(stage)
	private, e := os.MkdirTemp("", "studio-pg-tools-")
	if e != nil {
		return report, e
	}
	defer os.RemoveAll(private)
	env, e := backupPostgresEnv(dsn, private, tools.LibraryPath)
	if e != nil {
		return report, e
	}
	conn, e := db.Conn(ctx)
	if e != nil {
		return report, errors.New("backup database unavailable")
	}
	defer conn.Close()
	// Acquire the barrier before establishing the repeatable-read snapshot.
	if _, e = conn.ExecContext(ctx, "SELECT pg_advisory_lock(728219117)"); e != nil {
		return report, errors.New("backup migration barrier unavailable")
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(728219117)")
	tx, e := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return report, e
	}
	defer tx.Rollback()
	var active int64
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM execution_jobs WHERE state IN ('leased','dispatch_started') AND lease_until>clock_timestamp()").Scan(&active); e != nil {
		return report, errors.New("backup queue state unavailable")
	}
	if active != 0 {
		return report, errors.New("backup refuses unexpired job leases; stop workers and wait for expiry")
	}
	manifest := PostgresBackupManifest{Version: 1, CreatedAt: time.Now().UTC(), Objects: map[string]BlobInfo{}}
	manifest.QueueStates, e = backupStateCounts(ctx, tx, "execution_jobs", "state")
	if e != nil {
		return report, e
	}
	manifest.CostStates, e = backupStateCounts(ctx, tx, "studio_cost_entries", "state")
	if e != nil {
		return report, e
	}
	rows, e := tx.QueryContext(ctx, "SELECT key,size,sha256 FROM studio_blobs ORDER BY key")
	if e != nil {
		return report, errors.New("backup blob inventory unavailable")
	}
	for rows.Next() {
		var info BlobInfo
		if e = rows.Scan(&info.Key, &info.Size, &info.SHA256); e != nil {
			rows.Close()
			return report, e
		}
		if !validBlobInfo(info) || len(manifest.Objects) >= 100000 || report.ObjectBytes > maxBackupObjectBytes-info.Size {
			rows.Close()
			return report, errors.New("backup object bound or integrity failure")
		}
		manifest.Objects[info.Key] = info
		report.ObjectBytes += info.Size
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return report, e
	}
	var snapshot string
	if e = tx.QueryRowContext(ctx, "SELECT pg_export_snapshot()").Scan(&snapshot); e != nil {
		return report, errors.New("backup snapshot export failed")
	}
	dump := tools.Dump
	if dump == "" {
		dump = "pg_dump"
	}
	file, e := os.OpenFile(filepath.Join(stage, "database.dump"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return report, e
	}
	cmd := exec.CommandContext(ctx, dump, "--no-password", "--format=custom", "--no-owner", "--no-privileges", "--snapshot="+snapshot)
	cmd.Env = env
	cmd.Stdout = &boundedBackupWriter{writer: file, remaining: maxDatabaseBackupBytes}
	cmd.Stderr = io.Discard
	runErr := cmd.Run()
	e = errors.Join(file.Sync(), file.Close())
	if runErr != nil || e != nil {
		return report, errors.New("pg_dump failed or exceeded backup bound")
	}
	manifest.Database, e = backupHashFile(filepath.Join(stage, "database.dump"), maxDatabaseBackupBytes)
	if e != nil {
		return report, e
	}
	objects, e := NewFileBlobStore(filepath.Join(stage, "objects"))
	if e != nil {
		return report, e
	}
	for key, info := range manifest.Objects {
		b, err := readVerifiedBlob(ctx, store, info)
		if err != nil {
			objects.Close()
			return report, errors.New("backup object missing or corrupt")
		}
		if _, err = objects.Put(ctx, key, b); err != nil {
			objects.Close()
			return report, err
		}
	}
	if e = objects.Close(); e != nil {
		return report, e
	}
	raw, e := json.MarshalIndent(manifest, "", "  ")
	if e != nil {
		return report, e
	}
	if e = os.WriteFile(filepath.Join(stage, "backup.json"), raw, 0600); e != nil {
		return report, e
	}
	if e = tx.Commit(); e != nil {
		return report, errors.New("backup snapshot transaction failed")
	}
	if e = syncMigrationDirectory(stage); e != nil {
		return report, e
	}
	if e = renameDirectoryNoReplace(stage, destination); e != nil {
		return report, e
	}
	report.Objects = len(manifest.Objects)
	report.DatabaseBytes = manifest.Database.Size
	report.ManifestSHA256 = s3Hash(string(raw))
	report.Verified = true
	return report, nil
}

func VerifyPostgresBackup(ctx context.Context, source string) (PostgresBackupManifest, PostgresBackupReport, error) {
	var manifest PostgresBackupManifest
	report := PostgresBackupReport{}
	st, e := os.Lstat(source)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return manifest, report, errors.New("backup source must be a regular directory")
	}
	raw, e := safeReadLocal(filepath.Join(source, "backup.json"), 32<<20)
	if e != nil {
		return manifest, report, e
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version != 1 || manifest.Objects == nil || manifest.QueueStates == nil || manifest.CostStates == nil || len(manifest.Objects) > 100000 {
		return manifest, report, errors.New("invalid backup manifest")
	}
	expected := map[string]bool{"backup.json": true, "database.dump": true}
	for k, i := range manifest.Objects {
		if k != i.Key || !validBlobInfo(i) || report.ObjectBytes > maxBackupObjectBytes-i.Size {
			return manifest, report, ErrBlobIntegrity
		}
		expected["objects/"+k] = true
		report.ObjectBytes += i.Size
	}
	if e = filepath.WalkDir(source, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("backup refuses symbolic links")
		}
		if d.IsDir() {
			return nil
		}
		st, e := d.Info()
		if e != nil || !st.Mode().IsRegular() {
			return ErrBlobIntegrity
		}
		rel, e := filepath.Rel(source, path)
		if e != nil || !expected[filepath.ToSlash(rel)] {
			return errors.New("unexpected backup file")
		}
		delete(expected, filepath.ToSlash(rel))
		return nil
	}); e != nil {
		return manifest, report, e
	}
	if len(expected) != 0 {
		return manifest, report, errors.New("backup files missing")
	}
	actual, e := backupHashFile(filepath.Join(source, "database.dump"), maxDatabaseBackupBytes)
	if e != nil || actual != manifest.Database {
		return manifest, report, ErrBlobIntegrity
	}
	objects, e := openBackupObjects(filepath.Join(source, "objects"))
	if e != nil {
		return manifest, report, e
	}
	defer objects.Close()
	for _, info := range manifest.Objects {
		if _, e = readVerifiedBlob(ctx, objects, info); e != nil {
			return manifest, report, e
		}
	}
	report.Objects = len(manifest.Objects)
	report.DatabaseBytes = manifest.Database.Size
	report.ManifestSHA256 = s3Hash(string(raw))
	report.Verified = true
	return manifest, report, nil
}

// RestorePostgresBackup never creates/drops databases or overwrites directories.
// It targets an already-created EMPTY database and publishes a new shared-file
// object root. It does not start API/workers, reset queues or release costs.
func RestorePostgresBackup(ctx context.Context, db *sql.DB, dsn, source, destination string, tools PostgresBackupTools, confirmStopped bool) (PostgresBackupReport, error) {
	if !confirmStopped {
		return PostgresBackupReport{}, errors.New("restore requires all target API/workers stopped")
	}
	manifest, report, e := VerifyPostgresBackup(ctx, source)
	if e != nil {
		return report, e
	}
	if _, e = os.Lstat(destination); !os.IsNotExist(e) {
		return report, errors.New("restore destination must be new")
	}
	conn, e := db.Conn(ctx)
	if e != nil {
		return report, errors.New("restore database unavailable")
	}
	defer conn.Close()
	if _, e = conn.ExecContext(ctx, "SELECT pg_advisory_lock(728219117)"); e != nil {
		return report, errors.New("restore migration barrier unavailable")
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(728219117)")
	var n int64
	if e = conn.QueryRowContext(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON c.relnamespace=n.oid WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp%'`).Scan(&n); e != nil || n != 0 {
		return report, errors.New("restore database is not empty")
	}
	if e = conn.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND pid<>pg_backend_pid()").Scan(&n); e != nil || n != 0 {
		return report, errors.New("restore database has other connections")
	}
	stage, e := os.MkdirTemp(filepath.Dir(destination), ".postgres-restore-")
	if e != nil {
		return report, e
	}
	preserve := false
	defer func() {
		if !preserve {
			os.RemoveAll(stage)
		}
	}()
	from, e := openBackupObjects(filepath.Join(source, "objects"))
	if e != nil {
		return report, e
	}
	defer from.Close()
	to, e := NewFileBlobStore(filepath.Join(stage, "objects"))
	if e != nil {
		return report, e
	}
	for _, info := range manifest.Objects {
		b, err := readVerifiedBlob(ctx, from, info)
		if err != nil {
			to.Close()
			return report, err
		}
		if _, err = to.Put(ctx, info.Key, b); err != nil {
			to.Close()
			return report, err
		}
	}
	if e = to.Close(); e != nil {
		return report, e
	}
	if e = os.Mkdir(filepath.Join(stage, "cache"), 0700); e != nil {
		return report, e
	}
	private, e := os.MkdirTemp("", "studio-pg-tools-")
	if e != nil {
		return report, e
	}
	defer os.RemoveAll(private)
	env, e := backupPostgresEnv(dsn, private, tools.LibraryPath)
	if e != nil {
		return report, e
	}
	restore := tools.Restore
	if restore == "" {
		restore = "pg_restore"
	}
	cmd := exec.CommandContext(ctx, restore, "--no-password", "--single-transaction", "--exit-on-error", "--no-owner", "--no-privileges", "--dbname="+backupDatabaseName(env), filepath.Join(source, "database.dump"))
	cmd.Env = env
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	// Preserve the private staged objects if restoring/publishing has an uncertain
	// outcome; never drop or retry a possibly committed database automatically.
	preserve = true
	if e = cmd.Run(); e != nil {
		return report, errors.New("pg_restore failed; inspect new database and retained .postgres-restore staging before retry")
	}
	if e = verifyRestoredBackup(ctx, conn, manifest); e != nil {
		return report, e
	}
	if e = os.WriteFile(filepath.Join(stage, "restore-manifest.json"), []byte(`{"version":1,"verified":true}`), 0600); e != nil {
		return report, e
	}
	if e = syncMigrationDirectory(stage); e != nil {
		return report, e
	}
	if e = renameDirectoryNoReplace(stage, destination); e != nil {
		return report, errors.New("database restored but directory publication failed; retained staging requires inspection")
	}
	preserve = false
	return report, nil
}
func backupDatabaseName(env []string) string {
	for _, s := range env {
		if strings.HasPrefix(s, "PGDATABASE=") {
			return strings.TrimPrefix(s, "PGDATABASE=")
		}
	}
	return ""
}
func verifyRestoredBackup(ctx context.Context, conn *sql.Conn, m PostgresBackupManifest) error {
	rows, e := conn.QueryContext(ctx, "SELECT key,size,sha256 FROM studio_blobs")
	if e != nil {
		return errors.New("restored blob manifest unavailable")
	}
	seen := 0
	for rows.Next() {
		var i BlobInfo
		if e = rows.Scan(&i.Key, &i.Size, &i.SHA256); e != nil {
			rows.Close()
			return e
		}
		if expected, ok := m.Objects[i.Key]; !ok || expected != i {
			rows.Close()
			return ErrBlobIntegrity
		}
		seen++
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil || seen != len(m.Objects) {
		return ErrBlobIntegrity
	}
	jobs, e := backupStateCounts(ctx, conn, "execution_jobs", "state")
	if e != nil {
		return e
	}
	costs, e := backupStateCounts(ctx, conn, "studio_cost_entries", "state")
	if e != nil {
		return e
	}
	a, _ := json.Marshal(jobs)
	b, _ := json.Marshal(m.QueueStates)
	c, _ := json.Marshal(costs)
	d, _ := json.Marshal(m.CostStates)
	if string(a) != string(b) || string(c) != string(d) {
		return errors.New("restored queue or cost states differ")
	}
	return nil
}

func openBackupObjects(path string) (*FileBlobStore, error) {
	st, e := os.Lstat(path)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("backup objects directory missing or unsafe")
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, e
	}
	return &FileBlobStore{root: root}, nil
}
