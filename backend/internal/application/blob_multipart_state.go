package application

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

var errMultipartState = errors.New("S3 multipart checkpoint unavailable; retain the source and retry")
var errMultipartFence = errors.New("S3 multipart checkpoint ownership lost")
var errMultipartBusy = errors.New("S3 multipart checkpoint is active")

const S3MultipartSchema = `CREATE TABLE IF NOT EXISTS studio_blob_multipart (
 config_hash text NOT NULL CHECK(config_hash ~ '^[a-f0-9]{64}$'), key text NOT NULL,
 lease_owner text NOT NULL DEFAULT '',lease_until timestamptz NOT NULL DEFAULT '-infinity',
 updated_at timestamptz NOT NULL,payload jsonb NOT NULL,
 CHECK((payload->>'scope'=config_hash AND payload->'info'->>'key'=key) IS TRUE),PRIMARY KEY(config_hash,key));
 CREATE INDEX IF NOT EXISTS studio_blob_multipart_cleanup ON studio_blob_multipart(updated_at);`

type s3MultipartState struct {
	Version   int       `json:"version"`
	Scope     string    `json:"scope"`
	Info      BlobInfo  `json:"info"`
	PartBytes int64     `json:"part_bytes"`
	Stage     string    `json:"stage"`
	UploadID  string    `json:"upload_id,omitempty"`
	Before    []string  `json:"before,omitempty"`
	Updated   time.Time `json:"updated_at"`
}
type s3MultipartJournal struct {
	dir string
	db  *sql.DB
}
type s3Checkpoint struct {
	state s3MultipartState
	save  func(s3MultipartState) error
}

