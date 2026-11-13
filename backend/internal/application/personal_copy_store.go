package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/revisionrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

type personalCopySnapshot struct {
	Project   Project               `json:"project"`
	Media     MediaState            `json:"media"`
	History   []ProjectRevision     `json:"history"`
	Revisions []revisionrepo.Record `json:"revisions"`
	Files     []personalCopyFile    `json:"files"`
	Sources   []media.LibrarySource `json:"library_sources"`
}
type personalCopyFile struct {
	Source string `json:"source"`
	Target string `json:"target"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type personalCopyJob struct {
	ID          string               `json:"id"`
	Space       string               `json:"space_id"`
	ProjectID   string               `json:"project_id"`
	Actor       string               `json:"created_by"`
	TargetSpace string               `json:"target_space_id"`
	TargetID    string               `json:"target_project_id"`
	Version     int                  `json:"version"`
	State       string               `json:"state"`
	SHA256      string               `json:"preview_sha256"`
	Revoked     bool                 `json:"revoked"`
	Snapshot    personalCopySnapshot `json:"snapshot"`
	Copied      map[string]bool      `json:"copied"`
	Verified    map[string]bool      `json:"verified"`
	CreatedAt   time.Time            `json:"created_at"`
}
type personalCopyDisk struct {
	Version int                        `json:"version"`
	Jobs    map[string]personalCopyJob `json:"jobs"`
}
type personalCopyStore struct {
	mu      stateMutex
	pg      *postgresScope
	path    string
	data    personalCopyDisk
	failure error
}

func openPersonalCopies(dir string, db *sql.DB) (*personalCopyStore, error) {
	s := &personalCopyStore{path: filepath.Join(dir, "personal-copies.json"), data: personalCopyDisk{Version: 1, Jobs: map[string]personalCopyJob{}}}
	if db != nil {
		var e error
		s.pg, e = attachPostgres(db, "personal_copies", &s.data, &s.mu)
		if e != nil {
			return nil, e
		}
		if e = validatePersonalCopies(s.data); e != nil {
			return nil, e
		}
		s.pg.replace = func(raw []byte) error {
			var d personalCopyDisk
			if e := json.Unmarshal(raw, &d); e != nil {
				return e
			}
			if e := validatePersonalCopies(d); e != nil {
				return e
			}
			s.data = d
			return nil
		}
		return s, nil
	}
	f, e := openLocalBlob(s.path)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(f)
	if e != nil {
		return nil, e
	}
	if json.Unmarshal(b, &s.data) != nil || validatePersonalCopies(s.data) != nil {
		return nil, errors.New("invalid personal copy state")
	}
	return s, nil
}
func (s *personalCopyStore) clone() personalCopyDisk {
	b, _ := json.Marshal(s.data)
	var d personalCopyDisk
	_ = json.Unmarshal(b, &d)
	return d
}
func (s *personalCopyStore) commit(d personalCopyDisk) (err error) {
	if err = validatePersonalCopies(d); err != nil {
		return err
	}
	if s.failure != nil {
		return s.failure
	}
	if s.pg != nil {
		if err = s.pg.save(d); err == nil {
			s.data = d
		}
		return err
	}
	defer func() {
		if err != nil {
			s.failure = err
		}
	}()
	f, e := os.CreateTemp(filepath.Dir(s.path), ".personal-copy-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(d)
	if err == nil {
		err = nativefs.SyncFile(f)
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = nativefs.Rename(f.Name(), s.path)
	}
	if err == nil {
		err = syncMultipartDir(filepath.Dir(s.path))
	}
	if err == nil {
		s.data = d
	}
	return err
}

func validatePersonalCopies(d personalCopyDisk) error {
	bad := errors.New("invalid personal copy state")
	if d.Version != 1 || d.Jobs == nil {
		return bad
	}
	for id, j := range d.Jobs {
		if id != j.ID || !library.IDPattern.MatchString(id) || !library.IDPattern.MatchString(j.Actor) || !library.IDPattern.MatchString(j.Space) || !library.IDPattern.MatchString(j.TargetSpace) || j.Space == j.TargetSpace || j.TargetID != copyIdentity(j.ID, "project", j.ProjectID) || j.ProjectID != j.Snapshot.Project.ID || j.Space != j.Snapshot.Project.SpaceID || j.Version < 1 || j.Copied == nil || j.Verified == nil || j.CreatedAt.IsZero() || (j.State != "copying" && j.State != "completed") {
			return bad
		}
		snap := j.Snapshot
		snap.Files = append([]personalCopyFile{}, snap.Files...)
		files := map[string]bool{}
		for i, f := range snap.Files {
			if !validMediaBlobName(f.Source) || !validMediaBlobName(f.Target) || len(f.SHA256) != 64 || f.Size < 0 || files[f.Source] {
				return bad
			}
			files[f.Source] = true
			snap.Files[i].Target = ""
			base, ext := f.Source, ""
			for _, suffix := range []string{".mp4", ".srt", ".vtt"} {
				if strings.HasSuffix(base, suffix) {
					base = strings.TrimSuffix(base, suffix)
					ext = suffix
					break
				}
			}
			if f.Target != copyIdentity(j.ID, "media", base)+ext {
				return bad
			}
		}
		if personalCopyDigest(j.Space, j.TargetSpace, snap) != j.SHA256 {
			return bad
		}
		for key, value := range j.Copied {
			if !value || !files[key] {
				return bad
			}
		}
		for key, value := range j.Verified {
			if !value || !j.Copied[key] || !files[key] {
				return bad
			}
		}
		if j.State == "completed" && (len(j.Copied) != len(files) || len(j.Verified) != len(files)) {
			return bad
		}
		for _, v := range snap.Sources {
			if v.ShareID != "" || !library.IDPattern.MatchString(v.AssetID) || !library.IDPattern.MatchString(v.VersionID) {
				return bad
			}
		}
	}
	return nil
}
func (s *personalCopyStore) get(id string) (personalCopyJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return personalCopyJob{}, s.failure
	}
	if s.pg != nil && s.pg.err != nil {
		return personalCopyJob{}, s.pg.err
	}
	v, ok := s.clone().Jobs[id]
	if !ok {
		return v, ErrNotFound
	}
	return v, nil
}
func (s *personalCopyStore) change(ctx context.Context, id string, fn func(*personalCopyDisk) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if s.pg != nil && s.pg.err != nil {
		return s.pg.err
	}
	d := s.clone()
	if e := fn(&d); e != nil {
		return e
	}
	return s.commit(d)
}
