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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
	"golang.org/x/sys/unix"
)

type MigrationReport struct {
	Version           int               `json:"version"`
	Direction         string            `json:"direction"`
	Records           map[string]int    `json:"records"`
	SHA256            map[string]string `json:"sha256"`
	MediaFiles        map[string]string `json:"media_files"`
	RequiresMediaCopy bool              `json:"requires_media_copy"`
	Notes             []string          `json:"notes"`
}

var postgresSnapshotFiles = map[string]string{"core": "state.json", "auth": "auth.json", "budget": "budget.json", "models": "model-config.json", "creative": "creative.json", "media": "media/state.json", "alignment": "media/alignments.json"}

func postgresTemplates() map[string]any {
	return map[string]any{
		"core": emptySnapshot(), "auth": authSnapshot{Version: 1, Users: map[string]authAccount{}, Organizations: map[string]AuthOrganization{}, Sessions: map[string]authSession{}}, "budget": costSnapshot{Version: 1, Limits: map[string]int64{}, Entries: map[string]CostEntry{}}, "models": modelSnapshot{Version: 1, Tenants: map[string]modelTenant{}}, "creative": creativeDisk{Version: 1, Jobs: map[string]CreativeJob{}, Receipts: map[string]creativeReceipt{}}, "media": mediaDisk{Version: 1, Projects: map[string]MediaState{}, Receipts: map[string]mediaReceipt{}}, "alignment": alignmentDisk{Version: 1, Tasks: map[string]AlignmentTask{}, Receipts: map[string]alignmentReceipt{}}}
}
func migrationDigest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func migrationMediaInventory(dir string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("migration refuses symbolic links")
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("migration refuses non-regular files")
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		if rel == "store.lock" {
			return nil
		}
		if info.Size() > 256<<20 {
			return errors.New("migration file exceeds safe bound")
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(h, io.LimitReader(f, (256<<20)+1))
		e = errors.Join(e, f.Close())
		if e != nil {
			return e
		}
		result[filepath.ToSlash(rel)] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return result, err
}

// ImportPostgresData is OFFLINE: all API/worker processes must be stopped. The
// source lock prevents a file writer and a DB-wide advisory barrier prevents
// concurrent domain mutations. Import requires an empty destination and commits
// every domain plus queued task identities in one transaction. Original files
// are never rewritten. Binary media remains at source until explicit blob copy.
func ImportPostgresData(ctx context.Context, dir string, db *sql.DB) (MigrationReport, error) {
	report := MigrationReport{Version: 1, Direction: "file-to-postgres", Records: map[string]int{}, SHA256: map[string]string{}, RequiresMediaCopy: true, Notes: []string{"all API and worker processes must be stopped", "binary media must be copied/verified separately before switching", "running remote requests become unknown and are never automatically dispatched again"}}
	if info, e := os.Lstat(dir); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return report, errors.New("source must be a real directory")
	}
	lock, err := os.OpenFile(filepath.Join(dir, "store.lock"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return report, errors.New("source store lock unavailable")
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return report, errors.New("source store is running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	report.MediaFiles, err = migrationMediaInventory(dir)
	if err != nil {
		return report, err
	}
	templates := postgresTemplates()
	scopes := []string{"auth", "budget", "models", "core", "creative", "media", "alignment"}
	values := map[string]json.RawMessage{}
	// Validate using an isolated copy of ONLY metadata. Constructors may normalize
	// their copy for validation; the source is not opened by a mutating store.
	validation, err := os.MkdirTemp("", "studio-migrate-validate-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(validation)
	for _, name := range scopes {
		path := postgresSnapshotFiles[name]
		raw, e := safeReadLocal(filepath.Join(dir, path), 32<<20)
		if os.IsNotExist(e) {
			if name == "core" {
				return report, errors.New("source core state.json is required; refusing partial migration")
			}
			raw, e = json.Marshal(templates[name])
		}
		if e != nil {
			return report, e
		}
		if len(raw) > 32<<20 {
			return report, errors.New("metadata exceeds migration bound")
		}
		if !json.Valid(raw) {
			return report, errors.New("invalid migration JSON")
		}
		values[name] = raw
		if e = os.MkdirAll(filepath.Dir(filepath.Join(validation, path)), 0700); e != nil {
			return report, e
		}
		if e = os.WriteFile(filepath.Join(validation, path), raw, 0600); e != nil {
			return report, e
		}
	}
	check, err := OpenStore(validation)
	if err != nil {
		return report, err
	}
	if err = check.Close(); err != nil {
		return report, err
	}
	if _, err = OpenAuthStore(validation); err != nil {
		return report, err
	}
	// Use the core constructor's compatible v1/v2 -> v3 normalization, including
	// conservative interrupted-state handling. Other modules retain exact data.
	values["core"], err = os.ReadFile(filepath.Join(validation, "state.json"))
	if err != nil {
		return report, err
	}
	for _, name := range []string{"creative", "media", "alignment"} {
		values[name], err = os.ReadFile(filepath.Join(validation, postgresSnapshotFiles[name]))
		if err != nil {
			return report, err
		}
	}
	var budget costSnapshot
	if err = json.Unmarshal(values["budget"], &budget); err != nil {
		return report, err
	}
	for id, e := range budget.Entries {
		if e.State == "reserved" {
			e.State = "unknown"
			e.UpdatedAt = time.Now().UTC()
			budget.Entries[id] = e
		}
	}
	values["budget"], _ = json.Marshal(budget)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return report, errors.New("postgres migration unavailable")
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(728219117)`); err != nil {
		return report, err
	}
	for _, cols := range pgCollections {
		for _, c := range cols {
			var n int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+c.table).Scan(&n); err != nil {
				return report, err
			}
			if n != 0 {
				return report, errors.New("destination PostgreSQL is not empty")
			}
		}
	}
	for _, table := range []string{"execution_jobs", "studio_blobs", "studio_scopes", "studio_audit_events"} {
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			return report, err
		}
		if n != 0 {
			return report, errors.New("destination PostgreSQL is not empty")
		}
	}
	for _, name := range scopes {
		raw := values[name]
		env, records, e := splitPostgres(raw, pgCollections[name])
		if e != nil {
			return report, e
		}
		envelope, _ := json.Marshal(env)
		if _, err = tx.ExecContext(ctx, `INSERT INTO studio_scopes(name,envelope) VALUES($1,$2)`, name, envelope); err != nil {
			return report, err
		}
		p := &postgresScope{name: name, ctx: ctx, tx: tx, before: map[string]map[string]json.RawMessage{}}
		if err = p.write(json.RawMessage(raw)); err != nil {
			return report, err
		}
		for field, rows := range records {
			report.Records[name+"."+field] = len(rows)
		}
		report.SHA256[postgresSnapshotFiles[name]] = migrationDigest(raw)
	}
	if err = importQueuedTasks(ctx, tx, values); err != nil {
		return report, err
	}
	report.Records["library"], err = libraryrepo.ImportFiles(ctx, tx, filepath.Join(dir, "library"))
	if err != nil {
		return report, err
	}
	// The manifest contains metadata only; S3 credentials and objects are external.
	manifestPath := filepath.Join(dir, "blobs.json")
	if raw, e := safeReadLocal(manifestPath, 32<<20); e == nil {
		var entries map[string]BlobInfo
		if json.Unmarshal(raw, &entries) != nil {
			return report, errors.New("invalid blob manifest")
		}
		for key, v := range entries {
			if key != v.Key || !validBlobInfo(v) {
				return report, errors.New("invalid blob manifest entry")
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO studio_blobs(key,tenant_hash,project_hash,size,sha256) VALUES($1,$2,$3,$4,$5)`, v.Key, strings.Split(v.Key, "/")[0], strings.Split(v.Key, "/")[1], v.Size, v.SHA256); err != nil {
				return report, err
			}
		}
		report.Records["blobs"] = len(entries)
	} else if !os.IsNotExist(e) {
		return report, e
	}
	report.Records["audit"], err = importPostgresAudit(ctx, tx, dir)
	if err != nil {
		return report, err
	}
	if err = tx.Commit(); err != nil {
		return report, errors.New("migration commit outcome uncertain; inspect destination before retry")
	}
	return report, nil
}

// ExportPostgresData writes metadata to a NEW directory only. It is a rollback
// building block, not an automatic full restore: retain/copy matching media or
// the verified S3 manifest before starting a file backend. KEKs remain external.
func ExportPostgresData(ctx context.Context, dir string, db *sql.DB) (MigrationReport, error) {
	report := MigrationReport{Version: 1, Direction: "postgres-to-file", Records: map[string]int{}, SHA256: map[string]string{}, RequiresMediaCopy: true, Notes: []string{"stop every API/worker before export", "copy matching binary media or retain verified S3 configuration before startup", "OIDC pending authorizations are intentionally invalidated; deployment KEK is not exported"}}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		return report, errors.New("export target must not exist")
	}
	parent := filepath.Dir(dir)
	stage, err := os.MkdirTemp(parent, ".studio-export-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(stage)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(728219117)`); err != nil {
		return report, err
	}
	uncertain, err := postgresExportUncertainJobs(ctx, tx)
	if err != nil {
		return report, err
	}
	report.Notes = append(report.Notes, "leased/dispatched/unknown and resume queue work is exported as failed, never automatically replayed")
	templates := postgresTemplates()
	names := make([]string, 0, len(templates))
	for n := range templates {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		raw, records, e := readPostgresScopeTx(ctx, tx, name, templates[name])
		if e != nil {
			return report, e
		}
		raw, e = sanitizePostgresExport(name, raw, uncertain)
		if e != nil {
			return report, e
		}
		path := filepath.Join(stage, postgresSnapshotFiles[name])
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return report, err
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			return report, err
		}
		report.SHA256[postgresSnapshotFiles[name]] = migrationDigest(raw)
		for field, rows := range records {
			report.Records[name+"."+field] = len(rows)
		}
	}
	if err = ExportBlobManifest(ctx, stage, db); err != nil {
		return report, err
	}
	report.Records["library"], err = libraryrepo.ExportFiles(ctx, tx, filepath.Join(stage, "library"))
	if err != nil {
		return report, err
	}
	report.Records["audit"], err = exportPostgresAudit(ctx, tx, stage)
	if err != nil {
		return report, err
	}
	if err = os.WriteFile(filepath.Join(stage, "store.lock"), nil, 0600); err != nil {
		return report, err
	}
	if err = tx.Commit(); err != nil {
		return report, errors.New("export transaction failed")
	}
	// Rename without replacement; callers cannot accidentally replace a live dir.
	if err = syncMigrationDirectory(stage); err != nil {
		return report, err
	}
	if err = renameDirectoryNoReplace(stage, dir); err != nil {
		return report, err
	}
	return report, nil
}

