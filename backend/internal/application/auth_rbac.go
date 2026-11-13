package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/identity"
)

const rbacSchema = `
CREATE TABLE IF NOT EXISTS studio_rbac_roles(id text PRIMARY KEY);
CREATE TABLE IF NOT EXISTS studio_rbac_permissions(id text PRIMARY KEY);
CREATE TABLE IF NOT EXISTS studio_rbac_role_permissions(
 role_id text NOT NULL REFERENCES studio_rbac_roles(id),
 permission_id text NOT NULL REFERENCES studio_rbac_permissions(id),
 PRIMARY KEY(role_id,permission_id));
CREATE TABLE IF NOT EXISTS studio_space_access(
 space_id text PRIMARY KEY REFERENCES studio_organizations(entity_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
 kind text NOT NULL CHECK(kind IN ('personal','team')),
 owner_id text NOT NULL REFERENCES studio_users(entity_id) DEFERRABLE INITIALLY DEFERRED,
 version bigint NOT NULL CHECK(version>0), UNIQUE(space_id,kind,owner_id));
ALTER TABLE studio_space_access ADD COLUMN IF NOT EXISTS closed boolean NOT NULL DEFAULT false;
DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_personal_space_open') THEN ALTER TABLE studio_space_access ADD CONSTRAINT studio_personal_space_open CHECK(kind<>'personal' OR NOT closed); END IF; END $$;
CREATE UNIQUE INDEX IF NOT EXISTS studio_one_personal_space ON studio_space_access(owner_id) WHERE kind='personal';
CREATE TABLE IF NOT EXISTS studio_space_memberships(
 space_id text NOT NULL, user_id text NOT NULL REFERENCES studio_users(entity_id) DEFERRABLE INITIALLY DEFERRED,
 role_id text NOT NULL REFERENCES studio_rbac_roles(id),
 space_kind text NOT NULL, space_owner_id text NOT NULL,
 PRIMARY KEY(space_id,user_id),
 FOREIGN KEY(space_id,space_kind,space_owner_id) REFERENCES studio_space_access(space_id,kind,owner_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
 CHECK((role_id='owner')=(user_id=space_owner_id)),
 CHECK(space_kind<>'personal' OR (user_id=space_owner_id AND role_id='owner')));
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_space_owner_membership') THEN
  ALTER TABLE studio_space_access ADD CONSTRAINT studio_space_owner_membership FOREIGN KEY(space_id,owner_id) REFERENCES studio_space_memberships(space_id,user_id) DEFERRABLE INITIALLY DEFERRED;
 END IF;
END $$;`

func initRBAC(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, rbacSchema+customRBACSchema); err != nil {
		return err
	}
	for _, role := range identity.Roles() {
		result, err := tx.ExecContext(ctx, `INSERT INTO studio_rbac_roles(id) VALUES($1) ON CONFLICT DO NOTHING`, role)
		if err != nil {
			return err
		}
		created, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if created == 0 {
			continue
		} // Reinitialization never restores revoked grants.
		for _, permission := range identity.Permissions(role) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO studio_rbac_permissions(id) VALUES($1) ON CONFLICT DO NOTHING`, permission); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO studio_rbac_role_permissions(role_id,permission_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, role, permission); err != nil {
				return err
			}
		}
	}
	// Old readers reject this unknown role instead of granting the legacy
	// editor/admin ceiling while ignoring scoped permissions.
	if _, err := tx.ExecContext(ctx, `INSERT INTO studio_rbac_roles(id)VALUES('custom') ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	// Only explicit type metadata identifies a personal space. Legacy organizations
	// remain teams; their names and current member counts are not evidence.
	_, err := tx.ExecContext(ctx, `WITH added AS (
 INSERT INTO studio_space_access(space_id,kind,owner_id,version,closed)
 SELECT entity_id,COALESCE(NULLIF(payload->>'type',''),'team'),payload->>'owner_id',GREATEST(1,COALESCE((payload->>'version')::bigint,1)),COALESCE((payload->>'closed')::boolean,false) FROM studio_organizations
 ON CONFLICT(space_id) DO NOTHING RETURNING space_id,kind,owner_id)
 INSERT INTO studio_space_memberships(space_id,user_id,role_id,space_kind,space_owner_id)
 SELECT a.space_id,m.key,m.value,a.kind,a.owner_id FROM added a JOIN studio_organizations o ON o.entity_id=a.space_id CROSS JOIN LATERAL jsonb_each_text(o.payload->'members') m`)

	return err
}

