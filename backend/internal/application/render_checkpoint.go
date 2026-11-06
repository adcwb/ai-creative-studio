package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

const RenderSchema = `CREATE TABLE IF NOT EXISTS studio_render_checkpoints (
 space_id text NOT NULL,project_id text NOT NULL,export_id text NOT NULL,step text NOT NULL,
 payload jsonb NOT NULL,PRIMARY KEY(space_id,project_id,export_id,step));`

var errRenderInputsChanged = errors.New("render inputs or encoder changed; create a new export")

const renderEngine = "segmented-v1-h264-pcm-24-48000"

type renderScope struct {
	Space, Project string
	Lease          *jobqueue.Job
}
type renderScopeKey struct{}
type renderManifest struct {
	Purpose             string           `json:"purpose,omitempty"`
	PreviewSourceSHA256 string           `json:"preview_source_sha256,omitempty"`
	Version             int              `json:"version"`
	SegmentFrames       int64            `json:"segment_frames"`
	Space               string           `json:"space"`
	Project             string           `json:"project"`
	Export              string           `json:"export"`
	Engine              string           `json:"engine"`
	Encoder             string           `json:"encoder"`
	Timeline            MediaTimeline    `json:"timeline"`
	Plan                media.RenderPlan `json:"plan"`
	Sources             []BlobInfo       `json:"sources"`
}
type renderArtifact struct {
	State    string   `json:"state"`
	Manifest string   `json:"manifest"`
	Kind     string   `json:"kind"`
	Frames   int64    `json:"frames"`
	Info     BlobInfo `json:"info"`
	Step     string   `json:"step"`
	Start    int64    `json:"start_frame"`
	End      int64    `json:"end_frame"`
}
type renderSession struct {
	m         *MediaStore
	scope     renderScope
	manifest  renderManifest
	hash, dir string
	ctx       context.Context
}

var renderEncoderOnce sync.Once
var renderEncoderHash string