func renameDirectoryNoReplace(source, target string) error {
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
}
func importQueuedTasks(ctx context.Context, tx *sql.Tx, values map[string]json.RawMessage) error {
	var core snapshot
	if err := json.Unmarshal(values["core"], &core); err != nil {
		return err
	}
	enqueue := func(tenant, id, kind, key, project, traceParent string) error {
		b, _ := json.Marshal(map[string]string{"task_id": id, "project_id": project})
		return jobqueue.EnqueueTx(ctx, tx, jobqueue.Job{TenantID: tenant, ID: id, Kind: kind, IdempotencyKey: key, Payload: b, TraceParent: traceParent})
	}
	for _, t := range core.Tasks {
		if t.Status == "queued" {
			if err := enqueue(t.SpaceID, t.ID, "image", t.Key, "", t.TraceParent); err != nil {
				return err
			}
		}
	}
	for _, t := range core.ScriptGenerations {
		if t.Status == "queued" {
			if err := enqueue(t.SpaceID, t.ID, "script", t.ID, t.ProjectID, t.TraceParent); err != nil {
				return err
			}
		}
	}
	for _, t := range core.StoryboardGenerations {
		if t.Status == "queued" {
			if err := enqueue(t.SpaceID, t.ID, "storyboard", t.ID, t.ProjectID, t.TraceParent); err != nil {
				return err
			}
		}
	}
	return nil
}

func syncMigrationDirectory(dir string) error {
	var dirs []string
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			return e
		}
		return errors.Join(f.Sync(), f.Close())
	}); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		f, err := os.Open(dirs[i])
		if err != nil {
			return err
		}
		if err = errors.Join(f.Sync(), f.Close()); err != nil {
			return err
		}
	}
	return nil
}
