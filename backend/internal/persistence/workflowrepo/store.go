// Package workflowrepo persists each project's creative graph transactions as
// one atomic document. Runtime model pins never leave this repository via API.
package workflowrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	w "github.com/adcwb/ai-creative-studio/backend/internal/modules/workflows"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

var ErrStorage = errors.New("workflow storage unavailable")
var ErrNotFound = errors.New("workflow not found")
var ErrKey = errors.New("workflow idempotency conflict")

const Schema = `CREATE TABLE IF NOT EXISTS studio_workflow_projects (
 space_id text NOT NULL,project_id text NOT NULL,active boolean NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(space_id,project_id),
 CONSTRAINT studio_workflow_project_fk FOREIGN KEY(space_id,project_id) REFERENCES studio_projects(tenant_id,entity_id) DEFERRABLE INITIALLY DEFERRED,
 CHECK((payload->>'space_id'=space_id AND payload->>'project_id'=project_id AND (payload->>'format')::integer=1 AND (payload->>'active')::boolean=active) IS TRUE));
 CREATE INDEX IF NOT EXISTS studio_workflow_pending ON studio_workflow_projects(space_id,project_id) WHERE active;`

type Receipt struct {
	Digest string `json:"digest"`
	ID     string `json:"id"`
	Kind   string `json:"kind"`
}
type Document struct {
	Format    int                        `json:"format"`
	Space     string                     `json:"space_id"`
	Project   string                     `json:"project_id"`
	Active    bool                       `json:"active"`
	Workflows map[string]w.Workflow      `json:"workflows"`
	Runs      map[string]w.Run           `json:"runs"`
	Receipts  map[string]Receipt         `json:"receipts"`
	Pins      map[string]json.RawMessage `json:"runtime_pins"`
}
type Scope struct{ Space, Project string }
type Store struct {
	dir string
	db  *sql.DB
	mu  sync.Mutex
}