func validS3State(v s3MultipartState) bool {
	scope, e := hex.DecodeString(v.Scope)
	if e != nil || len(scope) != 32 || v.Scope != strings.ToLower(v.Scope) {
		return false
	}
	for _, id := range v.Before {
		if id == "" || len(id) > 4096 || strings.ContainsAny(id, "\x00\r\n") {
			return false
		}
	}
	return v.Version == 1 && validBlobInfo(v.Info) && v.PartBytes >= 5<<20 && v.PartBytes <= 5<<30 && v.PartBytes*(10000) >= v.Info.Size && (v.Stage == "new" || v.Stage == "initiating" || v.Stage == "uploading" || v.Stage == "completing" || v.Stage == "aborting") && len(v.UploadID) <= 4096 && !strings.ContainsAny(v.UploadID, "\x00\r\n") && !v.Updated.IsZero()
}
func (s *S3BlobStore) configureMultipart(dir string, db *sql.DB) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.cleanupCancel != nil {
		return errors.New("S3 multipart journal already serving requests")
	}
	if db == nil {
		if e := os.MkdirAll(dir, 0700); e != nil {
			return e
		}
	}
	s.multipart = &s3MultipartJournal{dir, db}
	return nil
}
func s3StateName(scope, key string) string { return s3Hash(scope + ":" + key) }
func writeS3State(dir, name string, v s3MultipartState) error {
	if !validS3State(v) {
		return errMultipartState
	}
	b, e := json.Marshal(v)
	if e != nil || len(b) > 2<<20 {
		return errMultipartState
	}
	f, e := os.CreateTemp(dir, ".pending-")
	if e != nil {
		return errMultipartState
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if e == nil {
		e = nativefs.SyncFile(f)
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return errMultipartState
	}
	if e = nativefs.Rename(f.Name(), filepath.Join(dir, name+".json")); e != nil {
		return errMultipartState
	}
	return syncMultipartDir(dir)
}
func syncMultipartDir(dir string) error {
	f, e := os.Open(dir)
	if e != nil {
		return errMultipartState
	}
	defer f.Close()
	if nativefs.SyncFile(f) != nil {
		return errMultipartState
	}
	return nil
}
func (j *s3MultipartJournal) with(ctx context.Context, initial s3MultipartState, fn func(context.Context, *s3Checkpoint) error) error {
	return j.withMode(ctx, initial, true, fn)
}
func (j *s3MultipartJournal) withMode(ctx context.Context, initial s3MultipartState, wait bool, fn func(context.Context, *s3Checkpoint) error) error {
	if j.db != nil {
		return j.withSQL(ctx, initial, wait, fn)
	}
	name := s3StateName(initial.Scope, initial.Info.Key)
	// Fixed lock stripes avoid one permanent lock inode per historical upload.
	// Lock files themselves must not be unlinked while another process waits.
	f, e := nativefs.OpenFile(filepath.Join(j.dir, name[:2]+".lock"), os.O_CREATE|os.O_RDWR|nativefs.NoFollow, 0600)
	if e != nil {
		return errMultipartState
	}
	defer f.Close()
	if st, e := f.Stat(); e != nil || !st.Mode().IsRegular() {
		return errMultipartState
	}
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		e = nativefs.Flock(int(f.Fd()), nativefs.LOCK_EX|nativefs.LOCK_NB)
		if e == nil {
			break
		}
		if !errors.Is(e, nativefs.ErrWouldBlock) {
			return errMultipartState
		}
		if !wait {
			return errMultipartBusy
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer nativefs.Flock(int(f.Fd()), nativefs.LOCK_UN)
	v := initial
	b, e := safeReadLocal(filepath.Join(j.dir, name+".json"), 2<<20)
	if e == nil {
		if json.Unmarshal(b, &v) != nil || !validS3State(v) || v.Scope != initial.Scope || v.Info != initial.Info {
			return errMultipartState
		}
	} else if !os.IsNotExist(e) {
		return errMultipartState
	}
	c := &s3Checkpoint{state: v}
	c.save = func(next s3MultipartState) error {
		next.Updated = time.Now().UTC()
		if e := writeS3State(j.dir, name, next); e != nil {
			return e
		}
		c.state = next
		return nil
	}
	if e = fn(ctx, c); e != nil {
		return e
	}
	if e = os.Remove(filepath.Join(j.dir, name+".json")); e != nil && !os.IsNotExist(e) {
		return errMultipartState
	}
	return syncMultipartDir(j.dir)
}
func (j *s3MultipartJournal) withSQL(ctx context.Context, initial s3MultipartState, wait bool, fn func(context.Context, *s3Checkpoint) error) error {
	owner := mediaID()
	var payload []byte
	for {
		tx, e := j.db.BeginTx(ctx, nil)
		if e != nil {
			return errMultipartState
		}
		if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
			tx.Rollback()
			return errMultipartState
		}
		b, _ := json.Marshal(initial)
		e = tx.QueryRowContext(ctx, `INSERT INTO studio_blob_multipart(config_hash,key,lease_owner,lease_until,updated_at,payload) VALUES($1,$2,$3,now()+interval '2 minutes',now(),$4) ON CONFLICT(config_hash,key) DO UPDATE SET lease_owner=EXCLUDED.lease_owner,lease_until=EXCLUDED.lease_until WHERE studio_blob_multipart.lease_until<=now() RETURNING payload`, initial.Scope, initial.Info.Key, owner, b).Scan(&payload)
		if errors.Is(e, sql.ErrNoRows) {
			tx.Rollback()
			if !wait {
				return errMultipartBusy
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		if e != nil {
			tx.Rollback()
			return errMultipartState
		}
		if e = tx.Commit(); e != nil {
			return errMultipartState
		}
		break
	}
	// Every checkpoint uses a short transaction; no DB connection/transaction is
	// held during network transfer. Tokens fence an expired/crashed writer.
	change := func(work context.Context, query string, args ...any) error {
		tx, e := j.db.BeginTx(work, nil)
		if e != nil {
			return errMultipartState
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(work, `SET LOCAL synchronous_commit=on; SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
			return errMultipartState
		}
		r, e := tx.ExecContext(work, query, args...)
		if e != nil {
			return errMultipartState
		}
		n, e := r.RowsAffected()
		if e != nil || n != 1 {
			return errMultipartFence
		}
		if e = tx.Commit(); e != nil {
			return errMultipartState
		}
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = change(cleanup, `UPDATE studio_blob_multipart SET lease_owner='',lease_until=now() WHERE config_hash=$1 AND key=$2 AND lease_owner=$3`, initial.Scope, initial.Info.Key, owner)
	}()
	var v s3MultipartState
	if json.Unmarshal(payload, &v) != nil || !validS3State(v) || v.Scope != initial.Scope || v.Info != initial.Info {
		return errMultipartState
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	leaseCtx, stopLease := context.WithCancel(work)
	done := make(chan struct{})
	var lost atomic.Bool
	go func() {
		defer close(done)
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-tick.C:
				if e := change(leaseCtx, `UPDATE studio_blob_multipart SET lease_until=now()+interval '2 minutes' WHERE config_hash=$1 AND key=$2 AND lease_owner=$3 AND lease_until>now()`, initial.Scope, initial.Info.Key, owner); e != nil {
					if leaseCtx.Err() == nil {
						lost.Store(true)
						cancel()
					}
					return
				}
			}
		}
	}()
	c := &s3Checkpoint{state: v}
	c.save = func(next s3MultipartState) error {
		next.Updated = time.Now().UTC()
		if !validS3State(next) {
			return errMultipartState
		}
		b, e := json.Marshal(next)
		if e != nil || len(b) > 2<<20 {
			return errMultipartState
		}
		e = change(work, `UPDATE studio_blob_multipart SET payload=$4,updated_at=now(),lease_until=now()+interval '2 minutes' WHERE config_hash=$1 AND key=$2 AND lease_owner=$3 AND lease_until>now()`, next.Scope, next.Info.Key, owner, b)
		if e == nil {
			c.state = next
		}
		return e
	}
	e := fn(work, c)
	stopLease()
	<-done
	if lost.Load() {
		return errMultipartFence
	}
	if e != nil {
		return e
	}
	return change(ctx, `DELETE FROM studio_blob_multipart WHERE config_hash=$1 AND key=$2 AND lease_owner=$3 AND lease_until>now()`, v.Scope, v.Info.Key, owner)
}

// Transfer checkpoints contain object identities, never endpoint credentials.
func importS3Multipart(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	entries, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return 0, nil
	}
	if e != nil {
		return 0, e
	}
	var existing int
	if tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_blob_multipart`).Scan(&existing) != nil || existing != 0 {
		return 0, errMultipartState
	}
	n := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") || strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
			return 0, errMultipartState
		}
		b, e := safeReadLocal(filepath.Join(dir, entry.Name()), 2<<20)
		var v s3MultipartState
		if e != nil || json.Unmarshal(b, &v) != nil || !validS3State(v) || entry.Name() != s3StateName(v.Scope, v.Info.Key)+".json" {
			return 0, errMultipartState
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO studio_blob_multipart(config_hash,key,updated_at,payload) VALUES($1,$2,$3,$4)`, v.Scope, v.Info.Key, v.Updated, b); e != nil {
			return 0, e
		}
		n++
	}
	return n, nil
}
func exportS3Multipart(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return 0, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT payload FROM studio_blob_multipart ORDER BY config_hash,key`)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var b []byte
		var v s3MultipartState
		if rows.Scan(&b) != nil || json.Unmarshal(b, &v) != nil || !validS3State(v) {
			return 0, errMultipartState
		}
		if e = writeS3State(dir, s3StateName(v.Scope, v.Info.Key), v); e != nil {
			return 0, e
		}
		n++
	}
	return n, rows.Err()
}
