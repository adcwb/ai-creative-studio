package application

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func importPostgresAudit(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	f, err := os.Open(filepath.Join(dir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 64<<10)
	n := 0
	for scan.Scan() {
		var r auditRecord
		if json.Unmarshal(scan.Bytes(), &r) != nil {
			return n, errors.New("invalid audit event")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO studio_audit_events(request_id,phase,at,actor_id,tenant_id,role,method,status,duration_ms) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, r.RequestID, r.Phase, r.At, r.Actor, r.Tenant, r.Role, r.Method, r.Status, r.DurationMS)
		if err != nil {
			return n, errors.New("audit migration constraint failed")
		}
		n++
	}
	return n, scan.Err()
}
func exportPostgresAudit(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT request_id,phase,at,actor_id,tenant_id,role,method,status,duration_ms FROM studio_audit_events ORDER BY at,request_id,phase`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	f, err := os.OpenFile(filepath.Join(dir, "audit.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	n := 0
	for rows.Next() {
		var r auditRecord
		if err = rows.Scan(&r.RequestID, &r.Phase, &r.At, &r.Actor, &r.Tenant, &r.Role, &r.Method, &r.Status, &r.DurationMS); err != nil {
			return n, err
		}
		if err = enc.Encode(r); err != nil {
			return n, err
		}
		n++
	}
	if err = rows.Err(); err != nil {
		return n, err
	}
	return n, f.Sync()
}