func Open(dir string, db *sql.DB) (*Store, error) {
	if db == nil {
		if err := nativefs.EnsurePrivateDirectory(dir); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir, db: db}, nil
}
func fresh(space, project string) Document {
	return Document{Format: 1, Space: space, Project: project, Workflows: map[string]w.Workflow{}, Runs: map[string]w.Run{}, Receipts: map[string]Receipt{}, Pins: map[string]json.RawMessage{}}
}
func valid(d Document, space, project string) bool {
	if d.Format != 1 || d.Space != space || d.Project != project || d.Workflows == nil || d.Runs == nil || d.Receipts == nil || d.Pins == nil {
		return false
	}
	active := false
	for id, v := range d.Workflows {
		if id != v.ID || !w.ID.MatchString(id) || v.Version < 1 || len(v.Revisions) != v.Version || v.Head != v.Revisions[len(v.Revisions)-1].ID {
			return false
		}
		seen := map[string]bool{}
		for index, revision := range v.Revisions {
			if !w.ID.MatchString(revision.ID) || !w.ID.MatchString(revision.Actor) || revision.Number != index+1 || seen[revision.ID] {
				return false
			}
			seen[revision.ID] = true
			if _, err := w.Validate(revision.Graph, false); err != nil || revision.SHA256 != w.Digest(revision.Graph) {
				return false
			}
		}
	}
	for id, r := range d.Runs {
		if r.FromRunID != "" {
			source, ok := d.Runs[r.FromRunID]
			if !ok || source.ID == r.ID || source.WorkflowID != r.WorkflowID || source.RevisionID != r.RevisionID || source.SHA256 != r.SHA256 || w.Digest(source.Inputs) != w.Digest(r.Inputs) || source.InputsSHA256 != r.InputsSHA256 || r.RestartNode == "" {
				return false
			}
		}
		visited := map[string]bool{r.ID: true}
		parent := r.FromRunID
		for parent != "" {
			if visited[parent] {
				return false
			}
			visited[parent] = true
			prior, ok := d.Runs[parent]
			if !ok {
				return false
			}
			parent = prior.FromRunID
		}
		if id != r.ID || !w.ID.MatchString(id) || !w.ID.MatchString(r.Actor) || r.ProjectVersion < 1 || r.Version < 1 || r.SHA256 != w.Digest(r.Graph) {
			return false
		}
		wf, exists := d.Workflows[r.WorkflowID]
		revisionMatches := false
		if exists {
			for _, rev := range wf.Revisions {
				if rev.ID == r.RevisionID && rev.SHA256 == r.SHA256 {
					revisionMatches = true
				}
			}
		}
		resolved, e := w.ResolveInputs(r.Graph, r.Inputs)
		if e != nil || r.InputsSHA256 != "" && r.InputsSHA256 != w.Digest(resolved) || len(r.Graph.Variables) > 0 && r.InputsSHA256 == "" {
			return false
		}
		if !revisionMatches {
			return false
		}
		if _, err := w.Validate(r.Graph, true); err != nil {
			return false
		}
		if len(r.Nodes) != len(r.Graph.Nodes) || !json.Valid(d.Pins[id]) {
			return false
		}
		for _, node := range r.Graph.Nodes {
			n, ok := r.Nodes[node.ID]
			if n.ReusedFrom != "" {
				source, ok := d.Runs[n.ReusedFrom]
				if !ok || n.ReusedFrom != r.FromRunID || n.Status != "succeeded" || w.Digest(n.Result) != w.Digest(source.Nodes[node.ID].Result) || source.Nodes[node.ID].Status != "succeeded" {
					return false
				}
			}
			if !ok || n.NodeID != node.ID {
				return false
			}
			switch n.Status {
			case "queued", "running", "waiting_review", "succeeded", "failed", "cancelled", "unknown":
			default:
				return false
			}
		}

		switch r.Status {
		case "queued", "running":
			active = true
		case "waiting_review":
		case "succeeded", "failed", "cancelled", "unknown":
		default:
			return false
		}
	}
	for _, receipt := range d.Receipts {
		if len(receipt.Digest) != 64 {
			return false
		}
		switch receipt.Kind {
		case "save":
			if _, ok := d.Workflows[receipt.ID]; !ok {
				return false
			}
		case "run", "rerun", "decision", "cancel", "reconcile":
			if _, ok := d.Runs[receipt.ID]; !ok {
				return false
			}
		default:
			return false
		}
	}
	return active == d.Active
}
func (s *Store) Read(ctx context.Context, space, project string) (Document, error) {
	var out Document
	err := s.access(ctx, space, project, false, func(d *Document) error { out = *d; return nil })
	return out, err
}
func (s *Store) Update(ctx context.Context, space, project string, fn func(*Document) error) error {
	return s.access(ctx, space, project, true, fn)
}
func (s *Store) access(ctx context.Context, space, project string, write bool, fn func(*Document) error) error {
	if !w.ID.MatchString(space) || !w.ID.MatchString(project) {
		return w.ErrInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := fresh(space, project)
	var raw []byte
	if s.db != nil {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return ErrStorage
		}
		defer tx.Rollback()
		if write {
			if _, err = tx.ExecContext(ctx, `SET LOCAL synchronous_commit=on; SELECT pg_advisory_xact_lock_shared(728219117)`); err != nil {
				return ErrStorage
			}
			if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,73119))`, space+":"+project); err != nil {
				return ErrStorage
			}
		}
		err = tx.QueryRowContext(ctx, `SELECT payload FROM studio_workflow_projects WHERE space_id=$1 AND project_id=$2`, space, project).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return ErrStorage
		}
		if len(raw) > 0 && (json.Unmarshal(raw, &d) != nil || !valid(d, space, project)) {
			return ErrStorage
		}
		if err = fn(&d); err != nil {
			return err
		}
		if !write {
			return nil
		}
		d.Active = false
		for _, r := range d.Runs {
			if r.Status == "queued" || r.Status == "running" {
				d.Active = true
			}
		}
		if !valid(d, space, project) {
			return ErrStorage
		}
		raw, err = json.Marshal(d)
		if err != nil {
			return ErrStorage
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO studio_workflow_projects(space_id,project_id,active,payload) VALUES($1,$2,$3,$4) ON CONFLICT(space_id,project_id) DO UPDATE SET active=EXCLUDED.active,payload=EXCLUDED.payload`, space, project, d.Active, raw); err != nil {
			return ErrStorage
		}
		if tx.Commit() != nil {
			return ErrStorage
		}
		return nil
	}
	lock, err := nativefs.OpenFile(filepath.Join(s.dir, w.Digest([]string{space, project})+".lock"), os.O_CREATE|os.O_RDWR|nativefs.NoFollow, 0600)
	if err != nil {
		return ErrStorage
	}
	defer lock.Close()
	for {
		lockErr := nativefs.Flock(int(lock.Fd()), nativefs.LOCK_EX|nativefs.LOCK_NB)
		if lockErr == nil {
			break
		}
		if !errors.Is(lockErr, nativefs.ErrWouldBlock) {
			return ErrStorage
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer nativefs.Flock(int(lock.Fd()), nativefs.LOCK_UN)
	path := filepath.Join(s.dir, w.Digest([]string{space, project})+".json")
	raw, err = readDocumentFile(path)
	if err == nil && len(raw) == 0 {
		return ErrStorage
	}
	if err != nil && !os.IsNotExist(err) {
		return ErrStorage
	}
	if len(raw) > 0 && (json.Unmarshal(raw, &d) != nil || !valid(d, space, project)) {
		return ErrStorage
	}
	if err = fn(&d); err != nil {
		return err
	}
	if !write {
		return nil
	}
	d.Active = false
	for _, r := range d.Runs {
		if r.Status == "queued" || r.Status == "running" {
			d.Active = true
		}
	}
	if !valid(d, space, project) {
		return ErrStorage
	}
	raw, err = json.Marshal(d)
	if err != nil {
		return ErrStorage
	}
	f, err := os.CreateTemp(s.dir, ".workflow-*")
	if err != nil {
		return ErrStorage
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = nativefs.SyncFile(f)
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrStorage
	}
	if nativefs.Rename(f.Name(), path) != nil {
		return ErrStorage
	}
	directory, e := os.Open(s.dir)
	if e != nil {
		return ErrStorage
	}
	defer directory.Close()
	if nativefs.SyncFile(directory) != nil {
		return ErrStorage
	}
	return nil
}

// Pending returns scopes, not graph payloads or credentials. The controller
// revisits waiting reviews without creating child tasks until explicit approval.
func (s *Store) Pending(ctx context.Context) ([]Scope, error) {
	out := []Scope{}
	if s.db != nil {
		rows, err := s.db.QueryContext(ctx, `SELECT space_id,project_id FROM studio_workflow_projects WHERE active ORDER BY space_id,project_id`)
		if err != nil {
			return nil, ErrStorage
		}
		defer rows.Close()
		for rows.Next() {
			var v Scope
			if rows.Scan(&v.Space, &v.Project) != nil {
				return nil, ErrStorage
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, ErrStorage
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := readDocumentFile(filepath.Join(s.dir, entry.Name()))
		var d Document
		if err != nil || json.Unmarshal(raw, &d) != nil || !valid(d, d.Space, d.Project) {
			return nil, ErrStorage
		}
		if entry.Name() != w.Digest([]string{d.Space, d.Project})+".json" {
			return nil, ErrStorage
		}
		if d.Active {
			out = append(out, Scope{d.Space, d.Project})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Space+out[i].Project < out[j].Space+out[j].Project })
	return out, nil
}

func readDocumentFile(path string) ([]byte, error) {
	f, err := nativefs.OpenFile(path, os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrStorage
	}
	return io.ReadAll(f)
}
