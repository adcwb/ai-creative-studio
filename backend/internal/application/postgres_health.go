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
		"SELECT space_id,series_id,episode_id,check_id,finding_id,payload FROM studio_story_decisions WHERE false",
		"SELECT space_id,series_id,episode_id,id,state,payload FROM studio_story_checks WHERE false",
		"SELECT space_id,series_id,episode_id,id,payload FROM studio_story_contexts WHERE false",
		"SELECT space_id,series_id,id,number,version_id,key_hash,request_digest,payload FROM studio_story_versions WHERE false",
		"SELECT space_id,series_id,id,number,version_id,kind,status,title,required,sha256 FROM studio_story_heads WHERE false",
		"SELECT space_id,id,source_project_id,row_version,state,payload FROM studio_episode_migrations WHERE false",
		"SELECT space_id,project_id,export_id,step,payload FROM studio_render_checkpoints WHERE false",
		"SELECT config_hash,key,lease_owner,lease_until,updated_at,payload FROM studio_blob_multipart WHERE false",
		"SELECT space_id,project_id,actor_id,id,state,expires_at,payload FROM studio_upload_sessions WHERE false",
		"SELECT space_id,session_id,byte_offset,size,sha256,data FROM studio_upload_parts WHERE false",
		"SELECT space_id,id,owner_id,visibility,status,row_version,head_version_id,payload FROM studio_prompt_templates WHERE false",
		"SELECT space_id,template_id,id,number,payload FROM studio_prompt_versions WHERE false",
		"SELECT space_id,template_id,key_hash,digest,payload FROM studio_prompt_receipts WHERE false",
		"SELECT space_id,actor_id,template_id,enabled FROM studio_prompt_favorites WHERE false",
		"SELECT space_id,id,kind,status,head_version_id,payload FROM studio_library_assets WHERE false",
		"SELECT space_id,kind,id,parent_id,series_id,ordinal,row_version,status,state,project_id,payload FROM studio_series_nodes WHERE false",
		"SELECT space_id,key_hash,digest,kind,id FROM studio_series_receipts WHERE false",
		"SELECT space_id,asset_id,id,number,payload FROM studio_library_asset_versions WHERE false",
		"SELECT space_id,receipt_key,digest,asset_id,payload FROM studio_library_receipts WHERE false",
		"SELECT name,revision,envelope FROM studio_scopes WHERE false",
		"SELECT tenant_id,id,kind,idempotency_key,payload,state,lease_token,lease_until,attempt,result,created_at,updated_at,trace_parent FROM execution_jobs WHERE false",
		"SELECT address_hash,attempts,expires FROM studio_auth_attempts WHERE false",
		"SELECT space_id,kind,owner_id,version FROM studio_space_access WHERE false",
		"SELECT space_id,user_id,role_id,space_kind,space_owner_id FROM studio_space_memberships WHERE false",
		"SELECT role_id,permission_id FROM studio_rbac_role_permissions WHERE false",
		"SELECT space_id,space_kind,id,name,base_role,version FROM studio_space_roles WHERE false",
		"SELECT space_id,role_id,permission_id FROM studio_space_role_permissions WHERE false",
		"SELECT space_id,user_id,role_id,base_role,membership_role FROM studio_space_member_roles WHERE false",
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
	return "SELECT (SELECT count(*)=1 AND min(version)=1 AND max(version)=1 FROM studio_schema) AND EXISTS(SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_blobs_size_nonnegative') AND EXISTS(SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_library_kind_v3' AND conrelid='studio_library_assets'::regclass) AND EXISTS(SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_space_role_team' AND conrelid='studio_space_roles'::regclass) AND EXISTS(SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_series_episode_project') AND " + strings.Join(checks, " AND ")
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
			if s.runtime.auth.models.usage != nil {
				add(s.runtime.auth.models.usage.pg)
			}
		}
	}
	return n
}
