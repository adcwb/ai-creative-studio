package application

import (
	"errors"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/identity"
)

const customRBACSchema = `
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_membership_role_identity') THEN
 ALTER TABLE studio_space_memberships ADD CONSTRAINT studio_membership_role_identity UNIQUE(space_id,user_id,role_id);
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_space_kind_identity') THEN
 ALTER TABLE studio_space_access ADD CONSTRAINT studio_space_kind_identity UNIQUE(space_id,kind);
 END IF;
END $$;
CREATE TABLE IF NOT EXISTS studio_space_roles(
 space_id text NOT NULL REFERENCES studio_space_access(space_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
 space_kind text NOT NULL DEFAULT 'team' CHECK(space_kind='team'),
 id text NOT NULL CHECK(id ~ '^[a-f0-9]{32}$'),name text NOT NULL CHECK(length(name)>0 AND length(name)<=80),
 base_role text NOT NULL REFERENCES studio_rbac_roles(id) CHECK(base_role IN ('admin','editor','viewer')),
 version bigint NOT NULL CHECK(version>0),PRIMARY KEY(space_id,id),UNIQUE(space_id,id,base_role),
 CONSTRAINT studio_space_role_team FOREIGN KEY(space_id,space_kind) REFERENCES studio_space_access(space_id,kind) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED);
CREATE TABLE IF NOT EXISTS studio_space_role_permissions(
 space_id text NOT NULL,role_id text NOT NULL,permission_id text NOT NULL REFERENCES studio_rbac_permissions(id),
 PRIMARY KEY(space_id,role_id,permission_id),FOREIGN KEY(space_id,role_id) REFERENCES studio_space_roles(space_id,id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED);
CREATE TABLE IF NOT EXISTS studio_space_member_roles(
 space_id text NOT NULL,user_id text NOT NULL,role_id text NOT NULL,base_role text NOT NULL,membership_role text NOT NULL DEFAULT 'custom' CHECK(membership_role='custom'),
 PRIMARY KEY(space_id,user_id),
 FOREIGN KEY(space_id,user_id,membership_role) REFERENCES studio_space_memberships(space_id,user_id,role_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(space_id,role_id,base_role) REFERENCES studio_space_roles(space_id,id,base_role) DEFERRABLE INITIALLY DEFERRED);
`

func validateAuthFormat(d authSnapshot) error {
	if d.Version != 1 && d.Version != 2 && d.Version != 3 {
		return errors.New("unsupported authentication format")
	}
	for _, o := range d.Organizations {
		if o.Closed && (d.Version < 3 || spaceType(o) != "team" || len(o.Members) != 1 || o.Members[o.OwnerID] != "owner" || len(o.MemberRoles) != 0) {
			return errors.New("invalid closed team identity")
		}
	}
	if d.Version == 1 {
		for _, o := range d.Organizations {
			if len(o.CustomRoles) > 0 || len(o.MemberRoles) > 0 {
				return errors.New("custom roles require auth format 2")
			}
		}
	}
	return nil
}

func (p *postgresScope) loadCustomRBAC(organizations map[string]AuthOrganization) error {
	for id, o := range organizations {
		o.CustomRoles = map[string]identity.CustomRole{}
		o.MemberRoles = map[string]string{}
		organizations[id] = o
	}
	rows, err := p.tx.QueryContext(p.ctx, `SELECT space_id,id,name,base_role,version FROM studio_space_roles`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var space string
		var role identity.CustomRole
		if err = rows.Scan(&space, &role.ID, &role.Name, &role.Base, &role.Version); err != nil {
			rows.Close()
			return err
		}
		o, ok := organizations[space]
		if !ok {
			rows.Close()
			return errors.New("orphan custom role")
		}
		role.Permissions = []string{}
		o.CustomRoles[role.ID] = role
		organizations[space] = o
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = p.tx.QueryContext(p.ctx, `SELECT space_id,role_id,permission_id FROM studio_space_role_permissions ORDER BY permission_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var space, id, permission string
		if err = rows.Scan(&space, &id, &permission); err != nil {
			rows.Close()
			return err
		}
		o, ok := organizations[space]
		r, found := o.CustomRoles[id]
		if !ok || !found {
			rows.Close()
			return errors.New("orphan custom permission")
		}
		r.Permissions = append(r.Permissions, permission)
		o.CustomRoles[id] = r
		organizations[space] = o
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = p.tx.QueryContext(p.ctx, `SELECT space_id,user_id,role_id,base_role FROM studio_space_member_roles`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var space, user, id, base string
		if err = rows.Scan(&space, &user, &id, &base); err != nil {
			rows.Close()
			return err
		}
		o, ok := organizations[space]
		if !ok {
			rows.Close()
			return errors.New("orphan role assignment")
		}
		o.MemberRoles[user] = id
		if o.Members[user] != "custom" || o.CustomRoles[id].Base != base {
			rows.Close()
			return errors.New("invalid custom member ceiling")
		}
		o.Members[user] = base
		organizations[space] = o
	}
	err = rows.Err()
	rows.Close()
	return err
}

func (p *postgresScope) writeCustomRBAC(o AuthOrganization) error {
	if err := identity.ValidateCustomRoles(o); err != nil {
		return err
	}
	rows, err := p.tx.QueryContext(p.ctx, `SELECT id FROM studio_space_roles WHERE space_id=$1`, o.ID)
	if err != nil {
		return err
	}
	remove := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if _, ok := o.CustomRoles[id]; !ok {
			remove = append(remove, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range remove {
		if _, err = p.tx.ExecContext(p.ctx, `DELETE FROM studio_space_roles WHERE space_id=$1 AND id=$2`, o.ID, id); err != nil {
			return err
		}
	}
	for _, r := range o.CustomRoles {
		if _, err = p.tx.ExecContext(p.ctx, `INSERT INTO studio_space_roles(space_id,id,name,base_role,version)VALUES($1,$2,$3,$4,$5) ON CONFLICT(space_id,id)DO UPDATE SET name=EXCLUDED.name,base_role=EXCLUDED.base_role,version=EXCLUDED.version`, o.ID, r.ID, r.Name, r.Base, r.Version); err != nil {
			return err
		}
		if _, err = p.tx.ExecContext(p.ctx, `DELETE FROM studio_space_role_permissions WHERE space_id=$1 AND role_id=$2`, o.ID, r.ID); err != nil {
			return err
		}
		for _, permission := range r.Permissions {
			if _, err = p.tx.ExecContext(p.ctx, `INSERT INTO studio_space_role_permissions(space_id,role_id,permission_id)VALUES($1,$2,$3)`, o.ID, r.ID, permission); err != nil {
				return err
			}
		}
	}
	return nil
}
func (p *postgresScope) writeCustomAssignments(o AuthOrganization) error {
	for user, id := range o.MemberRoles {
		r, ok := o.CustomRoles[id]
		if !ok {
			return identity.ErrCustomRole
		}
		if _, err := p.tx.ExecContext(p.ctx, `INSERT INTO studio_space_member_roles(space_id,user_id,role_id,base_role)VALUES($1,$2,$3,$4)`, o.ID, user, id, r.Base); err != nil {
			return err
		}
	}
	return nil
}
