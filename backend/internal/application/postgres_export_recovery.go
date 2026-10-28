package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func postgresExportUncertainJobs(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT tenant_id,id FROM execution_jobs WHERE state IN ('leased','dispatch_started','unknown') OR kind LIKE '%\_resume' ESCAPE '\'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uncertain := map[string]bool{}
	for rows.Next() {
		var tenant, id string
		if err = rows.Scan(&tenant, &id); err != nil {
			return nil, err
		}
		uncertain[tenant+"\x00"+id] = true
	}
	return uncertain, rows.Err()
}

// A stopped worker may leave queue dispatch_started while its domain remains
// queued. File mode must never interpret that as a fresh request after rollback.
// This projection changes only the EXPORTED snapshot, preserving original DB and
// upstream IDs for explicit read-only recovery/reconciliation.
func sanitizePostgresExport(scope string, raw []byte, uncertain map[string]bool) ([]byte, error) {
	var d map[string]json.RawMessage
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	fields := map[string][]string{"core": {"tasks", "script_generations", "storyboard_generations"}, "creative": {"jobs"}, "alignment": {"tasks"}}
	for _, field := range fields[scope] {
		var records map[string]map[string]any
		decoder := json.NewDecoder(bytes.NewReader(d[field]))
		decoder.UseNumber()
		if err := decoder.Decode(&records); err != nil {
			return nil, err
		}
		for _, v := range records {
			tenant, _ := v["space_id"].(string)
			id, _ := v["id"].(string)
			status, _ := v["status"].(string)
			if uncertain[tenant+"\x00"+id] && (status == "queued" || status == "running") {
				v["status"] = "failed"
				v["progress"] = 100
				v["error_code"] = "execution_outcome_unknown"
				v["error"] = "Execution outcome and charges are unknown; automatic replay is disabled."
			}
		}
		d[field], _ = json.Marshal(records)
	}
	if scope == "media" {
		var projects map[string]MediaState
		if err := json.Unmarshal(d["projects"], &projects); err != nil {
			return nil, err
		}
		for key, p := range projects {
			tenant := strings.SplitN(key, ":", 2)[0]
			for i := range p.Exports {
				e := &p.Exports[i]
				if uncertain[tenant+"\x00"+e.ID] && (e.Status == "queued" || e.Status == "running") {
					e.Status = "failed"
					e.Error = "Local export interrupted before rollback; create a new export."
				}
			}
			projects[key] = p
		}
		d["projects"], _ = json.Marshal(projects)
	}
	if scope == "budget" {
		var entries map[string]CostEntry
		if err := json.Unmarshal(d["entries"], &entries); err != nil {
			return nil, err
		}
		for id, e := range entries {
			parts := strings.SplitN(id, ":", 2)
			if len(parts) == 2 && uncertain[e.Tenant+"\x00"+parts[1]] && e.State == "reserved" {
				e.State = "unknown"
				e.Evidence = "rollback_dispatch_outcome_unknown"
				entries[id] = e
			}
		}
		d["entries"], _ = json.Marshal(entries)
	}
	return json.Marshal(d)
}
