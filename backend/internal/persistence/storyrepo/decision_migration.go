package storyrepo

import (
	"bytes"
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

// Rejected/intentional decisions have their own immutable file. Accepted ones
// are derived from formal version files, never written to a second file mirror.
func eachDecisionFile(ctx context.Context, dir string, fn func(Scope, string, DecisionRecord) error) error {
	if e := realDir(dir); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return ErrStorage
	}
	e := eachFile(ctx, dir, func(r record) error {
		if r.Adoption == nil {
			return nil
		}
		d := r.Adoption.Decision
		q := Scope{r.Version.Space, r.Version.SeriesID}
		s := Store{dir: dir}
		c, e := s.readCheck(q, d.EpisodeID, d.CheckID)
		if e != nil {
			return e
		}
		f, e := finding(c.Check, d.FindingID)
		if e != nil || story.AdoptionTarget(d.CheckID, f) != r.Version.ID || c.Check.Input.ContextID != d.ContextID {
			return ErrStorage
		}
		return fn(q, d.EpisodeID, *r.Adoption)
	})
	if e != nil {
		return e
	}
	return filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&os.ModeSymlink != 0 {
			return ErrStorage
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return e
		}
		if rel == "." {
			return nil
		}
		parts := strings.Split(rel, string(os.PathSeparator))
		if len(parts) == 1 && !entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".lock-") {
				return nil
			}
			return ErrStorage
		}
		if !story.Hash.MatchString(parts[0]) || len(parts) >= 2 && !story.ID.MatchString(parts[1]) {
			return ErrStorage
		}
		if len(parts) == 3 && parts[2] != ".decisions" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return ErrStorage
		}
		if len(parts) >= 4 && !story.ID.MatchString(parts[3]) || len(parts) >= 5 && !story.ID.MatchString(parts[4]) {
			return ErrStorage
		}
		if entry.IsDir() {
			if len(parts) > 5 {
				return ErrStorage
			}
			return nil
		}
		if len(parts) == 6 && strings.HasPrefix(entry.Name(), ".pending-") {
			return nil
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if len(parts) != 6 || !story.ID.MatchString(id) || entry.Name() != id+".json" || !entry.Type().IsRegular() {
			return ErrStorage
		}
		// Resolve the real space from the related scoped check, rather than trusting
		// a client-supplied filename or introducing a reversible tenant directory.
		file, e := os.OpenFile(filepath.Join(dir, parts[0], parts[1], ".checks", parts[3], parts[4]+".json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return ErrStorage
		}
		check, e := decodeCheck(file)
		closeErr := file.Close()
		if e != nil || closeErr != nil || story.TextHash(check.Check.Space) != parts[0] {
			return ErrStorage
		}
		q := Scope{check.Check.Space, parts[1]}
		s := Store{dir: dir}
		c, e := s.readCheck(q, parts[3], parts[4])
		if e != nil {
			return e
		}
		r, e := s.readDecisionFile(q, parts[3], parts[4], id)
		if e != nil || r.Decision.EpisodeID != parts[3] || r.Decision.ContextID != c.Check.Input.ContextID {
			return ErrStorage
		}
		if _, e = finding(c.Check, id); e != nil {
			return e
		}
		return fn(q, parts[3], r)
	})
}
func CopyDecisionValidation(ctx context.Context, src, dst string) error {
	s, e := Open(dst, nil)
	if e != nil {
		return e
	}
	return eachDecisionFile(ctx, src, func(q Scope, ep string, r DecisionRecord) error {
		if r.Decision.Action == "adopt" {
			return nil
		}
		return s.appendDecisionFile(q, ep, r)
	})
}
func ImportDecisions(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	var count int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_story_decisions`).Scan(&count); e != nil || count != 0 {
		return 0, ErrStorage
	}
	e := eachDecisionFile(ctx, dir, func(q Scope, ep string, r DecisionRecord) error {
		if e := insertDecision(ctx, tx, q, ep, r); e != nil {
			return e
		}
		count++
		return nil
	})
	return count, e
}
func ExportDecisions(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	s, e := Open(dir, nil)
	if e != nil {
		return 0, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT space_id,series_id,episode_id,payload FROM studio_story_decisions ORDER BY space_id,series_id,episode_id,check_id,finding_id`)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var q Scope
		var ep string
		var b []byte
		if rows.Scan(&q.Space, &q.Series, &ep, &b) != nil || !validScope(q) || !story.ID.MatchString(ep) {
			return 0, ErrStorage
		}
		r, e := decodeDecision(bytes.NewReader(b))
		if e != nil || r.Decision.EpisodeID != ep {
			return 0, ErrStorage
		}
		c, e := s.readCheck(q, ep, r.Decision.CheckID)
		if e != nil {
			return 0, e
		}
		f, e := finding(c.Check, r.Decision.FindingID)
		if e != nil {
			return 0, e
		}
		if r.Decision.Action == "adopt" {
			accepted, e := s.fileDecision(q, ep, c.Check, f)
			if e != nil || story.Digest(accepted) != story.Digest(r) {
				return 0, ErrStorage
			}
		} else {
			if e = s.appendDecisionFile(q, ep, r); e != nil {
				return 0, e
			}
		}
		count++
	}
	if e = rows.Err(); e != nil {
		return 0, e
	}
	rows.Close()
	derived := 0
	if e = eachDecisionFile(ctx, dir, func(Scope, string, DecisionRecord) error { derived++; return nil }); e != nil || derived != count {
		return 0, ErrStorage
	}
	return count, nil

}
