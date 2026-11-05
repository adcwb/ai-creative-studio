// Package promptrepo stores recipes per entity, never in an application scope.
package promptrepo

import (
	"context"
	"database/sql"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"sort"
	"strings"
	"time"
)

var ErrStorage = errors.New("prompt storage unavailable")
var ErrKey = errors.New("prompt idempotency conflict")

type Store struct {
	dir string
	db  *sql.DB
}
type Command struct {
	RequestIdentity string
	ChangeMetadata  bool
	Operation       string
	Metadata        prompts.Metadata
	Recipe          prompts.Recipe
	ExpectedRow     int64
	ExpectedHead    string
	Status          string
}
type Query struct {
	Text, Category, Tag, Source, Status, After string
	Favorite                                   bool
	Limit                                      int
}
type record struct {
	Format     int            `json:"format"`
	Actor      string         `json:"actor"`
	Operation  string         `json:"operation"`
	KeyHash    string         `json:"key_hash"`
	Digest     string         `json:"digest"`
	NewVersion bool           `json:"new_version"`
	Result     prompts.Result `json:"result"`
}

func commandDigest(c Command) string {
	if c.RequestIdentity != "" {
		return c.RequestIdentity
	}
	return prompts.Digest(c)
}

func Open(dir string, db *sql.DB) (*Store, error) {
	s := &Store{dir: dir, db: db}
	if db == nil {
		if e := s.initDir(); e != nil {
			return nil, e
		}
	}
	return s, nil
}
func validActor(a prompts.Actor) bool {
	return a.Space != "" && a.User != "" && len(a.Space) <= 128 && len(a.User) <= 128
}
func Visible(a prompts.Actor, r prompts.Result) error {
	if !prompts.CanRead(a, r.Template) {
		return prompts.ErrNotFound
	}
	return nil
}
func managingArchive(a prompts.Actor, c Command) bool {
	return a.Manage && a.Writable && c.Operation == "admin_archive"
}
func apply(a prompts.Actor, id, key string, c Command, old *prompts.Result) (record, error) {
	if !validActor(a) || !prompts.ID.MatchString(id) || !a.Writable {
		return record{}, prompts.ErrDenied
	}
	var out prompts.Result
	if old != nil {
		out = *old
		if e := Visible(a, out); e != nil && !managingArchive(a, c) {
			return record{}, e
		}
	}
	now := time.Now().UTC()
	newVersion := false
	switch c.Operation {
	case "create":
		if old != nil {
			return record{}, prompts.ErrConflict
		}
		m, e := prompts.NormalizeMetadata(c.Metadata)
		if e != nil {
			return record{}, e
		}
		r, e := prompts.NormalizeRecipe(c.Recipe)
		if e != nil {
			return record{}, e
		}
		out.Template = prompts.Template{Metadata: m, ID: id, SpaceID: a.Space, OwnerID: a.User, Status: "active", RowVersion: 1, CreatedAt: now, UpdatedAt: now}
		out.Version = prompts.Version{Recipe: r, ID: prompts.TextSHA(id + "\x00" + key)[:32], TemplateID: id, SpaceID: a.Space, Number: 1, CreatedBy: a.User, CreatedAt: now}
		newVersion = true
	case "metadata", "version", "admin_archive":
		if old == nil {
			return record{}, prompts.ErrNotFound
		}
		if !prompts.CanWrite(a, out.Template) && !managingArchive(a, c) {
			return record{}, prompts.ErrDenied
		}
		// Sharing or privatizing history is an owner decision, never an editor takeover.
		if c.Operation == "admin_archive" {
			if !managingArchive(a, c) {
				return record{}, prompts.ErrDenied
			}
			if c.ExpectedRow != out.Template.RowVersion {
				return record{}, prompts.ErrConflict
			}
			out.Template.Status = "archived"
		} else if c.Operation == "metadata" {
			if c.ExpectedRow != out.Template.RowVersion {
				return record{}, prompts.ErrConflict
			}
			m, e := prompts.NormalizeMetadata(c.Metadata)
			if e != nil {
				return record{}, e
			}
			if m.Visibility != out.Template.Visibility && a.User != out.Template.OwnerID {
				return record{}, prompts.ErrDenied
			}
			if c.Status != "active" && c.Status != "archived" {
				return record{}, prompts.ErrInput
			}
			out.Template.Metadata = m
			out.Template.Status = c.Status
		} else {
			if out.Template.Status == "archived" {
				return record{}, prompts.ErrArchived
			}
			if c.ExpectedHead != out.Template.HeadVersionID {
				return record{}, prompts.ErrConflict
			}
			if c.ChangeMetadata {
				if c.ExpectedRow != out.Template.RowVersion {
					return record{}, prompts.ErrConflict
				}
				m, e := prompts.NormalizeMetadata(c.Metadata)
				if e != nil {
					return record{}, e
				}
				if m.Visibility != out.Template.Visibility && a.User != out.Template.OwnerID {
					return record{}, prompts.ErrDenied
				}
				out.Template.Metadata = m
			}
			r, e := prompts.NormalizeRecipe(c.Recipe)
			if e != nil {
				return record{}, e
			}
			out.Version = prompts.Version{Recipe: r, ID: prompts.TextSHA(id + "\x00" + a.User + "\x00" + key)[:32], TemplateID: id, SpaceID: a.Space, Number: out.Version.Number + 1, CreatedBy: a.User, CreatedAt: now}
			newVersion = true
		}
		out.Template.RowVersion++
		out.Template.UpdatedAt = now
	default:
		return record{}, prompts.ErrInput
	}
	if newVersion {
		out.Version.SHA256 = prompts.Digest(out.Version.Recipe)
		out.Template.HeadVersionID = out.Version.ID
	}
	out.Favorite = false
	return record{1, a.User, c.Operation, prompts.TextSHA(a.User + "\x00" + c.Operation + "\x00" + key), commandDigest(c), newVersion, out}, nil
}
func (s *Store) Mutate(ctx context.Context, a prompts.Actor, id, key string, c Command) (prompts.Result, error) {
	if !validActor(a) || !prompts.ID.MatchString(id) || len(key) < 8 || len(key) > 128 {
		return prompts.Result{}, prompts.ErrInput
	}
	if !a.Writable {
		return prompts.Result{}, prompts.ErrDenied
	}
	if s.db != nil {
		return s.mutateSQL(ctx, a, id, key, c)
	}
	var result prompts.Result
	e := s.withLock(a.Space, true, func() error {
		current, e := s.currentFile(a.Space, id)
		if e != nil && !errors.Is(e, prompts.ErrNotFound) {
			return e
		}
		if current != nil {
			if e = Visible(a, *current); e != nil && !managingArchive(a, c) {
				return e
			}
		}
		hash := prompts.TextSHA(a.User + "\x00" + c.Operation + "\x00" + key)
		prior, e := s.receiptFile(a.Space, id, hash)
		if e != nil {
			return e
		}
		if prior != nil {
			if prior.Digest != commandDigest(c) {
				return ErrKey
			}
			result = prior.Result
			return nil
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		r, e := apply(a, id, key, c, current)
		if e != nil {
			return e
		}
		if e = s.writeRecord(r); e != nil {
			return e
		}
		result = r.Result
		return nil
	})
	return result, e
}
func (s *Store) Get(ctx context.Context, a prompts.Actor, id string) (prompts.Result, error) {
	if !validActor(a) || !prompts.ID.MatchString(id) {
		return prompts.Result{}, prompts.ErrNotFound
	}
	if s.db != nil {
		return s.getSQL(ctx, a, id)
	}
	var out prompts.Result
	e := s.withLock(a.Space, false, func() error {
		r, e := s.currentFile(a.Space, id)
		if e != nil {
			return e
		}
		if e = Visible(a, *r); e != nil {
			return e
		}
		out = *r
		out.Favorite = s.favoriteFile(a, id)
		return nil
	})
	return out, e
}
func (s *Store) Version(ctx context.Context, a prompts.Actor, id, version string) (prompts.Version, error) {
	if !prompts.ID.MatchString(version) {
		return prompts.Version{}, prompts.ErrNotFound
	}
	if _, e := s.Get(ctx, a, id); e != nil {
		return prompts.Version{}, e
	}
	if s.db != nil {
		return s.versionSQL(ctx, a, id, version)
	}
	var out prompts.Version
	e := s.withLock(a.Space, false, func() error {
		current, e := s.currentFile(a.Space, id)
		if e != nil {
			return e
		}
		if e = Visible(a, *current); e != nil {
			return e
		}
		entries, e := s.entityEntries(a.Space, id)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if strings.Contains(entry, "-v-"+version+"-") {
				r, e := s.readRecord(a.Space, id, entry)
				if e != nil {
					return e
				}
				out = r.Result.Version
				return nil
			}
		}
		return prompts.ErrNotFound
	})
	return out, e
}
func matches(a prompts.Actor, r prompts.Result, q Query) bool {
	t := r.Template
	if !prompts.CanRead(a, t) || t.Status != q.Status || (q.Source == "mine" && t.OwnerID != a.User) || (q.Source == "organization" && t.Visibility != "organization") || (q.Category != "" && q.Category != t.Category) || (q.Favorite && !r.Favorite) {
		return false
	}
	if q.Tag != "" {
		found := false
		for _, tag := range t.Tags {
			found = found || tag == q.Tag
		}
		if !found {
			return false
		}
	}
	return q.Text == "" || strings.Contains(strings.ToLower(t.Name+"\n"+t.Description+"\n"+r.Version.Body), strings.ToLower(q.Text))
}
func (s *Store) List(ctx context.Context, a prompts.Actor, q Query) ([]prompts.Result, error) {
	if !validActor(a) || q.Limit < 1 || q.Limit > 100 || (q.Source != "mine" && q.Source != "organization" && q.Source != "") || (q.Status != "active" && q.Status != "archived") || (q.After != "" && !prompts.ID.MatchString(q.After)) {
		return nil, prompts.ErrInput
	}
	if s.db != nil {
		return s.listSQL(ctx, a, q)
	}
	out := []prompts.Result{}
	e := s.withLock(a.Space, false, func() error {
		ids, e := s.entities(a.Space)
		if e != nil {
			return e
		}
		sort.Strings(ids)
		for _, id := range ids {
			if id <= q.After {
				continue
			}
			if e = ctx.Err(); e != nil {
				return e
			}
			r, e := s.currentFile(a.Space, id)
			if errors.Is(e, prompts.ErrNotFound) {
				continue
			}
			if e != nil {
				return e
			}
			r.Favorite = s.favoriteFile(a, id)
			if matches(a, *r, q) {
				out = append(out, *r)
				if len(out) == q.Limit {
					break
				}
			}
		}
		return nil
	})
	return out, e
}
func (s *Store) Versions(ctx context.Context, a prompts.Actor, id, after string, limit int) ([]prompts.Version, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", prompts.ErrInput
	}
	if _, e := s.Get(ctx, a, id); e != nil {
		return nil, "", e
	}
	if s.db != nil {
		return s.versionsSQL(ctx, a, id, after, limit)
	}
	if after != "" && (!recordPattern.MatchString(after) || !strings.Contains(after, "-v-")) {
		return nil, "", prompts.ErrInput
	}
	out := []prompts.Version{}
	next := ""
	e := s.withLock(a.Space, false, func() error {
		r, e := s.currentFile(a.Space, id)
		if e != nil {
			return e
		}
		if e = Visible(a, *r); e != nil {
			return e
		}
		entries, e := s.entityEntries(a.Space, id)
		if e != nil {
			return e
		}
		for i := len(entries) - 1; i >= 0; i-- {
			name := entries[i]
			if !strings.Contains(name, "-v-") || (after != "" && name >= after) {
				continue
			}
			r, e := s.readRecord(a.Space, id, name)
			if e != nil {
				return e
			}
			out = append(out, r.Result.Version)
			next = name
			if len(out) == limit {
				break
			}
		}
		return nil
	})
	if len(out) < limit {
		next = ""
	}
	return out, next, e
}
func (s *Store) Favorite(ctx context.Context, a prompts.Actor, id string, enabled bool) error {
	if _, e := s.Get(ctx, a, id); e != nil {
		return e
	}
	if s.db != nil {
		return s.favoriteSQL(ctx, a, id, enabled)
	}
	return s.withLock(a.Space, true, func() error {
		r, e := s.currentFile(a.Space, id)
		if e != nil {
			return e
		}
		if e = Visible(a, *r); e != nil {
			return e
		}
		return s.writeFavorite(a, id, enabled)
	})
}
