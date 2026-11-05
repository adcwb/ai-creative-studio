package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var errMultipartKeep = errors.New("S3 multipart checkpoint still current")

func (s *S3BlobStore) multipartScope() string {
	return s3Hash(s.endpoint.String() + "\n" + s.config.Bucket + "\n" + s.config.Region)
}

// CleanupMultipart collects expired *transfers*, never accepted objects or
// project history. The batch size bounds maintenance work, not user storage.
// Active writers are skipped rather than interrupted or made to wait for GC.
func (s *S3BlobStore) CleanupMultipart(ctx context.Context, before time.Time, limit int) (int, error) {
	if s.multipart == nil || limit < 1 || limit > 100 {
		return 0, errMultipartState
	}
	candidates, e := s.multipart.expired(ctx, s.multipartScope(), before, limit)
	if e != nil {
		return 0, e
	}
	n := 0
	var failures error
	for _, initial := range candidates {
		if e = ctx.Err(); e != nil {
			return n, errors.Join(failures, e)
		}
		e = s.multipart.withMode(ctx, initial, false, func(ctx context.Context, c *s3Checkpoint) error {
			v := c.state
			if v.Stage != "aborting" && v.Updated.After(before) {
				return errMultipartKeep
			}
			// A lost initiation response may have created an upload. Resolve only
			// the unique new ID in our private namespace; do not abort foreign IDs.
			if v.Stage == "initiating" && v.UploadID == "" {
				ids, e := s.listMultipart(ctx, v.Info.Key)
				if e != nil {
					return e
				}
				old := map[string]bool{}
				for _, id := range v.Before {
					old[id] = true
				}
				for _, id := range ids {
					if !old[id] {
						if v.UploadID != "" {
							return errS3Ambiguous
						}
						v.UploadID = id
					}
				}
			}
			v.Stage = "aborting"
			if e := c.save(v); e != nil {
				return e
			}
			return s.abortMultipart(ctx, c.state)
		})
		if e == nil {
			n++
		} else if !errors.Is(e, errMultipartBusy) && !errors.Is(e, errMultipartKeep) {
			failures = errors.Join(failures, e)
		}
	}
	return n, failures
}

func (j *s3MultipartJournal) expired(ctx context.Context, scope string, before time.Time, limit int) ([]s3MultipartState, error) {
	out := []s3MultipartState{}
	appendState := func(b []byte) error {
		var v s3MultipartState
		if json.Unmarshal(b, &v) != nil || !validS3State(v) {
			return errMultipartState
		}
		if v.Scope == scope && (v.Stage == "aborting" || !v.Updated.After(before)) {
			out = append(out, v)
		}
		return nil
	}
	if j.db != nil {
		rows, e := j.db.QueryContext(ctx, `SELECT payload FROM studio_blob_multipart WHERE config_hash=$1 AND lease_until<=now() AND (updated_at<=$2 OR payload->>'stage'='aborting') ORDER BY updated_at,key LIMIT $3`, scope, before, limit)
		if e != nil {
			return nil, errMultipartState
		}
		defer rows.Close()
		for rows.Next() {
			var b []byte
			if rows.Scan(&b) != nil || appendState(b) != nil {
				return nil, errMultipartState
			}
		}
		return out, rows.Err()
	}
	d, e := os.Open(j.dir)
	if e != nil {
		return nil, errMultipartState
	}
	defer d.Close()
	for len(out) < limit {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		entries, readErr := d.ReadDir(100)
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			b, e := safeReadLocal(filepath.Join(j.dir, entry.Name()), 2<<20)
			if os.IsNotExist(e) { // another writer completed after directory listing
				continue
			}
			if e != nil || appendState(b) != nil {
				return nil, errMultipartState
			}
			var identity s3MultipartState
			if json.Unmarshal(b, &identity) != nil || entry.Name() != s3StateName(identity.Scope, identity.Info.Key)+".json" {
				return nil, errMultipartState
			}
			if len(out) == limit {
				break
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return nil, errMultipartState
			}
			break
		}
	}
	return out, nil
}

func (s *S3BlobStore) startMultipartCleanup() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.cleanupCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cleanupCancel = cancel
	done := make(chan struct{})
	s.cleanupDone = done
	go func() {
		defer close(done)
		tick := time.NewTicker(time.Hour)
		defer tick.Stop()
		for {
			work, stop := context.WithTimeout(ctx, time.Minute)
			_, e := s.CleanupMultipart(work, time.Now().Add(-7*24*time.Hour), 100)
			stop()
			if e != nil && ctx.Err() == nil {
				log.Print("S3 multipart maintenance incomplete; checkpoints retained for retry")
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

func (s *S3BlobStore) Close() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.cleanupCancel != nil {
		s.cleanupCancel()
		<-s.cleanupDone
		s.cleanupCancel = nil
		s.cleanupDone = nil
	}
	s.client.CloseIdleConnections()
	return nil
}
