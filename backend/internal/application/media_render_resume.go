package application

import (
	"context"
	"database/sql"
	"errors"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

// Resume only deterministic local post-production. This operation cannot
// enqueue an image/video/text provider request or replace the export snapshot.
func (m *MediaStore) ResumeExport(project, space, key, id string) (MediaExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return MediaExport{}, errors.New("media store closed")
	}
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]string{"resume-export", id})
	replay, e := m.check(d, k, key, digest)
	if e != nil {
		return MediaExport{}, e
	}
	for i := range p.Exports {
		current := &p.Exports[i]
		if current.ID != id {
			continue
		}
		if replay {
			return cloneMediaExport(*current), nil
		}
		if current.RenderVersion != 1 {
			return MediaExport{}, mediaInvalid("这次导出不能继续，请重新导出")
		}
		schedule := current.Status == "failed" || current.Status == "cancelled"
		if schedule && !current.ResumeAvailable {
			return MediaExport{}, mediaInvalid("这次导出不能继续，请重新导出")
		}
		if current.Status == "succeeded" {
			return MediaExport{}, mediaInvalid("导出已完成，无需继续")
		}
		if schedule {
			current.Status = "queued"
			current.Error = ""
			current.ResumeAvailable = false
			current.RenderProgress = nil
		}
		p.Exports[i] = *current
		d.Projects[k] = p
		d.Receipts[k+":"+key] = mediaReceipt{Digest: digest, ExportID: id}
		if m.pg != nil && schedule {
			m.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
				r, e := tx.ExecContext(ctx, `UPDATE execution_jobs SET state='queued',lease_token='',lease_until=NULL,result=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND kind='export' AND payload->>'render_version'='1' AND state IN ('failed','unknown','canceled')`, space, id)
				if e != nil {
					return e
				}
				n, e := r.RowsAffected()
				if e != nil {
					return e
				}
				if n != 1 {
					return jobqueue.ErrLeaseLost
				}
				return nil
			}
			defer func() { m.pg.beforeCommit = nil }()
		}
		if e = m.commit(d); e != nil {
			return MediaExport{}, e
		}
		if m.pg == nil && schedule {
			if cancel := m.cancels[id]; cancel != nil {
				cancel()
			}
			delete(m.cancels, id)
			m.scheduleExportLocked(space, project, p, *current)
		}
		return cloneMediaExport(*current), nil
	}
	return MediaExport{}, ErrNotFound
}
