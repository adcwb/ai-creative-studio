package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func postgresAllowAttempt(db *sql.DB, address string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	defer tx.Rollback()
	// A shared bounded table, independent of process restarts. Store an address
	// digest rather than client IPs; the configured trusted-proxy boundary applies.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(728219115)`); err != nil {
		return false
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM studio_auth_attempts WHERE expires<clock_timestamp()`); err != nil {
		return false
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_auth_attempts`).Scan(&count); err != nil || count >= 1024 {
		return false
	}
	var n int
	err = tx.QueryRowContext(ctx, `INSERT INTO studio_auth_attempts(address_hash,attempts,expires) VALUES($1,1,clock_timestamp()+interval '15 minutes') ON CONFLICT(address_hash) DO UPDATE SET attempts=studio_auth_attempts.attempts+1 RETURNING attempts`, authDigest(address)).Scan(&n)
	if err != nil || tx.Commit() != nil {
		return false
	}
	return n <= 20
}
func postgresPutOIDC(db *sql.DB, state string, p oidcPending) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(728219116)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM studio_oidc_pending WHERE expires<clock_timestamp()`); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_oidc_pending`).Scan(&count); err != nil {
		return err
	}
	if count >= 1000 {
		return ErrConflict
	}
	raw, _ := json.Marshal(p)
	_, err = tx.ExecContext(ctx, `INSERT INTO studio_oidc_pending(state_hash,binding_hash,payload,expires) VALUES($1,$2,$3,$4)`, authDigest(state), authDigest(p.Binding), raw, p.Expires)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func postgresTakeOIDC(db *sql.DB, state, binding string) (oidcPending, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var raw []byte
	err := db.QueryRowContext(ctx, `DELETE FROM studio_oidc_pending WHERE state_hash=$1 AND binding_hash=$2 AND expires>clock_timestamp() RETURNING payload`, authDigest(state), authDigest(binding)).Scan(&raw)
	var p oidcPending
	if err != nil || json.Unmarshal(raw, &p) != nil {
		return p, false
	}
	return p, true
}
