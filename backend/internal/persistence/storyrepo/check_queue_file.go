package storyrepo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

// The pending index is disposable scheduling metadata. The accepted job file
// remains authoritative. Intents are linked BEFORE that file while holding its
// series lock, so a committed queued job can never be omitted by a crash.
type checkIntent struct {
	Format  int    `json:"format"`
	Space   string `json:"space_id"`
	Series  string `json:"series_id"`
	Episode string `json:"episode_id"`
	ID      string `json:"check_id"`
}

func (s *Store) checkQueueDir() string { return s.dir + "-check-queue" }
func validIntent(r checkIntent) bool {
	return r.Format == 1 && validScope(Scope{r.Space, r.Series}) && story.ID.MatchString(r.Episode) && story.ID.MatchString(r.ID)
}
func (s *Store) readIntent(id string) (checkIntent, error) {
	var r checkIntent
	if !story.ID.MatchString(id) || realDir(s.checkQueueDir()) != nil {
		return r, ErrStorage
	}
	f, e := nativefs.OpenFile(filepath.Join(s.checkQueueDir(), id+".json"), os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
	if e != nil {
		return r, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return r, ErrStorage
	}
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || !validIntent(r) || r.ID != id {
		return r, ErrStorage
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return r, ErrStorage
	}
	return r, nil
}
func (s *Store) ensureCheckIntent(c story.Check) error {
	dir := s.checkQueueDir()
	if e := os.Mkdir(dir, 0700); e != nil && !os.IsExist(e) {
		return ErrStorage
	}
	if realDir(dir) != nil || syncDir(filepath.Dir(dir)) != nil {
		return ErrStorage
	}
	expected := checkIntent{1, c.Space, c.SeriesID, c.EpisodeID, c.ID}
	old, e := s.readIntent(c.ID)
	if e == nil {
		if old != expected {
			return ErrStorage
		}
		return nil
	}
	if !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(dir, ".pending-")
	if e != nil {
		return ErrStorage
	}
	defer os.Remove(f.Name())
	e = json.NewEncoder(f).Encode(expected)
	if e == nil {
		e = nativefs.SyncFile(f)
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return ErrStorage
	}
	if os.Link(f.Name(), filepath.Join(dir, c.ID+".json")) != nil {
		return ErrStorage
	}
	return syncDir(dir)
}
func (s *Store) removeCheckIntent(id string) error {
	if !story.ID.MatchString(id) {
		return ErrStorage
	}
	e := os.Remove(filepath.Join(s.checkQueueDir(), id+".json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return ErrStorage
	}
	return syncDir(s.checkQueueDir())
}
func (s *Store) nextIntent(ctx context.Context) (CheckRecord, bool, error) {
	var out CheckRecord
	dir := s.checkQueueDir()
	if e := realDir(dir); os.IsNotExist(e) {
		return out, false, nil
	} else if e != nil {
		return out, false, ErrStorage
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return out, false, e
	}
	for _, entry := range entries {
		if e = ctx.Err(); e != nil {
			return out, false, e
		}
		if strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !story.ID.MatchString(id) || entry.Name() != id+".json" || !entry.Type().IsRegular() {
			return out, false, ErrStorage
		}
		intent, e := s.readIntent(id)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return out, false, e
		}
		q := Scope{intent.Space, intent.Series}
		taken := false
		e = s.withFile(ctx, q, true, func() error {
			r, e := s.readCheck(q, intent.Episode, id)
			if errors.Is(e, story.ErrNotFound) {
				return s.removeCheckIntent(id)
			}
			if e != nil {
				return e
			}
			if r.Check.State == "running" {
				return nil
			}
			if r.Check.State != "queued" {
				return s.removeCheckIntent(id)
			}
			r.Check.State = "running"
			r.Check.UpdatedAt = time.Now().UTC()
			if e = s.writeCheck(q, r, false); e != nil {
				return e
			}
			out = r
			taken = true
			return nil
		})
		if e != nil {
			return out, false, e
		}
		if taken {
			return out, true, nil
		}
	}
	return out, false, nil
}