func spaceType(o AuthOrganization) string {
	if o.Type == "personal" {
		return "personal"
	}
	return "team"
}
func spaceVersion(o AuthOrganization) int64 {
	if o.Version > 0 {
		return o.Version
	}
	return 1
}

func ensurePersonalSpace(d *authSnapshot, user string) {
	for _, o := range d.Organizations {
		if o.Type == "personal" && o.OwnerID == user {
			return
		}
	}
	id := "org-" + authRandom(16)
	d.Organizations[id] = AuthOrganization{ID: id, Name: "个人空间", OwnerID: user, Type: "personal", Version: 1, Members: map[string]string{user: "owner"}}
}

// Hydrate authorization from constrained relations; the JSON payload remains a
// same-transaction compatibility/export representation, not a second authority.
func (p *postgresScope) loadRBAC(d map[string]json.RawMessage) error {
	var organizations map[string]AuthOrganization
	if err := json.Unmarshal(d["organizations"], &organizations); err != nil {
		return err
	}
	for id, o := range organizations {
		o.Members = map[string]string{}
		organizations[id] = o
	}
	rows, err := p.tx.QueryContext(p.ctx, `SELECT a.space_id,a.kind,a.owner_id,a.version,a.closed,m.user_id,m.role_id FROM studio_space_access a JOIN studio_space_memberships m ON m.space_id=a.space_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, owner, user, role string
		var version int64
		var closed bool
		if err = rows.Scan(&id, &kind, &owner, &version, &closed, &user, &role); err != nil {
			return err
		}
		o, ok := organizations[id]
		if !ok || (!validRole(role) && role != "custom") {
			return errors.New("invalid RBAC identity")
		}
		o.Type, o.OwnerID, o.Version, o.Closed = kind, owner, version, closed
		o.Members[user] = role
		organizations[id] = o
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = p.loadCustomRBAC(organizations); err != nil {
		return err
	}
	for _, o := range organizations {
		if o.Members[o.OwnerID] != "owner" {
			return errors.New("missing RBAC owner")
		}
		if err = identity.ValidateCustomRoles(o); err != nil {
			return err
		}
		for _, role := range o.Members {
			if !validRole(role) {
				return errors.New("custom membership without policy")
			}
		}
	}
	d["organizations"], err = json.Marshal(organizations)
	return err
}

func (p *postgresScope) writeRBAC(records map[string]map[string]json.RawMessage) error {
	for id, raw := range records["organizations"] {
		if sameJSON(p.before["organizations"][id], raw) {
			continue
		}
		var o AuthOrganization
		if err := json.Unmarshal(raw, &o); err != nil {
			return err
		}
		if _, err := p.tx.ExecContext(p.ctx, `INSERT INTO studio_space_access(space_id,kind,owner_id,version,closed) VALUES($1,$2,$3,$4,$5) ON CONFLICT(space_id) DO UPDATE SET kind=EXCLUDED.kind,owner_id=EXCLUDED.owner_id,version=EXCLUDED.version,closed=EXCLUDED.closed`, o.ID, spaceType(o), o.OwnerID, spaceVersion(o), o.Closed); err != nil {
			return err
		}
		if _, err := p.tx.ExecContext(p.ctx, `DELETE FROM studio_space_memberships WHERE space_id=$1`, o.ID); err != nil {
			return err
		}
		if err := p.writeCustomRBAC(o); err != nil {
			return err
		}
		for user, role := range o.Members {
			if o.MemberRoles[user] != "" {
				role = "custom"
			}
			if _, err := p.tx.ExecContext(p.ctx, `INSERT INTO studio_space_memberships(space_id,user_id,role_id,space_kind,space_owner_id) VALUES($1,$2,$3,$4,$5)`, o.ID, user, role, spaceType(o), o.OwnerID); err != nil {
				return err
			}
		}
		if err := p.writeCustomAssignments(o); err != nil {
			return err
		}
	}
	return nil
}

func (s *AuthStore) can(user, space, permission string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil || s.pg != nil && s.pg.err != nil {
		return false
	}
	return s.canLocked(user, space, permission)
}

// Caller holds the auth transaction lock, including permission and mutation.
func (s *AuthStore) canLocked(user, space, permission string) bool {
	if s.failed != nil || s.pg != nil && (s.pg.err != nil || s.pg.tx == nil) {
		return false
	}
	o := s.data.Organizations[space]
	if o.Type == "personal" && o.OwnerID != user {
		return false
	}
	if !identity.EffectiveAllows(o, user, permission) {
		return false
	}
	if s.pg != nil {
		var allowed bool
		err := s.pg.tx.QueryRowContext(s.pg.ctx, `SELECT EXISTS(SELECT 1 FROM studio_space_memberships m LEFT JOIN studio_space_member_roles c ON c.space_id=m.space_id AND c.user_id=m.user_id JOIN studio_rbac_role_permissions p ON p.role_id=COALESCE(c.base_role,m.role_id) WHERE m.space_id=$1 AND m.user_id=$2 AND p.permission_id=$3 AND ((c.role_id IS NULL AND m.role_id<>'custom') OR (m.role_id='custom' AND EXISTS(SELECT 1 FROM studio_space_role_permissions g WHERE g.space_id=c.space_id AND g.role_id=c.role_id AND g.permission_id=$3))))`, space, user, permission).Scan(&allowed)
		return err == nil && allowed
	}
	return true
}

func (s *AuthStore) sessionSpaces(user string) []map[string]any {
	spaces := s.spaces(user)
	out := make([]map[string]any, 0, len(spaces))
	for _, space := range spaces {
		item := map[string]any{}
		for key, value := range space {
			item[key] = value
		}
		item["permissions"] = s.permissions(user, space["id"])
		s.mu.Lock()
		o := s.data.Organizations[space["id"]]
		item["version"] = spaceVersion(o)
		if o.Closed {
			item["closed"] = true
		}
		if id := o.MemberRoles[user]; id != "" {
			item["custom_role_id"] = id
			item["custom_role_name"] = o.CustomRoles[id].Name
		}
		s.mu.Unlock()
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a["type"] != b["type"] {
			return a["type"] == "personal"
		}
		return a["id"].(string) < b["id"].(string)
	})
	return out
}

func (s *AuthStore) permissions(user, space string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	if s.failed != nil || s.pg != nil && s.pg.err != nil {
		return out
	}
	o := s.data.Organizations[space]
	if o.Type == "personal" && o.OwnerID != user {
		return out
	}
	if s.pg == nil {
		return identity.EffectivePermissions(o, user)
	}
	rows, err := s.pg.tx.QueryContext(s.pg.ctx, `SELECT p.permission_id FROM studio_space_memberships m LEFT JOIN studio_space_member_roles c ON c.space_id=m.space_id AND c.user_id=m.user_id JOIN studio_rbac_role_permissions p ON p.role_id=COALESCE(c.base_role,m.role_id) WHERE m.space_id=$1 AND m.user_id=$2 ORDER BY p.permission_id`, space, user)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var permission string
		if rows.Scan(&permission) != nil {
			return []string{}
		}
		if identity.EffectiveAllows(o, user, permission) {
			out = append(out, permission)
		}
	}
	if rows.Err() != nil {
		return []string{}
	}
	return out
}

// File snapshots preserve scoped custom policies. Refuse a lossy export after
// built-in grants were changed by an operator; native PG backups preserve them.
var errRBACFileExport = errors.New("nonstandard RBAC grants require native PostgreSQL backup; file export refused")

func validateFileExportGrants(ctx context.Context, tx *sql.Tx) error {
	expected := map[string]bool{}
	for _, role := range identity.Roles() {
		for _, p := range identity.Permissions(role) {
			expected[role+"\x00"+p] = true
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT role_id,permission_id FROM studio_rbac_role_permissions`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var role, p string
		if err = rows.Scan(&role, &p); err != nil {
			return err
		}
		key := role + "\x00" + p
		if !expected[key] {
			return errRBACFileExport
		}
		delete(expected, key)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(expected) != 0 {
		return errRBACFileExport
	}
	return nil
}
