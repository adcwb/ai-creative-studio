package studio

import (
	"context"
	"database/sql"
)

// ReconcilePostgresJobs makes an uncertain execution terminal in the domain as
// well as in the queue, preserving task/upstream identities and budget exposure.
// The result is not labeled a provider failure or a proven zero-cost failure.
func ReconcilePostgresJobs(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(728219117)`); err != nil {
		return err
	}
	for _, scope := range []string{"core", "creative", "alignment"} {
		if _, err = tx.ExecContext(ctx, `SELECT name FROM studio_scopes WHERE name=$1 FOR UPDATE`, scope); err != nil {
			return err
		}
	}
	for kind, table := range map[string]string{"image": "studio_image_tasks", "script": "studio_script_generations", "storyboard": "studio_storyboard_generations", "creative": "studio_creative_jobs", "alignment": "studio_alignment_tasks"} {
		_, err = tx.ExecContext(ctx, `UPDATE `+table+` d SET state='failed',payload=d.payload || '{"status":"failed","progress":100,"error_code":"execution_outcome_unknown","error":"Execution outcome and charges are unknown; automatic replay is disabled."}'::jsonb WHERE d.state IN ('queued','running') AND EXISTS(SELECT 1 FROM execution_jobs j WHERE j.tenant_id=d.tenant_id AND j.id=d.entity_id AND j.kind=$1 AND j.state='unknown')`, kind)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
