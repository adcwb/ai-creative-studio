package studio

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// stateMutex preserves the existing domain critical sections. PostgreSQL scopes
// refresh every read under a database row lock; caches are never authoritative.
// Failed refreshes clear the cache and writes fail closed. The SQL transaction
// belongs to this critical section and is always rolled back on Unlock unless a
// domain commit has committed it. File mode remains an ordinary mutex.
type stateMutex struct {
	sync.Mutex
	scope *postgresScope
}

func (m *stateMutex) Lock() {
	m.Mutex.Lock()
	if m.scope != nil {
		m.scope.begin()
	}
}
func (m *stateMutex) Unlock() {
	if m.scope != nil {
		m.scope.end()
	}
	m.Mutex.Unlock()
}

type postgresScope struct {
	failures             atomic.Uint64
	skipMigrationBarrier bool
	db                   *sql.DB
	name                 string
	target               any
	empty                []byte
	tx                   *sql.Tx
	cancel               context.CancelFunc
	ctx                  context.Context
	err                  error
	before               map[string]map[string]json.RawMessage
	// Atomic queue enqueue/fenced completion runs before the same commit.
	beforeCommit func(context.Context, *sql.Tx) error
	replace      func([]byte) error
}

type pgCollection struct{ field, table, kind string }

var pgCollections = map[string][]pgCollection{
	"core":      {{"tasks", "studio_image_tasks", "map"}, {"assets", "studio_image_assets", "map"}, {"projects", "studio_projects", "map"}, {"script_generations", "studio_script_generations", "map"}, {"storyboard_generations", "studio_storyboard_generations", "map"}, {"project_receipts", "studio_project_receipts", "map"}, {"history", "studio_project_history", "map"}, {"image_resume_receipts", "studio_image_resume_receipts", "map"}},
	"auth":      {{"users", "studio_users", "map"}, {"organizations", "studio_organizations", "map"}, {"sessions", "studio_sessions", "map"}},
	"budget":    {{"limits", "studio_budget_limits", "map"}, {"entries", "studio_cost_entries", "map"}, {"changes", "studio_budget_changes", "array"}},
	"models":    {{"tenants", "studio_model_tenants", "map"}},
	"creative":  {{"jobs", "studio_creative_jobs", "map"}, {"receipts", "studio_creative_receipts", "map"}},
	"media":     {{"projects", "studio_media_projects", "map"}, {"receipts", "studio_media_receipts", "map"}},
	"alignment": {{"tasks", "studio_alignment_tasks", "map"}, {"receipts", "studio_alignment_receipts", "map"}},
}

