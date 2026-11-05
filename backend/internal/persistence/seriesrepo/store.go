package seriesrepo

import (
	"context"
	"database/sql"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"sort"
	"strings"
	"time"
)

var ErrStorage = errors.New("series storage unavailable")
var ErrKey = errors.New("series idempotency conflict")

type Store struct {
	dir string
	db  *sql.DB
}
type Query struct {
	Kind, Parent, After, Status string
	Limit                       int
}
type Page struct {
	Items []series.Node `json:"items"`
	Next  string        `json:"next_cursor"`
}
type receipt struct{ Actor, KeyHash, Digest, ID, Kind string }
type reader interface {
	get(string, string) (series.Node, error)
	children(string, string) ([]series.Node, error)
}
type mutation struct {
	Rows   []series.Node
	Result series.Node
}

func Open(dir string, db *sql.DB) (*Store, error) {
	s := &Store{dir, db}
	if db == nil {
		if e := s.initFile(); e != nil {
			return nil, e
		}
	}
	return s, nil
}
func validActor(a series.Actor) bool {
	return series.ScopeID.MatchString(a.Space) && series.ScopeID.MatchString(a.User)
}
func keyHash(a series.Actor, c series.Command, key string) string {
	return series.SHA(a.User + "\x00" + c.Operation + "\x00" + key)
}
func apply(a series.Actor, c series.Command, r reader, preparing bool) (mutation, error) {
	if !validActor(a) || !a.Writable {
		return mutation{}, series.ErrDenied
	}
	if !series.ID.MatchString(c.ID) {
		return mutation{}, series.ErrInput
	}
	now := time.Now().UTC()
	if c.Operation == "create" {
		if _, e := r.get(c.Kind, c.ID); e == nil {
			return mutation{}, series.ErrConflict
		} else if !errors.Is(e, series.ErrNotFound) {
			return mutation{}, e
		}
		in := c.Input
		var parent series.Node
		if c.Kind != "series" {
			parentKind := "series"
			if c.Kind == "episode" {
				parentKind = "season"
			} else if c.Kind != "season" {
				return mutation{}, series.ErrInput
			}
			var err error
			parent, err = r.get(parentKind, c.Parent)
			if err != nil {
				return mutation{}, err
			}
			if parent.Status != "active" {
				return mutation{}, series.ErrConflict
			}
			root := parent
			if c.Kind == "episode" {
				root, err = r.get("series", parent.SeriesID)
				if err != nil {
					return mutation{}, err
				}
				if root.Status != "active" {
					return mutation{}, series.ErrConflict
				}
			}
			if in.Profile == (series.Profile{}) {
				in.Profile = parent.Profile
			}
			if c.Kind == "episode" && strings.TrimSpace(in.Summary) == "" {
				in.Summary = parent.Summary
				if strings.TrimSpace(in.Summary) == "" {
					in.Summary = root.Summary
				}
			}
		} else if in.Profile == (series.Profile{}) {
			in.Profile = series.DefaultProfile()
		}
		in, e := series.Normalize(in, c.Kind)
		if e != nil {
			return mutation{}, e
		}
		n := series.Node{ID: c.ID, Space: a.Space, Kind: c.Kind, Title: in.Title, Summary: in.Summary, Audience: in.Audience, Format: in.Format, Profile: in.Profile, Version: 1, NextOrdinal: 1, Status: "active", State: "ready", CreatedBy: a.User, CreatedAt: now, UpdatedAt: now}
		rows := []series.Node{}
		if c.Kind == "series" {
			n.SeriesID = n.ID
		} else {
			n.Parent, n.SeriesID, n.Ordinal = parent.ID, parent.SeriesID, parent.NextOrdinal
			if n.Ordinal >= series.MaxOrdinal {
				return mutation{}, series.ErrInput
			}
			parent.NextOrdinal++
			parent.Version++
			parent.UpdatedAt = now
			rows = append(rows, parent)
			if c.Kind == "episode" {
				n.ProjectID = n.ID
				n.Status = "planned"
				if preparing {
					n.State = "preparing"
				}
			}
		}
		if !series.Valid(n) {
			return mutation{}, series.ErrInput
		}
		return mutation{append(rows, n), n}, nil
	}
	old, e := r.get(c.Kind, c.ID)
	if e != nil {
		return mutation{}, e
	}
	if old.State != "ready" {
		return mutation{}, series.ErrConflict
	}
	if c.Expected != old.Version {
		return mutation{}, series.ErrConflict
	}
	switch c.Operation {
	case "update":
		input := c.Input
		if c.Kind == "episode" {
			if input.Title != "" && input.Title != old.Title || input.Summary != "" && input.Summary != old.Summary {
				return mutation{}, series.ErrInput
			}
			input.Title, input.Summary = old.Title, old.Summary
		}
		in, e := series.Normalize(input, c.Kind)
		if e != nil {
			return mutation{}, e
		}
		if c.Kind == "episode" { // Project is the canonical title/brief editor.
			old.Profile = in.Profile
		} else {
			old.Title, old.Summary, old.Audience, old.Format, old.Profile = in.Title, in.Summary, in.Audience, in.Format, in.Profile
		}
		old.Status = c.Status
	case "move":
		if c.Kind == "series" || c.Ordinal < 1 || c.Ordinal >= series.MaxOrdinal {
			return mutation{}, series.ErrInput
		}
		parentKind := "series"
		if c.Kind == "episode" {
			parentKind = "season"
		}
		parent, e := r.get(parentKind, old.Parent)
		if e != nil {
			return mutation{}, e
		}
		if c.ExpectedParent != parent.Version {
			return mutation{}, series.ErrConflict
		}
		siblings, e := r.children(c.Kind, old.Parent)
		if e != nil {
			return mutation{}, e
		}
		if parent.NextOrdinal <= c.Ordinal {
			parent.NextOrdinal = c.Ordinal + 1
		}
		parent.Version++
		parent.UpdatedAt = now
		rows := []series.Node{parent}
		for _, n := range siblings {
			if n.State != "ready" {
				return mutation{}, series.ErrConflict
			}
			if n.ID != old.ID && n.Ordinal == c.Ordinal {
				n.Ordinal = old.Ordinal
				n.Version++
				n.UpdatedAt = now
				rows = append(rows, n)
			}
		}
		old.Ordinal = c.Ordinal
		old.Version++
		old.UpdatedAt = now
		rows = append(rows, old)
		return mutation{rows, old}, nil
	default:
		return mutation{}, series.ErrInput
	}
	old.Version++
	old.UpdatedAt = now
	if !series.Valid(old) {
		return mutation{}, series.ErrInput
	}
	return mutation{[]series.Node{old}, old}, nil
}
func (s *Store) Get(ctx context.Context, space, kind, id string) (series.Node, error) {
	if !series.ScopeID.MatchString(space) || !series.ID.MatchString(id) {
		return series.Node{}, series.ErrNotFound
	}
	if s.db != nil {
		return scanNode(s.db.QueryRowContext(ctx, `SELECT payload FROM studio_series_nodes WHERE space_id=$1 AND kind=$2 AND id=$3 AND state='ready'`, space, kind, id))
	}
	var n series.Node
	e := s.withFile(ctx, space, func(f *fileAccess) error {
		var e error
		n, e = f.get(kind, id)
		if e == nil && n.State != "ready" {
			e = series.ErrNotFound
		}
		return e
	})
	return n, e
}
func (s *Store) ByProject(ctx context.Context, space, id string) (series.Node, error) {
	if !series.ScopeID.MatchString(space) || !series.ID.MatchString(id) {
		return series.Node{}, series.ErrNotFound
	}
	if s.db != nil {
		return scanNode(s.db.QueryRowContext(ctx, `SELECT payload FROM studio_series_nodes WHERE space_id=$1 AND kind='episode' AND project_id=$2 AND state='ready'`, space, id))
	}
	return s.Get(ctx, space, "episode", id)
}
func (s *Store) List(ctx context.Context, space string, q Query) (Page, error) {
	if !series.ScopeID.MatchString(space) || q.Limit < 1 || q.Limit > 100 || (q.Kind != "series" && q.Kind != "season" && q.Kind != "episode") {
		return Page{}, series.ErrInput
	}
	if q.Parent != "" && !series.ID.MatchString(q.Parent) || (q.Kind == "series" && q.Parent != "") {
		return Page{}, series.ErrInput
	}
	if _, _, err := parseCursor(q.After); err != nil {
		return Page{}, err
	}
	if s.db != nil {
		return s.listSQL(ctx, space, q)
	}
	out := Page{Items: []series.Node{}}
	e := s.withFile(ctx, space, func(f *fileAccess) error {
		nodes, e := f.children(q.Kind, q.Parent)
		if e != nil {
			return e
		}
		sortNodes(nodes)
		for _, n := range nodes {
			if n.State != "ready" || q.Status != "" && n.Status != q.Status {
				continue
			}
			if q.After != "" && cursor(n) <= q.After {
				continue
			}
			out.Items = append(out.Items, n)
			if len(out.Items) > q.Limit {
				out.Next = cursor(out.Items[q.Limit-1])
				out.Items = out.Items[:q.Limit]
				break
			}
		}
		return nil
	})
	return out, e
}
func sortNodes(n []series.Node) {
	sort.Slice(n, func(i, j int) bool {
		if n[i].Ordinal == n[j].Ordinal {
			return n[i].ID < n[j].ID
		}
		return n[i].Ordinal < n[j].Ordinal
	})
}
