package libraryrepo

import (
	"context"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
)

type authorizationMutation struct {
	Input library.AuthorizationInput
	Actor string
	Now   time.Time
}

// Updates current authority and its original receipt in the same per-space
// transaction. No content version or old receipt is rewritten.
func (s *Store) SetAuthorization(ctx context.Context, space, asset, key, digest, actor string, in library.AuthorizationInput, now time.Time) (Result, error) {
	if !library.IDPattern.MatchString(space) || !library.IDPattern.MatchString(asset) || key == "" || len(key) > 1024 {
		return Result{}, library.ErrInput
	}
	normalized, e := library.NormalizeAuthorization(in)
	if e != nil {
		return Result{}, e
	}
	mutation := &authorizationMutation{normalized, actor, now}
	target := Result{Asset: library.Asset{ID: asset, SpaceID: space}}
	if s.db != nil {
		return s.mutateSQL(ctx, space, key, digest, "", target, false, mutation)
	}
	var out Result
	e = s.withFileLock(ctx, space, func() error {
		var e error
		out, e = s.fileMutate(ctx, space, key, digest, "", target, false, mutation)
		return e
	})
	return out, e
}
func (s *Store) authorizationResult(ctx context.Context, old Result, m authorizationMutation, version func(string) error) (Result, error) {
	next, e := library.ApplyAuthorization(old.Asset, m.Input, m.Actor, m.Now)
	if e != nil {
		return Result{}, e
	}
	if next.Status == "granted" {
		for _, id := range next.VersionIDs {
			if e = ctx.Err(); e != nil {
				return Result{}, e
			}
			if e = version(id); e != nil {
				return Result{}, e
			}
		}
	}
	old.Asset.UseAuthorization = &next
	old.Asset.UpdatedAt = m.Now.UTC()
	return old, nil
}
