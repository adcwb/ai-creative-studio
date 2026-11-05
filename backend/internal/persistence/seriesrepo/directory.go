package seriesrepo

import (
	"context"
	"database/sql"
	"sort"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

type DirectorySnapshot struct {
	Root     series.Node
	Seasons  []series.Node
	Episodes []series.Node
}

// Directory captures only directory metadata, never every episode's script or
// media. Narrative intervals use stable episode IDs resolved in this snapshot.
func (s *Store) Directory(ctx context.Context, space, id string) (DirectorySnapshot, error) {
	out := DirectorySnapshot{Seasons: []series.Node{}, Episodes: []series.Node{}}
	if !series.ScopeID.MatchString(space) || !series.ID.MatchString(id) {
		return out, series.ErrNotFound
	}
	read := func(r reader) error {
		var e error
		out.Root, e = r.get("series", id)
		if e != nil {
			return e
		}
		out.Seasons, e = r.children("season", id)
		if e != nil {
			return e
		}
		for _, season := range out.Seasons {
			episodes, e := r.children("episode", season.ID)
			if e != nil {
				return e
			}
			for _, episode := range episodes {
				if episode.State == "ready" {
					out.Episodes = append(out.Episodes, episode)
				}
			}
		}
		sort.Slice(out.Seasons, func(i, j int) bool { return cursor(out.Seasons[i]) < cursor(out.Seasons[j]) })
		ranks := map[string]int64{}
		for _, n := range out.Seasons {
			ranks[n.ID] = n.Ordinal
		}
		sort.Slice(out.Episodes, func(i, j int) bool {
			a, b := out.Episodes[i], out.Episodes[j]
			if ranks[a.Parent] != ranks[b.Parent] {
				return ranks[a.Parent] < ranks[b.Parent]
			}
			return cursor(a) < cursor(b)
		})
		return nil
	}
	if s.db == nil {
		e := s.withFile(ctx, space, func(f *fileAccess) error { return read(f) })
		return out, e
	}
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return out, ErrStorage
	}
	defer tx.Rollback()
	if e = read(sqlAccess{ctx, space, tx}); e != nil {
		return out, e
	}
	if tx.Commit() != nil {
		return out, ErrStorage
	}
	return out, nil
}