func renderEncoder() (string, error) {
	renderEncoderOnce.Do(func() {
		var b strings.Builder
		for _, tool := range []string{"ffmpeg", "ffprobe"} {
			out, e := exec.Command(tool, "-version").Output()
			if e != nil {
				return
			}
			b.Write(out)
		}
		renderEncoderHash = s3Hash(b.String())
	})
	if renderEncoderHash == "" {
		return "", errors.New("local encoder unavailable")
	}
	return renderEncoderHash, nil
}
func renderOwner(space, project, id string) string { return mediaDigest([]string{space, project, id}) }
func atomicRenderJSON(path string, value any) error {
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	return syncMultipartDir(filepath.Dir(path))
}
func renderInputIDs(t MediaTimeline) []string {
	ids := map[string]bool{}
	for _, c := range t.Clips {
		ids[c.AssetID] = true
		if c.AudioAssetID != "" {
			ids[c.AudioAssetID] = true
		}
		if c.SourceAudioAssetID != "" {
			ids[c.SourceAudioAssetID] = true
		}
	}
	if t.MusicAssetID != "" {
		ids[t.MusicAssetID] = true
	}
	for _, a := range t.AudioTracks {
		ids[a.AssetID] = true
	}
	for _, a := range t.VideoOverlays {
		ids[a.AssetID] = true
	}
	out := []string{}
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
func (m *MediaStore) withRenderSession(ctx context.Context, p MediaState, e MediaExport, fn func(*renderSession) error) error {
	if err := validatePreviewExport(e); err != nil {
		return err
	}
	if !mediaFileID(e.ID) {
		return ErrBlobIntegrity
	}
	if err := validateTimeline(p, e.Timeline); err != nil {
		return err
	}
	scope, ok := ctx.Value(renderScopeKey{}).(renderScope)
	if !ok {
		scope = renderScope{Space: "standalone", Project: e.ID}
	}
	if m.pg != nil && (scope.Lease == nil || scope.Lease.TenantID != scope.Space || scope.Lease.ID != e.ID) {
		return jobqueue.ErrLeaseLost
	}
	encoder, err := renderEncoder()
	if err != nil {
		return err
	}
	plan, err := media.CompileRenderPlan(e.Timeline)
	if err != nil {
		return err
	}
	segmentFrames, err := renderSegmentFrames()
	if err != nil {
		return err
	}
	v := renderManifest{Purpose: e.Purpose, Version: 1, SegmentFrames: segmentFrames, Space: scope.Space, Project: scope.Project, Export: e.ID, Engine: renderEngine, Encoder: encoder, Timeline: e.Timeline, Plan: plan, Sources: []BlobInfo{}}
	if e.PreviewSource != nil {
		v.PreviewSourceSHA256 = e.PreviewSource.SHA256
	}
	for _, id := range renderInputIDs(e.Timeline) {
		if _, ok := mediaAsset(p, id); !ok {
			return ErrNotFound
		}
		path, err := m.localBlob(scope.Space, scope.Project, id)
		if err != nil {
			return err
		}
		info, err := localBlobInfo(ctx, blobKey(scope.Space, scope.Project, id), path)
		if err != nil {
			return err
		}
		if m.blobs != nil {
			expected, err := m.blobs.info(ctx, info.Key)
			if err != nil {
				return err
			}
			if expected != info {
				return ErrBlobIntegrity
			}
		}
		if e.PreviewSource != nil && !previewAssetMatches(e.PreviewSource, id, info) {
			return errRenderInputsChanged
		}
		v.Sources = append(v.Sources, info)
	}
	dir := filepath.Join(m.dir, "render-checkpoints", renderOwner(scope.Space, scope.Project, e.ID))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if m.pg == nil {
		f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		for {
			err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if err == nil {
				break
			}
			if !errors.Is(err, syscall.EWOULDBLOCK) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
		}
		defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
	s := &renderSession{m: m, scope: scope, manifest: v, hash: mediaDigest(v), dir: dir, ctx: ctx}
	b, err := s.read("manifest")
	if err == nil {
		var old renderManifest
		if json.Unmarshal(b, &old) != nil || !validRenderManifest(old) {
			return ErrBlobIntegrity
		}
		v.SegmentFrames = old.SegmentFrames
		s.manifest = v
		s.hash = mediaDigest(v)
		if mediaDigest(old) != s.hash {
			return errRenderInputsChanged
		}
	} else if errors.Is(err, os.ErrNotExist) || errors.Is(err, sql.ErrNoRows) {
		if err = s.write("manifest", v); err != nil {
			return err
		}
	} else {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".work-") || strings.HasPrefix(entry.Name(), ".assemble-") {
			if err = os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return fn(s)
}
func (s *renderSession) read(step string) ([]byte, error) {
	if s.m.pg != nil {
		var b []byte
		e := s.m.pg.db.QueryRowContext(s.ctx, `SELECT payload FROM studio_render_checkpoints WHERE space_id=$1 AND project_id=$2 AND export_id=$3 AND step=$4`, s.scope.Space, s.scope.Project, s.manifest.Export, step).Scan(&b)
		return b, e
	}
	return safeReadLocal(filepath.Join(s.dir, step+".json"), 8<<20)
}
func (s *renderSession) write(step string, v any) error {
	if e := s.ctx.Err(); e != nil {
		return e
	}
	if s.m.pg == nil {
		return atomicRenderJSON(filepath.Join(s.dir, step+".json"), v)
	}
	tx, e := s.m.pg.db.BeginTx(s.ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(s.ctx, `SET LOCAL synchronous_commit=on; SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
		return e
	}
	if e = jobqueue.CheckLeaseTx(s.ctx, tx, *s.scope.Lease); e != nil {
		return e
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(s.ctx, `INSERT INTO studio_render_checkpoints(space_id,project_id,export_id,step,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT(space_id,project_id,export_id,step) DO UPDATE SET payload=EXCLUDED.payload`, s.scope.Space, s.scope.Project, s.manifest.Export, step, b)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *renderSession) artifact(step, kind string, frames int64, produce func(string) error) (string, error) {
	key := s3Hash(step)
	start, end, err := renderStepBounds(step, frames)
	if err != nil {
		return "", err
	}
	b, e := s.read(key)
	if e == nil {
		var a renderArtifact
		if json.Unmarshal(b, &a) != nil || a.Step != step || a.Start != start || a.End != end || (a.State != "ready" && a.State != "staging") || a.Manifest != s.hash || a.Kind != kind || a.Frames != frames || !validBlobInfo(a.Info) || blobKey(s.scope.Space, s.scope.Project, filepath.Base(a.Info.Key)) != a.Info.Key {
			return "", ErrBlobIntegrity
		}
		path := filepath.Join(s.m.dir, filepath.Base(a.Info.Key))
		actual, err := localBlobInfo(s.ctx, a.Info.Key, path)
		if err == nil && actual == a.Info {
			return s.acceptArtifact(key, a, path)
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if s.m.blobs != nil && s.m.blobs.has(a.Info.Key) {
			if err == nil {
				if err = os.Remove(path); err != nil {
					return "", err
				}
			}
			if _, err = s.m.blobs.materialize(s.ctx, a.Info.Key, path); err == nil {
				actual, err = localBlobInfo(s.ctx, a.Info.Key, path)
				if err == nil && actual == a.Info {
					return s.acceptArtifact(key, a, path)
				}
			}
			if err != nil && !errors.Is(err, ErrBlobNotFound) && !errors.Is(err, ErrBlobIntegrity) {
				return "", err
			}
		}
	} else if !os.IsNotExist(e) && !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	scratch, e := os.MkdirTemp(s.dir, ".work-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(scratch)
	extension := map[string]string{"video": ".mp4", "pcm": ".wav", "final": ".mp4"}[kind]
	if extension == "" {
		return "", ErrBlobIntegrity
	}
	output := filepath.Join(scratch, "output"+extension)
	if e = produce(output); e != nil {
		return "", fmt.Errorf("render step %s: %w", step, e)
	}
	w, h, e := outputSpec(s.manifest.Timeline)
	if e != nil {
		return "", e
	}
	if e = verifyRenderArtifact(s.ctx, output, kind, frames, w, h); e != nil {
		return "", fmt.Errorf("render step %s: %w", step, e)
	}
	id := mediaID()
	path := filepath.Join(s.m.dir, id)
	info, e := localBlobInfo(s.ctx, blobKey(s.scope.Space, s.scope.Project, id), output)
	if e != nil {
		return "", e
	}
	f, e := openLocalBlob(output)
	if e != nil {
		return "", e
	}
	e = errors.Join(f.Sync(), f.Close())
	if e != nil {
		return "", e
	}
	if e = os.Link(output, path); e != nil {
		return "", e
	}
	if e = syncMultipartDir(s.m.dir); e != nil {
		return "", e
	}
	a := renderArtifact{State: "staging", Manifest: s.hash, Kind: kind, Frames: frames, Info: info, Step: step, Start: start, End: end}
	if e = s.write(key, a); e != nil {
		return "", e
	}
	return s.acceptArtifact(key, a, path)

}
func (s *renderSession) acceptArtifact(step string, a renderArtifact, path string) (string, error) {
	if a.State == "ready" {
		return path, nil
	}
	if e := s.m.storeBlobContext(s.ctx, s.scope.Space, s.scope.Project, filepath.Base(a.Info.Key)); e != nil {
		return "", e
	}
	a.State = "ready"
	if e := s.write(step, a); e != nil {
		return "", e
	}
	return path, nil
}
func verifyRenderArtifact(ctx context.Context, path, kind string, frames int64, w, h int) error {
	probe, e := probeMedia(path)
	if e != nil {
		return e
	}
	video, audio := 0, 0
	for _, stream := range probe.Streams {
		switch stream.CodecType {
		case "video":
			video++
			n, e := strconv.ParseInt(stream.NBFrames, 10, 64)
			if e != nil || n != frames || stream.CodecName != "h264" || stream.Width != w || stream.Height != h || stream.AvgFrameRate != "24/1" {
				return fmt.Errorf("render video profile mismatch: frames=%d expected=%d size=%dx%d rate=%s", n, frames, stream.Width, stream.Height, stream.AvgFrameRate)
			}
		case "audio":
			audio++
			if stream.SampleRate != "48000" || stream.Channels != 2 {
				return errors.New("render audio profile mismatch")
			}
			if kind == "final" && (stream.DurationTS != frames*media.SamplesPerFrame || stream.TimeBase != "1/48000") {
				return fmt.Errorf("final audio duration mismatch: got %d expected %d timebase=%s", stream.DurationTS, frames*media.SamplesPerFrame, stream.TimeBase)
			}
			if kind == "pcm" && (stream.DurationTS != frames*media.SamplesPerFrame || stream.TimeBase != "1/48000" || stream.CodecName != "pcm_f32le") {
				return fmt.Errorf("render sample count mismatch: got %d expected %d", stream.DurationTS, frames*media.SamplesPerFrame)
			}
		}
	}
	if kind == "video" && (video != 1 || audio != 0) || kind == "pcm" && (video != 0 || audio != 1) || kind == "final" && (video != 1 || audio != 1) {
		return errors.New("render stream count mismatch")
	}
	// Decode the whole intermediate before checkpointing. Reuse additionally checks
	// its complete SHA; a crash-created/truncated file is never an accepted stage.
	return ffmpeg(ctx, "-xerror", "-err_detect", "explode", "-protocol_whitelist", "file,pipe", "-i", path, "-map", "0:v?", "-map", "0:a?", "-f", "null", "-")
}

// Checkpoint metadata is private execution state. Offline migration preserves it
// separately from the media scope; artifact bytes use the existing blob chain.
func transferRenderFiles(ctx context.Context, tx *sql.Tx, root string, importing bool) (int, error) {
	count := 0
	if importing {
		var n int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_render_checkpoints`).Scan(&n); e != nil || n != 0 {
			return 0, errors.New("render destination not empty")
		}
		owners, e := os.ReadDir(root)
		if os.IsNotExist(e) {
			return 0, nil
		}
		if e != nil {
			return 0, e
		}
		for _, owner := range owners {
			if !owner.IsDir() {
				return 0, ErrBlobIntegrity
			}
			dir := filepath.Join(root, owner.Name())
			b, e := safeReadLocal(filepath.Join(dir, "manifest.json"), 8<<20)
			if os.IsNotExist(e) {
				files, err := os.ReadDir(dir)
				if err != nil {
					return 0, err
				}
				for _, file := range files {
					if strings.HasSuffix(file.Name(), ".json") {
						return 0, ErrBlobIntegrity
					}
				}
				continue
			}
			var v renderManifest
			if e != nil || json.Unmarshal(b, &v) != nil || !validRenderManifest(v) || owner.Name() != renderOwner(v.Space, v.Project, v.Export) {
				return 0, ErrBlobIntegrity
			}
			files, e := os.ReadDir(dir)
			if e != nil {
				return 0, e
			}
			for _, file := range files {
				if !strings.HasSuffix(file.Name(), ".json") {
					continue
				}
				b, e := safeReadLocal(filepath.Join(dir, file.Name()), 8<<20)
				if e != nil || !json.Valid(b) {
					return 0, ErrBlobIntegrity
				}
				step := strings.TrimSuffix(file.Name(), ".json")
				if step != "manifest" {
					var a renderArtifact
					if json.Unmarshal(b, &a) != nil || !validRenderArtifactRecord(a, v) || step != s3Hash(a.Step) {
						return 0, ErrBlobIntegrity
					}
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO studio_render_checkpoints(space_id,project_id,export_id,step,payload) VALUES($1,$2,$3,$4,$5)`, v.Space, v.Project, v.Export, step, b); e != nil {
					return 0, e
				}
				count++
			}
		}
		return count, nil
	}
	rows, e := tx.QueryContext(ctx, `SELECT c.space_id,c.project_id,c.export_id,c.step,c.payload,m.payload FROM studio_render_checkpoints c LEFT JOIN studio_render_checkpoints m ON m.space_id=c.space_id AND m.project_id=c.project_id AND m.export_id=c.export_id AND m.step='manifest' ORDER BY c.space_id,c.project_id,c.export_id,c.step`)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	for rows.Next() {
		var space, project, id, step string
		var b, header json.RawMessage
		if e := rows.Scan(&space, &project, &id, &step, &b, &header); e != nil {
			return 0, e
		}
		var v renderManifest
		if json.Unmarshal(header, &v) != nil || !validRenderManifest(v) || v.Space != space || v.Project != project || v.Export != id {
			return 0, ErrBlobIntegrity
		}
		if step != "manifest" {
			var a renderArtifact
			if json.Unmarshal(b, &a) != nil || !validRenderArtifactRecord(a, v) || step != s3Hash(a.Step) {
				return 0, ErrBlobIntegrity
			}
		}
		if step != "manifest" && (len(step) != 64 || strings.ContainsAny(step, "/\\.")) {
			return 0, ErrBlobIntegrity
		}
		dir := filepath.Join(root, renderOwner(space, project, id))
		if e := os.MkdirAll(dir, 0700); e != nil {
			return 0, e
		}
		if e := atomicRenderJSON(filepath.Join(dir, step+".json"), b); e != nil {
			return 0, e
		}
		count++
	}
	return count, rows.Err()
}

func (m *MediaStore) resumePendingExports() {
	if m == nil || m.pg != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, p := range m.data.Projects {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		for _, e := range p.Exports {
			if e.RenderVersion == 1 && e.Status == "queued" {
				m.scheduleExportLocked(parts[0], parts[1], p, e)
			}
		}
	}
}

func fileRenderReferences(root string) ([]BlobInfo, error) {
	owners, e := os.ReadDir(root)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	out := []BlobInfo{}
	for _, owner := range owners {
		if !owner.IsDir() {
			return nil, ErrBlobIntegrity
		}
		dir := filepath.Join(root, owner.Name())
		b, e := safeReadLocal(filepath.Join(dir, "manifest.json"), 8<<20)
		if os.IsNotExist(e) {
			continue
		}
		var v renderManifest
		if e != nil || json.Unmarshal(b, &v) != nil || !validRenderManifest(v) || owner.Name() != renderOwner(v.Space, v.Project, v.Export) {
			return nil, ErrBlobIntegrity
		}
		files, e := os.ReadDir(dir)
		if e != nil {
			return nil, e
		}
		for _, file := range files {
			if file.Name() == "manifest.json" || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			b, e := safeReadLocal(filepath.Join(dir, file.Name()), 8<<20)
			var a renderArtifact
			if e != nil || json.Unmarshal(b, &a) != nil || !validRenderArtifactRecord(a, v) || file.Name() != s3Hash(a.Step)+".json" {
				return nil, ErrBlobIntegrity
			}
			if a.State == "ready" {
				out = append(out, a.Info)
			}
		}
	}
	return out, nil
}

func (m *MediaStore) renderProgress(space, project, id string) *media.RenderProgress {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	scope := renderScope{Space: space, Project: project}
	s := &renderSession{m: m, scope: scope, manifest: renderManifest{Export: id}, dir: filepath.Join(m.dir, "render-checkpoints", renderOwner(space, project, id)), ctx: ctx}
	b, e := s.read("manifest")
	var manifest renderManifest
	if e != nil || json.Unmarshal(b, &manifest) != nil || !validRenderManifest(manifest) {
		return nil
	}
	total := manifest.Plan.BlockCount(manifest.SegmentFrames)
	if total < 1 {
		return nil
	}
	counts := map[string]int64{}
	if m.pg != nil {
		rows, e := m.pg.db.QueryContext(ctx, `SELECT payload->>'kind',count(*) FROM studio_render_checkpoints WHERE space_id=$1 AND project_id=$2 AND export_id=$3 AND step<>'manifest' AND payload->>'state'='ready' GROUP BY payload->>'kind'`, space, project, id)
		if e != nil {
			return nil
		}
		defer rows.Close()
		for rows.Next() {
			var kind string
			var n int64
			if rows.Scan(&kind, &n) != nil {
				return nil
			}
			counts[kind] = n
		}
		if rows.Err() != nil {
			return nil
		}
	} else {
		entries, e := os.ReadDir(s.dir)
		if e != nil {
			return nil
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "manifest.json" {
				continue
			}
			b, e := safeReadLocal(filepath.Join(s.dir, entry.Name()), 8<<20)
			var a renderArtifact
			if e != nil || json.Unmarshal(b, &a) != nil {
				return nil
			}
			if a.State == "ready" {
				counts[a.Kind]++
			}
		}
	}
	completed := min(total, counts["video"], counts["pcm"])
	phase := "rendering"
	if completed == total {
		phase = "assembling"
	}
	if counts["final"] > 0 {
		phase = "publishing"
	}
	return &media.RenderProgress{Completed: completed, Total: total, Phase: phase}
}

func renderSegmentFrames() (int64, error) {
	raw := strings.TrimSpace(os.Getenv("MEDIA_RENDER_SEGMENT_SECONDS"))
	if raw == "" {
		return 240, nil
	}
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n < 1 || n > 30 {
		return 0, errors.New("MEDIA_RENDER_SEGMENT_SECONDS must be an integer from 1 to 30")
	}
	return n * 24, nil
}
func renderStepBounds(step string, frames int64) (int64, int64, error) {
	if frames < 1 {
		return 0, 0, ErrBlobIntegrity
	}
	if step == "final" || step == "global-pcm" {
		return 0, frames, nil
	}
	parts := strings.Split(step, "-")
	if len(parts) != 5 || parts[0] != "block" || (parts[4] != "video" && parts[4] != "audio") {
		return 0, 0, ErrBlobIntegrity
	}
	index, e := strconv.ParseInt(parts[1], 10, 64)
	if e != nil || index < 0 {
		return 0, 0, ErrBlobIntegrity
	}
	start, e := strconv.ParseInt(parts[2], 10, 64)
	if e != nil || start < 0 {
		return 0, 0, ErrBlobIntegrity
	}
	end, e := strconv.ParseInt(parts[3], 10, 64)
	if e != nil || end <= start || end-start != frames {
		return 0, 0, ErrBlobIntegrity
	}
	return start, end, nil
}
func validRenderManifest(v renderManifest) bool {
	if v.Version != 1 || v.Space == "" || v.Project == "" || !mediaFileID(v.Export) || v.Engine == "" || len(v.Encoder) != 64 || v.SegmentFrames < 24 || v.SegmentFrames > 720 {
		return false
	}
	plan, e := media.CompileRenderPlan(v.Timeline)
	if e != nil || mediaDigest(plan) != mediaDigest(v.Plan) {
		return false
	}
	ids := renderInputIDs(v.Timeline)
	if len(ids) != len(v.Sources) {
		return false
	}
	for i, id := range ids {
		if !validBlobInfo(v.Sources[i]) || v.Sources[i].Key != blobKey(v.Space, v.Project, id) {
			return false
		}
	}
	return true
}
func validRenderArtifactRecord(a renderArtifact, v renderManifest) bool {
	start, end, e := renderStepBounds(a.Step, a.Frames)
	return e == nil && a.Start == start && a.End == end && end <= v.Plan.Frames && a.Manifest == mediaDigest(v) && (a.Kind == "video" || a.Kind == "pcm" || a.Kind == "final") && (a.State == "ready" || a.State == "staging") && validBlobInfo(a.Info) && a.Info.Size > 0 && blobKey(v.Space, v.Project, filepath.Base(a.Info.Key)) == a.Info.Key
}

func hasFileRenderState(root string) (bool, error) {
	owners, e := os.ReadDir(root)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	for _, owner := range owners {
		if !owner.IsDir() {
			return false, ErrBlobIntegrity
		}
		files, e := os.ReadDir(filepath.Join(root, owner.Name()))
		if e != nil {
			return false, e
		}
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".json") {
				return true, nil
			}
		}
	}
	return false, nil
}
