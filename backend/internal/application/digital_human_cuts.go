package application

import (
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

type avatarSplitSpec struct {
	Mode     string
	Cuts     []float64
	Approved string
}

func validAvatarSplitSpec(mode string, cuts []float64, approved string) bool {
	if mode != "" && mode != "fixed" && mode != "pause" && mode != "manual" {
		return false
	}
	if mode == "" && approved != "" {
		return false
	}
	if mode != "manual" && len(cuts) > 0 {
		return false
	}
	if approved != "" && (len(approved) != 64 || strings.Trim(approved, "0123456789abcdef") != "") {
		return false
	}
	previous := 0.0
	for _, v := range cuts {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= previous || v > float64(math.MaxInt64/48000-1) {
			return false
		}
		previous = v
	}
	return true
}
func avatarPlanner(mode string) string {
	if mode == "" {
		return ""
	}
	return mode + "-v1"
}

// Silence is an acoustic candidate, never a claim of sentence/plot understanding.
// Search a bounded six-second PCM window for >=120ms of quiet, then use the
// midpoint of the last qualifying quiet run. No pause means the fixed window.
func avatarPauseCut(f io.ReaderAt, start, end int64) (int64, bool, error) {
	const block int64 = 960
	from := max(start+12*48000, end-6*48000)
	if from >= end {
		return end, false, nil
	}
	count := (end - from) / block
	data := make([]byte, block*2)
	runStart, runEnd := int64(-1), int64(-1)
	bestStart, bestEnd := int64(-1), int64(-1)
	finish := func() {
		if runStart >= 0 && runEnd-runStart >= 6*block {
			bestStart, bestEnd = runStart, runEnd
		}
		runStart = -1
		runEnd = -1
	}
	for i := int64(0); i < count; i++ {
		pos := from + i*block
		if _, err := f.ReadAt(data, pos*2); err != nil {
			return 0, false, err
		}
		var energy float64
		for n := 0; n < len(data); n += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(data[n:])))
			energy += v * v
		}
		if energy/float64(block) <= 64*64 {
			if runStart < 0 {
				runStart = pos
			}
			runEnd = pos + block
		} else {
			finish()
		}
	}
	finish()
	if bestStart >= 0 {
		return bestStart + (bestEnd-bestStart)/2, true, nil
	}
	return end, false, nil
}
func avatarCutBounds(f *os.File, total int64, spec avatarSplitSpec) ([]int64, []string, error) {
	if !validAvatarSplitSpec(spec.Mode, spec.Cuts, spec.Approved) {
		return nil, nil, ErrInvalidProjectInput
	}
	ends := []int64{}
	reasons := []string{}
	offset := int64(0)
	if spec.Mode == "manual" {
		for _, seconds := range spec.Cuts {
			cut := int64(math.Round(seconds * 48000))
			if cut <= offset || cut >= total || (cut+1999)/2000 <= (offset+1999)/2000 || cut-offset >= 20*48000 {
				return nil, nil, projectInputError("手动切点须递增、在音频内，且每段有完整视频帧并不足20秒")
			}
			ends = append(ends, cut)
			reasons = append(reasons, "manual")
			offset = cut
		}
		if total-offset >= 20*48000 || (total+1999)/2000 <= (offset+1999)/2000 {
			return nil, nil, projectInputError("请补充手动切点，每段须不足20秒且至少有一帧")
		}
		return append(ends, total), append(reasons, "end"), nil
	}
	for offset < total {
		end := min(offset+avatarSegmentSamples, total)
		reason := "window"
		if end == total {
			reason = "end"
		} else if spec.Mode == "pause" {
			candidate, quiet, err := avatarPauseCut(f, offset, end)
			if err != nil {
				return nil, nil, err
			}
			if quiet {
				end = candidate
				reason = "pause"
			}
		}
		if remain := total - end; remain > 0 && remain < 2000 {
			end = total
			reason = "end"
		}
		ends = append(ends, end)
		reasons = append(reasons, reason)
		offset = end
	}
	return ends, reasons, nil
}
func avatarPlanDigest(audio string, spec avatarSplitSpec, total int64, plan []avatarSegment) string {
	rows := make([][]any, 0, len(plan))
	for _, v := range plan {
		rows = append(rows, []any{v.Start, v.Samples, v.AudioSHA, v.CutReason})
	}
	hash, _ := projectFingerprint("avatar-cut-preview-v1", audio, []any{spec.Mode, spec.Cuts, total, rows})
	return hash
}

type avatarCutPreview struct {
	ID       string                  `json:"id"`
	Total    float64                 `json:"duration_seconds"`
	Segments []avatarSegmentProgress `json:"segments"`
}

func registerAvatarCutRoutes(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("POST /api/v1/projects/{id}/avatar-cut-preview", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in struct {
			Audio string    `json:"audio_asset_id"`
			Mode  string    `json:"avatar_split_mode"`
			Cuts  []float64 `json:"avatar_cut_points,omitempty"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		if _, err := s.GetProject(space, r.PathValue("id")); err != nil {
			storeError(w, err)
			return
		}
		state, err := s.media.GetChecked(r.PathValue("id"), space)
		if err != nil {
			storeError(w, err)
			return
		}
		asset, ok := mediaAsset(state, in.Audio)
		if !ok || asset.Kind != "audio" {
			storeError(w, ErrInvalidProjectInput)
			return
		}
		path, _, err := s.media.file(r.PathValue("id"), space, in.Audio, false, false)
		if err != nil {
			storeError(w, err)
			return
		}
		dir, err := nativefs.MkdirTemp("", "studio-avatar-cut-preview-*")
		if err != nil {
			storeError(w, err)
			return
		}
		defer os.RemoveAll(dir)
		if in.Mode == "" {
			storeError(w, ErrInvalidProjectInput)
			return
		}
		spec := avatarSplitSpec{Mode: in.Mode, Cuts: in.Cuts}
		plan, _, total, err := prepareAvatarSegments(r.Context(), &avatarWork{audioPath: path, videoPath: filepath.Join(dir, "unused.mp4")}, spec)
		if err != nil {
			storeError(w, err)
			return
		}
		out := avatarCutPreview{ID: avatarPlanDigest(in.Audio, spec, total, plan), Total: float64(total) / 48000, Segments: []avatarSegmentProgress{}}
		for i, v := range plan {
			out.Segments = append(out.Segments, avatarSegmentProgress{Index: i + 1, Start: float64(v.Start) / 48000, Duration: float64(v.Samples) / 48000, Status: "queued", CutReason: v.CutReason})
		}
		respond(w, 200, out)
	})
}

// One place for the exact source fingerprint, preserving all legacy digests.
func avatarSplitSourceHash(base string, job CreativeJob) (string, error) {
	if job.AvatarSplitMode == "" && len(job.AvatarCutPoints) == 0 && job.AvatarPlanSHA == "" {
		return base, nil
	}
	return projectFingerprint("avatar-split-inputs", job.ID, []any{base, job.AvatarSplitMode, job.AvatarCutPoints, job.AvatarPlanSHA})
}

// Kept as an error category for a preview that no longer matches actual PCM.
func avatarPlanChanged() error { return providerFailure("avatar_plan_changed") }
