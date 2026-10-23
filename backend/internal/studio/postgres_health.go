package studio

import (
	"context"
	"database/sql"
	"errors"
)

func CheckPostgres(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("postgres unavailable")
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM studio_schema WHERE version=1`).Scan(&n); err != nil || n != 1 {
		return errors.New("postgres schema unavailable")
	}
	return nil
}
func (s *FileStore) PersistenceFailureEpoch() uint64 {
	var n uint64
	add := func(p *postgresScope) {
		if p != nil {
			n += p.failures.Load()
		}
	}
	add(s.pg)
	if s.budget != nil {
		add(s.budget.pg)
	}
	if s.creative != nil {
		add(s.creative.pg)
	}
	if s.media != nil {
		add(s.media.pg)
		if s.media.alignment != nil {
			add(s.media.alignment.pg)
		}
	}
	if s.runtime != nil && s.runtime.auth != nil {
		add(s.runtime.auth.pg)
		if s.runtime.auth.models != nil {
			add(s.runtime.auth.models.pg)
		}
	}
	return n
}
