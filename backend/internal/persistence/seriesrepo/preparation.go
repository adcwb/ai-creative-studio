package seriesrepo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

// GetPreparation is internal recovery access. Public reads still hide every
// preparing/cancelled episode; callers must verify its owning migration job.
func (s *Store) GetPreparation(ctx context.Context, space, id string) (series.Node, error) {
	if !series.ScopeID.MatchString(space) || !series.ID.MatchString(id) {
		return series.Node{}, series.ErrNotFound
	}
	if s.db != nil {
		return scanNode(s.db.QueryRowContext(ctx, `SELECT payload FROM studio_series_nodes WHERE space_id=$1 AND kind='episode' AND id=$2`, space, id))
	}
	var n series.Node
	e := s.withFile(ctx, space, func(f *fileAccess) error { var e error; n, e = f.get("episode", id); return e })
	return n, e
}

type Preparation struct {
	MigrationID, ID, SeasonID string
	Ordinal, SeasonVersion    int64
	Input                     series.Input
}

func reserve(a series.Actor, p Preparation, r reader) (mutation, error) {
	if !validActor(a) || !a.Writable {
		return mutation{}, series.ErrDenied
	}
	if !series.ID.MatchString(p.MigrationID) || !series.ID.MatchString(p.ID) || !series.ID.MatchString(p.SeasonID) || p.Ordinal < 1 || p.Ordinal >= series.MaxOrdinal {
		return mutation{}, series.ErrInput
	}
	old, e := r.get("episode", p.ID)
	if e == nil {
		if old.MigrationID != p.MigrationID || old.Parent != p.SeasonID || old.Ordinal != p.Ordinal || old.State == "cancelled" {
			return mutation{}, series.ErrConflict
		}
		return mutation{Result: old}, nil
	}
	if !errors.Is(e, series.ErrNotFound) {
		return mutation{}, e
	}
	parent, e := r.get("season", p.SeasonID)
	if e != nil {
		return mutation{}, e
	}
	root, e := r.get("series", parent.SeriesID)
	if e != nil {
		return mutation{}, e
	}
	if parent.Version != p.SeasonVersion || parent.Status != "active" || root.Status != "active" {
		return mutation{}, series.ErrConflict
	}
	nodes, e := r.children("episode", p.SeasonID)
	if e != nil {
		return mutation{}, e
	}
	for _, n := range nodes {
		if n.State != "cancelled" && n.Ordinal == p.Ordinal {
			return mutation{}, series.ErrConflict
		}
	}
	in, e := series.Normalize(p.Input, "episode")
	if e != nil {
		return mutation{}, e
	}
	now := time.Now().UTC()
	n := series.Node{MigrationID: p.MigrationID, ID: p.ID, Space: a.Space, Kind: "episode", Parent: parent.ID, SeriesID: parent.SeriesID, Ordinal: p.Ordinal, NextOrdinal: 1, Version: 1, Title: in.Title, Summary: in.Summary, Profile: in.Profile, Status: "planned", State: "preparing", ProjectID: p.ID, CreatedBy: a.User, CreatedAt: now, UpdatedAt: now}
	parent.Version++
	if parent.NextOrdinal <= p.Ordinal {
		parent.NextOrdinal = p.Ordinal + 1
	}
	parent.UpdatedAt = now
	if !series.Valid(n) || !series.Valid(parent) {
		return mutation{}, series.ErrInput
	}
	return mutation{Rows: []series.Node{parent, n}, Result: n}, nil
}
func preparationLock(ctx context.Context, tx *sql.Tx, space string) error {
	if _, e := tx.ExecContext(ctx, `SET LOCAL synchronous_commit=on;SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
		return ErrStorage
	}
	if _, e := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,728219128))`, space); e != nil {
		return ErrStorage
	}
	return nil
}
func (s *Store) ReserveMigrationTx(ctx context.Context, tx *sql.Tx, a series.Actor, p Preparation) (series.Node, error) {
	if e := preparationLock(ctx, tx, a.Space); e != nil {
		return series.Node{}, e
	}
	m, e := reserve(a, p, sqlAccess{ctx, a.Space, tx})
	if e != nil {
		return series.Node{}, e
	}
	for _, n := range m.Rows {
		if e = saveSQL(ctx, tx, n); e != nil {
			return series.Node{}, e
		}
	}
	return m.Result, nil
}
func (s *Store) ReserveMigration(ctx context.Context, a series.Actor, p Preparation, project func(series.Node) error) (series.Node, error) {
	if s.db != nil || project == nil {
		return series.Node{}, ErrStorage
	}
	var n series.Node
	e := s.withFile(ctx, a.Space, func(f *fileAccess) error {
		m, e := reserve(a, p, f)
		if e != nil {
			return e
		}
		if len(m.Rows) > 0 {
			id, e := randomTx()
			if e != nil {
				return e
			}
			if e = f.commit(id, m.Rows, nil); e != nil {
				return e
			}
		}
		n = m.Result
		if n.State == "preparing" {
			return project(n)
		}
		return nil
	})
	return n, e
}
func changePreparation(a series.Actor, id, migration, state string, r reader) (mutation, error) {
	if !validActor(a) || !a.Writable {
		return mutation{}, series.ErrDenied
	}
	n, e := r.get("episode", id)
	if e != nil {
		return mutation{}, e
	}
	if n.MigrationID != migration || migration == "" {
		return mutation{}, series.ErrConflict
	}
	if n.State == state {
		return mutation{Result: n}, nil
	}
	if n.State != "preparing" {
		return mutation{}, series.ErrConflict
	}
	parent, e := r.get("season", n.Parent)
	if e != nil {
		return mutation{}, e
	}
	if state == "ready" {
		root, e := r.get("series", n.SeriesID)
		if e != nil {
			return mutation{}, e
		}
		if parent.Status != "active" || root.Status != "active" {
			return mutation{}, series.ErrConflict
		}
	}
	n.State = state
	n.Version++
	n.UpdatedAt = time.Now().UTC()
	parent.Version++
	parent.UpdatedAt = n.UpdatedAt
	return mutation{Rows: []series.Node{parent, n}, Result: n}, nil
}
func (s *Store) FinishMigrationTx(ctx context.Context, tx *sql.Tx, a series.Actor, id, migration string, cancel bool) error {
	if e := preparationLock(ctx, tx, a.Space); e != nil {
		return e
	}
	state := "ready"
	if cancel {
		state = "cancelled"
	}
	m, e := changePreparation(a, id, migration, state, sqlAccess{ctx, a.Space, tx})
	if errors.Is(e, series.ErrNotFound) && cancel {
		return nil
	}
	if e != nil {
		return e
	}
	for _, n := range m.Rows {
		if n.State == "cancelled" {
			if _, e = tx.ExecContext(ctx, `DELETE FROM studio_series_nodes WHERE space_id=$1 AND kind='episode' AND id=$2 AND state='preparing'`, a.Space, n.ID); e != nil {
				return ErrStorage
			}
		} else if e = saveSQL(ctx, tx, n); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) FinishMigration(ctx context.Context, a series.Actor, id, migration string, cancel bool) error {
	if s.db != nil {
		return ErrStorage
	}
	return s.withFile(ctx, a.Space, func(f *fileAccess) error {
		state := "ready"
		if cancel {
			state = "cancelled"
		}
		m, e := changePreparation(a, id, migration, state, f)
		if errors.Is(e, series.ErrNotFound) && cancel {
			return nil
		}
		if e != nil {
			return e
		}
		if len(m.Rows) == 0 {
			return nil
		}
		txid, e := randomTx()
		if e != nil {
			return e
		}
		return f.commit(txid, m.Rows, nil)
	})
}

func (s *Store) OrdinalAvailable(ctx context.Context, space, parent string, ordinal int64) (bool, error) {
	if !series.ScopeID.MatchString(space) || !series.ID.MatchString(parent) || ordinal < 1 || ordinal >= series.MaxOrdinal {
		return false, series.ErrInput
	}
	if s.db != nil {
		var occupied bool
		e := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM studio_series_nodes WHERE space_id=$1 AND kind='episode' AND parent_id=$2 AND ordinal=$3)`, space, parent, ordinal).Scan(&occupied)
		return !occupied, e
	}
	available := true
	e := s.withFile(ctx, space, func(f *fileAccess) error {
		nodes, e := f.children("episode", parent)
		if e != nil {
			return e
		}
		for _, n := range nodes {
			if n.State != "cancelled" && n.Ordinal == ordinal {
				available = false
			}
		}
		return nil
	})
	return available, e
}