// InitPostgres creates domain tables, not a JSON snapshot mirror. Domain records
// have tenant/identity/version/state columns and constrained relations; payload
// JSONB only contains the variable record body. Run using a migration account.
func InitPostgres(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("postgres migration connection failed")
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(728219114)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS studio_schema(version integer PRIMARY KEY CHECK(version=1)); INSERT INTO studio_schema VALUES(1) ON CONFLICT DO NOTHING; CREATE TABLE IF NOT EXISTS studio_scopes(name text PRIMARY KEY, revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0), envelope jsonb NOT NULL);`); err != nil {
		return err
	}
	names := make([]string, 0, len(pgCollections))
	for n := range pgCollections {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, c := range pgCollections[name] {
			ddl := `CREATE TABLE IF NOT EXISTS ` + c.table + ` (record_key text PRIMARY KEY, tenant_id text NOT NULL, entity_id text NOT NULL, version bigint NOT NULL DEFAULT 0 CHECK(version>=0), state text NOT NULL DEFAULT '', parent_id text, currency text, amount bigint, reserved bigint, actual bigint, payload jsonb NOT NULL, UNIQUE(tenant_id,entity_id))`
			if _, err = tx.ExecContext(ctx, ddl); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS `+c.table+`_tenant ON `+c.table+`(tenant_id)`); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS studio_users_identity ON studio_users(entity_id); CREATE UNIQUE INDEX IF NOT EXISTS studio_organizations_identity ON studio_organizations(entity_id);`); err != nil {
		return err
	}
	// Adding named constraints conditionally keeps migrations restartable.
	constraints := map[string]string{
		"studio_media_project_fk":      `ALTER TABLE studio_media_projects ADD CONSTRAINT studio_media_project_fk FOREIGN KEY(tenant_id,parent_id) REFERENCES studio_projects(tenant_id,entity_id) DEFERRABLE INITIALLY DEFERRED`,
		"studio_sessions_user_fk":      `ALTER TABLE studio_sessions ADD CONSTRAINT studio_sessions_user_fk FOREIGN KEY(parent_id) REFERENCES studio_users(entity_id) DEFERRABLE INITIALLY DEFERRED`,
		"studio_organization_owner_fk": `ALTER TABLE studio_organizations ADD CONSTRAINT studio_organization_owner_fk FOREIGN KEY(parent_id) REFERENCES studio_users(entity_id) DEFERRABLE INITIALLY DEFERRED`,
		"studio_creative_project_fk":   `ALTER TABLE studio_creative_jobs ADD CONSTRAINT studio_creative_project_fk FOREIGN KEY(tenant_id,parent_id) REFERENCES studio_projects(tenant_id,entity_id) DEFERRABLE INITIALLY DEFERRED`,
		"studio_alignment_project_fk":  `ALTER TABLE studio_alignment_tasks ADD CONSTRAINT studio_alignment_project_fk FOREIGN KEY(tenant_id,parent_id) REFERENCES studio_projects(tenant_id,entity_id) DEFERRABLE INITIALLY DEFERRED`,
		"studio_image_assets_task_fk":  `ALTER TABLE studio_image_assets ADD CONSTRAINT studio_image_assets_task_fk FOREIGN KEY(tenant_id,parent_id) REFERENCES studio_image_tasks(tenant_id,entity_id) DEFERRABLE INITIALLY DEFERRED`,
		"studio_script_project_fk":     `ALTER TABLE studio_script_generations ADD CONSTRAINT studio_script_project_fk FOREIGN KEY(tenant_id,parent_id) REFERENCES studio_projects(tenant_id,entity_id) DEFERRABLE INITIALLY DEFERRED`,
		"studio_storyboard_project_fk": `ALTER TABLE studio_storyboard_generations ADD CONSTRAINT studio_storyboard_project_fk FOREIGN KEY(tenant_id,parent_id) REFERENCES studio_projects(tenant_id,entity_id) DEFERRABLE INITIALLY DEFERRED`,
		"studio_cost_bounds":           `ALTER TABLE studio_cost_entries ADD CONSTRAINT studio_cost_bounds CHECK (tenant_id<>'' AND currency IN ('USD','EUR','GBP','CNY','CAD','AUD','CHF','HKD','SGD','JPY','KRW','KWD','BHD') AND reserved>0 AND actual>=0 AND state IN ('reserved','unknown','settled','released'))`,
		"studio_budget_bounds":         `ALTER TABLE studio_budget_limits ADD CONSTRAINT studio_budget_bounds CHECK(tenant_id<>'' AND amount>=0 AND currency IN ('USD','EUR','GBP','CNY','CAD','AUD','CHF','HKD','SGD','JPY','KRW','KWD','BHD'))`,
		"studio_image_status":          `ALTER TABLE studio_image_tasks ADD CONSTRAINT studio_image_status CHECK(tenant_id<>'' AND state IN ('queued','running','succeeded','failed'))`,
	}

	for _, table := range []string{"studio_image_tasks", "studio_image_assets", "studio_projects", "studio_script_generations", "studio_storyboard_generations", "studio_creative_jobs", "studio_alignment_tasks"} {
		name := table + "_identity_match"
		constraints[name] = `ALTER TABLE ` + table + ` ADD CONSTRAINT ` + name + ` CHECK(tenant_id<>'' AND tenant_id=payload->>'space_id' AND entity_id=payload->>'id')`
	}
	constraints["studio_cost_identity_match"] = `ALTER TABLE studio_cost_entries ADD CONSTRAINT studio_cost_identity_match CHECK(tenant_id=payload->>'tenant' AND entity_id=payload->>'id' AND currency=payload->>'currency' AND reserved=(payload->>'reserved_minor')::bigint AND actual=(payload->>'actual_minor')::bigint AND state=payload->>'state')`
	for name, ddl := range constraints {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname=$1 AND connamespace=current_schema()::regnamespace)`, name).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err = tx.ExecContext(ctx, ddl); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS studio_user_email ON studio_users((payload->>'email')); CREATE UNIQUE INDEX IF NOT EXISTS studio_user_sso ON studio_users((payload->>'issuer'),(payload->>'subject')) WHERE payload->>'issuer'<>''; CREATE TABLE IF NOT EXISTS studio_auth_attempts (address_hash text PRIMARY KEY, attempts integer NOT NULL CHECK(attempts>0), expires timestamptz NOT NULL); CREATE TABLE IF NOT EXISTS studio_oidc_pending (state_hash text PRIMARY KEY, binding_hash text NOT NULL, payload jsonb NOT NULL, expires timestamptz NOT NULL);`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, BlobSchema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, PostgresAuditSchema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, jobqueue.Schema); err != nil {
		return err
	}
	return tx.Commit()
}

func attachPostgres(db *sql.DB, name string, target any, mu *stateMutex) (*postgresScope, error) {
	raw, err := json.Marshal(target)
	if err != nil {
		return nil, err
	}
	p := &postgresScope{db: db, name: name, target: target, empty: raw}
	mu.scope = p
	mu.Lock()
	defer mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	return p, nil
}
func (p *postgresScope) begin() {
	p.err = nil
	p.before = nil
	p.ctx, p.cancel = context.WithTimeout(context.Background(), 30*time.Second)
	p.tx, p.err = p.db.BeginTx(p.ctx, nil)
	if p.err == nil {
		_, p.err = p.tx.ExecContext(p.ctx, `SET LOCAL synchronous_commit = on`)
	}
	if p.err == nil && !p.skipMigrationBarrier {
		_, p.err = p.tx.ExecContext(p.ctx, `SELECT pg_advisory_xact_lock_shared(728219117)`)
	}
	if p.err == nil {
		var env map[string]json.RawMessage
		_ = json.Unmarshal(p.empty, &env)
		for _, c := range pgCollections[p.name] {
			delete(env, c.field)
		}
		raw, _ := json.Marshal(env)
		_, p.err = p.tx.ExecContext(p.ctx, `INSERT INTO studio_scopes(name,envelope) VALUES($1,$2) ON CONFLICT DO NOTHING`, p.name, raw)
	}
	var envelope []byte
	if p.err == nil {
		p.err = p.tx.QueryRowContext(p.ctx, `SELECT envelope FROM studio_scopes WHERE name=$1 FOR UPDATE`, p.name).Scan(&envelope)
	}
	var d map[string]json.RawMessage
	if p.err == nil {
		p.err = json.Unmarshal(envelope, &d)
	}
	p.before = map[string]map[string]json.RawMessage{}
	if p.err == nil {
		for _, c := range pgCollections[p.name] {
			values := map[string]json.RawMessage{}
			rows, err := p.tx.QueryContext(p.ctx, `SELECT record_key,payload FROM `+c.table)
			if err != nil {
				p.err = err
				break
			}
			for rows.Next() {
				var k string
				var b []byte
				if err = rows.Scan(&k, &b); err != nil {
					break
				}
				decoded, e := base64.RawURLEncoding.DecodeString(k)
				if e != nil {
					err = e
					break
				}
				values[string(decoded)] = b
			}
			err = errors.Join(err, rows.Err(), rows.Close())
			if err != nil {
				p.err = err
				break
			}
			p.before[c.field] = values
			if c.kind == "array" {
				arr := make([]json.RawMessage, len(values))
				for i := range arr {
					key := fmt.Sprintf("%012d", i)
					v, ok := values[key]
					if !ok {
						p.err = errors.New("non-contiguous postgres sequence")
						break
					}
					arr[i] = v
				}
				d[c.field], _ = json.Marshal(arr)
			} else {
				d[c.field], _ = json.Marshal(values)
			}
		}
	}
	if p.err == nil {
		var raw []byte
		raw, p.err = json.Marshal(d)
		if p.err == nil {
			p.err = p.replaceValue(raw)
		}
	}
	if p.err != nil { // Never serve previously cached ownership or records on DB failure.
		_ = p.replaceValue(p.empty)
		p.err = errors.New("postgres state unavailable")
		p.failures.Add(1)
	}
}
func (p *postgresScope) end() {
	if p.tx != nil {
		_ = p.tx.Rollback()
		p.tx = nil
	}
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}
func splitPostgres(raw []byte, cols []pgCollection) (map[string]json.RawMessage, map[string]map[string]json.RawMessage, error) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, nil, err
	}
	out := map[string]map[string]json.RawMessage{}
	for _, c := range cols {
		values := map[string]json.RawMessage{}
		b := env[c.field]
		if len(b) > 0 && string(b) != "null" {
			if c.kind == "array" {
				var arr []json.RawMessage
				if err := json.Unmarshal(b, &arr); err != nil {
					return nil, nil, err
				}
				for i, v := range arr {
					values[fmt.Sprintf("%012d", i)] = v
				}
			} else if err := json.Unmarshal(b, &values); err != nil {
				return nil, nil, err
			}
		}
		out[c.field] = values
		delete(env, c.field)
	}
	return env, out, nil
}
func recordColumns(scope, field, key string, raw json.RawMessage) (tenant, id, state string, version int64, parent, currency, amount, reserved, actual any) {
	id = base64.RawURLEncoding.EncodeToString([]byte(key))
	var v map[string]json.RawMessage
	_ = json.Unmarshal(raw, &v)
	str := func(k string) string { var x string; _ = json.Unmarshal(v[k], &x); return x }
	tenant = str("space_id")
	if tenant == "" {
		tenant = str("tenant")
	}
	state = str("status")
	if state == "" {
		state = str("state")
	}
	if x := str("id"); x != "" {
		id = x
	}
	_ = json.Unmarshal(v["version"], &version)
	if x := str("project_id"); x != "" {
		parent = x
	}
	if scope == "core" && field == "assets" {
		parent = str("task_id")
	}
	if scope == "auth" && field == "organizations" {
		tenant = str("id")
		parent = str("owner_id")
	}
	if scope == "auth" && field == "sessions" {
		parent = str("user_id")
	}
	if scope == "models" {
		tenant = key
	}
	if scope == "media" {
		parts := strings.SplitN(key, ":", 2)
		tenant = parts[0]
		if field == "projects" && len(parts) == 2 {
			id = parts[1]
			parent = parts[1]
		}
	}
	if scope == "budget" && field == "limits" {
		parts := strings.Split(key, "\x00")
		if len(parts) == 2 {
			tenant = parts[0]
			currency = parts[1]
		}
		var n int64
		_ = json.Unmarshal(raw, &n)
		amount = n
	}
	if scope == "budget" && field == "entries" {
		currency = str("currency")
		var n, a int64
		_ = json.Unmarshal(v["reserved_minor"], &n)
		_ = json.Unmarshal(v["actual_minor"], &a)
		reserved = n
		actual = a
	}
	return
}
func sameJSON(a, b []byte) bool {
	var x, y any
	left := json.NewDecoder(bytes.NewReader(a))
	left.UseNumber()
	right := json.NewDecoder(bytes.NewReader(b))
	right.UseNumber()
	if left.Decode(&x) != nil || right.Decode(&y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func (p *postgresScope) write(value any) error {
	if p.err != nil {
		return p.err
	}
	if p.tx == nil {
		return errors.New("postgres mutation outside transaction")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	env, records, err := splitPostgres(raw, pgCollections[p.name])
	if err != nil {
		return err
	}
	for _, c := range pgCollections[p.name] {
		before := p.before[c.field]
		next := records[c.field]
		for key := range before {
			if _, ok := next[key]; !ok {
				if _, err = p.tx.ExecContext(p.ctx, `DELETE FROM `+c.table+` WHERE record_key=$1`, base64.RawURLEncoding.EncodeToString([]byte(key))); err != nil {
					return errors.New("postgres record delete failed")
				}
			}
		}
		for key, b := range next {
			if sameJSON(before[key], b) {
				continue
			}
			tenant, id, state, version, parent, currency, amount, reserved, actual := recordColumns(p.name, c.field, key, b)
			_, err = p.tx.ExecContext(p.ctx, `INSERT INTO `+c.table+`(record_key,tenant_id,entity_id,version,state,parent_id,currency,amount,reserved,actual,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(record_key) DO UPDATE SET tenant_id=EXCLUDED.tenant_id,entity_id=EXCLUDED.entity_id,version=EXCLUDED.version,state=EXCLUDED.state,parent_id=EXCLUDED.parent_id,currency=EXCLUDED.currency,amount=EXCLUDED.amount,reserved=EXCLUDED.reserved,actual=EXCLUDED.actual,payload=EXCLUDED.payload`, base64.RawURLEncoding.EncodeToString([]byte(key)), tenant, id, version, state, parent, currency, amount, reserved, actual, []byte(b))
			if err != nil {
				return errors.New("postgres record constraint or write failed")
			}
		}
	}
	envelope, _ := json.Marshal(env)
	if _, err = p.tx.ExecContext(p.ctx, `UPDATE studio_scopes SET revision=revision+1,envelope=$2 WHERE name=$1`, p.name, envelope); err != nil {
		return errors.New("postgres revision write failed")
	}
	if p.beforeCommit != nil {
		if err = p.beforeCommit(p.ctx, p.tx); err != nil {
			return err
		}
	}
	return nil
}
func (p *postgresScope) save(value any) (result error) {
	defer func() {
		if result != nil {
			p.failures.Add(1)
		}
	}()
	if err := p.write(value); err != nil {
		return err
	}
	if err := p.tx.Commit(); err != nil {
		p.err = errors.New("postgres commit outcome uncertain; do not retry external dispatch")
		return p.err
	}
	p.tx = nil
	return nil
}

// OpenPostgresStore uses dir only for private media cache/result files. It never
// imports existing local snapshots implicitly: migration is an explicit command.
// Multiple processes use distinct cache directories until BlobStore is enabled.
func OpenPostgresStore(dir string, db *sql.DB, options ...Capabilities) (*FileStore, error) {
	if db == nil {
		return nil, errors.New("postgres database required")
	}
	for _, name := range []string{"state.json", "auth.json", "budget.json", "creative.json", "model-config.json", "media/state.json", "media/alignments.json"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return nil, errors.New("postgres cache contains legacy snapshots; use explicit offline migration and a fresh cache directory")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "results"), 0700); err != nil {
		return nil, err
	}
	cacheLock, err := os.OpenFile(filepath.Join(dir, "store.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(cacheLock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		cacheLock.Close()
		return nil, errors.New("PostgreSQL cache already in use; each process requires a private cache")
	}
	marker, err := os.OpenFile(filepath.Join(dir, ".postgres-cache"), os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		cacheLock.Close()
		return nil, err
	}
	if err = marker.Sync(); err != nil {
		marker.Close()
		cacheLock.Close()
		return nil, err
	}
	marker.Close()
	s := &FileStore{lock: cacheLock, path: filepath.Join(dir, "state.json"), data: emptySnapshot(), capabilities: mockCapabilities()}
	if len(options) > 0 {
		s.capabilities = options[0]
	}
	s.results, err = os.OpenRoot(filepath.Join(dir, "results"))
	if err != nil {
		s.Close()
		return nil, err
	}
	s.creative, err = openCreativeStore(dir, db)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.media, err = openMediaStore(dir, db)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.budget = &BudgetLedger{path: filepath.Join(dir, "budget.json"), data: costSnapshot{Version: 1, Limits: map[string]int64{}, Entries: map[string]CostEntry{}}}
	s.pg, err = attachPostgres(db, "core", &s.data, &s.mu)
	if err == nil {
		s.budget.pg, err = attachPostgres(db, "budget", &s.budget.data, &s.budget.mu)
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func OpenPostgresAuthStore(dir string, db *sql.DB) (*AuthStore, error) {
	s := &AuthStore{path: filepath.Join(dir, "auth.json"), attempts: map[string]authAttempt{}, data: authSnapshot{Version: 1, Users: map[string]authAccount{}, Organizations: map[string]AuthOrganization{}, Sessions: map[string]authSession{}}}
	var err error
	s.pg, err = attachPostgres(db, "auth", &s.data, &s.mu)
	if err != nil {
		return nil, err
	}
	s.pg.replace = func(raw []byte) error {
		var d authSnapshot
		if err := json.Unmarshal(raw, &d); err != nil {
			return err
		}
		s.cancelChangedRuntimeLocked(s.data, d)
		s.data = d
		return nil
	}
	s.models, err = OpenPostgresModelConfigStore(dir, db, nil)
	return s, err
}
func OpenPostgresModelConfigStore(dir string, db *sql.DB, key []byte) (*ModelConfigStore, error) {
	// The ordinary constructor validates the deployment KEK. No credentials are
	// fetched here and the fresh cache has no model-config snapshot.
	s, err := OpenModelConfigStore(dir, key)
	if err != nil {
		return nil, err
	}
	s.pg, err = attachPostgres(db, "models", &s.data, &s.mu)
	if err == nil {
		s.pg.replace = func(raw []byte) error {
			var d modelSnapshot
			if err := json.Unmarshal(raw, &d); err != nil {
				return err
			}
			s.publishRuntimeSnapshot(d)
			return nil
		}
	}
	return s, err
}

func replacePostgresValue(raw []byte, target any) error {
	v := reflect.ValueOf(target)
	fresh := reflect.New(v.Elem().Type())
	if err := json.Unmarshal(raw, fresh.Interface()); err != nil {
		return err
	}
	v.Elem().Set(fresh.Elem())
	return nil
}

func (p *postgresScope) replaceValue(raw []byte) error {
	if p.replace != nil {
		return p.replace(raw)
	}
	return replacePostgresValue(raw, p.target)
}
