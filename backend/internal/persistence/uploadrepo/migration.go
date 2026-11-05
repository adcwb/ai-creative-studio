package uploadrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/uploads"
)

func (s *Store) allFiles(ctx context.Context) ([]Identity, error) {
	spaces, e := os.ReadDir(s.dir)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, ErrStorage
	}
	out := []Identity{}
	for _, space := range spaces {
		if !space.IsDir() || !uploads.SHA.MatchString(space.Name()) {
			return nil, ErrStorage
		}
		items, e := os.ReadDir(filepath.Join(s.dir, space.Name()))
		if e != nil {
			return nil, ErrStorage
		}
		for _, item := range items {
			if e = ctx.Err(); e != nil {
				return nil, e
			}
			if !item.IsDir() || !uploads.ID.MatchString(item.Name()) {
				return nil, ErrStorage
			}
			u, e := creationRecord(filepath.Join(s.dir, space.Name(), item.Name(), "session.json"))
			if errors.Is(e, uploads.ErrNotFound) {
				continue
			}
			if e != nil {
				return nil, e
			}
			if uploads.Digest(u.Space) != space.Name() || u.ID != item.Name() {
				return nil, ErrStorage
			}
			out = append(out, Identity{u.Space, u.Project, u.Actor, u.ID})
		}
	}
	return out, nil
}
func ImportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	s := &Store{dir: dir}
	ids, e := s.allFiles(ctx)
	if e != nil {
		return 0, e
	}
	for _, table := range []string{"studio_upload_sessions", "studio_upload_parts"} {
		var n int
		if tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n) != nil {
			return 0, ErrStorage
		}
		if n != 0 {
			return 0, uploads.ErrConflict
		}
	}
	n := 0
	for _, id := range ids {
		u, e := s.currentFile(id)
		if e != nil {
			return 0, e
		}
		b, _ := json.Marshal(u)
		if _, e = tx.ExecContext(ctx, `INSERT INTO studio_upload_sessions(space_id,project_id,actor_id,id,state,expires_at,payload) VALUES($1,$2,$3,$4,$5,$6,$7)`, u.Space, u.Project, u.Actor, u.ID, u.State, u.ExpiresAt, b); e != nil {
			return 0, ErrStorage
		}
		n++
		if u.State != "receiving" {
			continue
		}
		l := &Locked{Session: u, store: s, ctx: ctx}
		for offset := int64(0); offset < u.Offset; {
			p, b, e := l.readPart(offset)
			if e != nil {
				return 0, e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO studio_upload_parts(space_id,session_id,byte_offset,size,sha256,data) VALUES($1,$2,$3,$4,$5,$6)`, u.Space, u.ID, offset, p.Size, p.SHA256, b); e != nil {
				return 0, ErrStorage
			}
			offset += p.Size
			n++
		}
	}
	return n, nil
}
func ExportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	rows, e := tx.QueryContext(ctx, `SELECT payload FROM studio_upload_sessions ORDER BY space_id,id`)
	if e != nil {
		return 0, ErrStorage
	}
	sessions := []uploads.Session{}
	for rows.Next() {
		var b []byte
		var u uploads.Session
		if rows.Scan(&b) != nil || json.Unmarshal(b, &u) != nil || !uploads.ValidSession(u) {
			rows.Close()
			return 0, ErrStorage
		}
		sessions = append(sessions, u)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	s, e := Open(dir, nil)
	if e != nil {
		return 0, e
	}
	n := 0
	for _, u := range sessions {
		i := Identity{u.Space, u.Project, u.Actor, u.ID}
		path := s.sessionPath(i)
		if _, e = os.Lstat(path); !os.IsNotExist(e) {
			return 0, uploads.ErrConflict
		}
		if e = os.MkdirAll(path, 0700); e != nil {
			return 0, e
		}
		original := u
		original.State = "receiving"
		original.Offset = 0
		original.AssetID = ""
		if e = atomicRecord(path, "session.json", fileHeader{Format: 1, Session: original}, nil); e != nil {
			return 0, e
		}
		n++
		if u.State == "receiving" {
			l := &Locked{Session: original, store: s, ctx: ctx}
			for offset := int64(0); offset < u.Offset; {
				reader := &Locked{Session: u, store: &Store{db: s.db}, tx: tx, ctx: ctx}
				p, b, e := reader.Part(offset)
				if e != nil {
					return 0, e
				}
				if p.Offset != offset || p.Size != min(uploads.ChunkBytes, u.Size-offset) {
					return 0, ErrStorage
				}
				l.Session.Offset += p.Size
				if e = l.writePart(p, b); e != nil {
					return 0, e
				}
				offset += p.Size
				n++
			}
			if l.Session.Offset != u.Offset {
				return 0, ErrStorage
			}
		} else {
			l := &Locked{Session: u, store: s, ctx: ctx}
			if e = l.saveTerminal(); e != nil {
				return 0, e
			}
		}
		if e = os.WriteFile(filepath.Join(path, ".lock"), nil, 0600); e != nil {
			return 0, e
		}
		if e = syncDir(path); e != nil {
			return 0, e
		}
		if e = syncDir(filepath.Dir(path)); e != nil {
			return 0, e
		}
	}
	if e = syncDir(dir); e != nil {
		return 0, e
	}
	return n, nil
}
