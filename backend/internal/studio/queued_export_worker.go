package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
)

func (m *MediaStore) exportInputs(space, project string, p MediaState, e MediaExport) error {
	if m.blobs == nil {
		return nil
	}
	ids := map[string]bool{}
	for _, c := range e.Timeline.Clips {
		ids[c.AssetID] = true
		if c.AudioAssetID != "" {
			ids[c.AudioAssetID] = true
		}
	}
	if e.Timeline.MusicAssetID != "" {
		ids[e.Timeline.MusicAssetID] = true
	}
	for _, track := range e.Timeline.AudioTracks {
		ids[track.AssetID] = true
	}
	for _, overlay := range e.Timeline.VideoOverlays {
		ids[overlay.AssetID] = true
	}
	for id := range ids {
		if _, err := m.localBlob(space, project, id); err != nil {
			return err
		}
	}
	return nil
}
func RunQueuedExportWorker(ctx context.Context, q jobqueue.Queue, s *FileStore) error {
	if s == nil || s.media == nil || s.media.pg == nil {
		return errors.New("export worker requires postgres")
	}
	m := s.media
	return jobqueue.Run(ctx, q, "export", func(ctx context.Context, q jobqueue.Queue, j jobqueue.Job) error {
		payload, err := projectJobPayload(j)
		if err != nil {
			return q.Finish(ctx, j, "failed", json.RawMessage(`{"code":"invalid_job"}`))
		}
		k := mediaKey(j.TenantID, payload.ProjectID)
		m.mu.Lock()
		p := mediaState(m.data, k)
		var export MediaExport
		for _, e := range p.Exports {
			if e.ID == j.ID {
				export = e
				break
			}
		}
		m.mu.Unlock()
		if export.ID == "" {
			return errors.New("queued export not found")
		}
		if export.Status == "cancelled" {
			return q.Cancel(ctx, j.TenantID, j.ID)
		}
		if err = q.Dispatch(ctx, j); err != nil {
			return err
		}
		renderCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		renderErr := m.exportInputs(j.TenantID, payload.ProjectID, p, export)
		if renderErr == nil {
			renderErr = m.render(renderCtx, p, export)
		}
		if renderErr == nil && m.blobs != nil {
			for _, ext := range []string{".mp4", ".srt", ".vtt"} {
				if renderErr = q.Renew(ctx, j); renderErr != nil {
					break
				}
				if renderErr = m.storeBlobContext(renderCtx, j.TenantID, payload.ProjectID, export.ID+ext); renderErr != nil {
					break
				}
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.pg.err != nil {
			return m.pg.err
		}
		state := "succeeded"
		if renderErr != nil {
			state = "failed"
		}
		m.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
			return jobqueue.FinishTx(ctx, tx, j, state, json.RawMessage(`{}`))
		}
		defer func() { m.pg.beforeCommit = nil }()
		return m.finishExportLocked(k, j.ID, j.TenantID, payload.ProjectID, renderErr)
	})
}
