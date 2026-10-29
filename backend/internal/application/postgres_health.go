package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func CheckPostgres(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("postgres unavailable")
	}
	// Resolve every required column without reading business data. The v1
	// marker alone cannot distinguish an older deployment missing additive DDL.
	var valid bool
	if err := db.QueryRowContext(ctx, postgresReadinessSQL).Scan(&valid); err != nil || !valid {
		return errors.New("postgres schema unavailable")
	}
	return nil
}

// Identifiers come only from this package's fixed schema definitions.
var postgresReadinessSQL = func() string {
	queries := []string{
		"SELECT space_id,id,kind,status,head_version_id,payload FROM studio_library_assets WHERE false",
		"SELECT space_id,asset_id,id,number,payload FROM studio_library_asset_versions WHERE false",
		"SELECT space_id,receipt_key,digest,asset_id,payload FROM studio_library_receipts WHERE false",
		"SELECT name,revision,envelope FROM studio_scopes WHERE false",
		"SELECT tenant_id,id,kind,idempotency_key,payload,state,lease_token,lease_until,attempt,result,created_at,updated_at,trace_parent FROM execution_jobs WHERE false",
		"SELECT address_hash,attempts,expires FROM studio_auth_attempts WHERE false",
		"SELECT state_hash,binding_hash,payload,expires FROM studio_oidc_pending WHERE false",
		"SELECT key,tenant_hash,project_hash,size,sha256 FROM studio_blobs WHERE false",
		"SELECT request_id,phase,at,actor_id,tenant_id,role,method,status,duration_ms FROM studio_audit_events WHERE false",
	}
	for _, collections := range pgCollections {
		for _, c := range collections {
			queries = append(queries, "SELECT record_key,tenant_id,entity_id,version,state,parent_id,currency,amount,reserved,actual,payload FROM "+c.table+" WHERE false")
		}
	}
	var checks []string
	for _, q := range queries {
		checks = append(checks, "NOT EXISTS ("+q+")")
	}
	return "SELECT (SELECT count(*)=1 AND min(version)=1 AND max(version)=1 FROM studio_schema) AND " + strings.Join(checks, " AND ")
}()

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
