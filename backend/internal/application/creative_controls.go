package application

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
)

type CreativeControls struct {
	MaxReferences   int  `json:"max_references"`
	Frames          bool `json:"frames"`
	MotionReference bool `json:"motion_reference"`
}

// Verified native contracts only. An unknown model must not inherit controls.
func creativeControls(provider, model, kind string) CreativeControls {
	if kind == "video" && provider == "gemini-video" && model == "gemini-omni-1.1-flash" {
		return CreativeControls{4, true, true}
	}
	if kind == "image" && provider == "gemini-interactions-images" && (model == "gemini-nano-banana-2.1" || model == "gemini-3.1-flash-image" || model == "gemini-3-pro-image") {
		return CreativeControls{MaxReferences: 4}
	}
	return CreativeControls{}
}

type referenceMedia struct {
	Data       []byte
	MIME, Role string
}
type multiReferenceImageProvider interface {
	GenerateWithReferences(context.Context, Input, []referenceMedia) (GenerationResult, error)
}
type controlledVideoProvider interface {
	SubmitControlledVideo(context.Context, string, []referenceMedia) (VideoJob, error)
}

func hasCreativeControls(in CreativeJobInput) bool {
	return len(in.ReferenceAssetIDs) > 0 || in.FirstFrameAssetID != "" || in.LastFrameAssetID != "" || in.MotionReferenceAssetID != ""
}
func (m *MediaStore) loadCreativeReferences(project, space string, in CreativeJobInput) ([]referenceMedia, error) {
	type item struct{ id, role string }
	items := []item{}
	if in.FirstFrameAssetID != "" {
		items = append(items, item{in.FirstFrameAssetID, "first"})
	}
	if in.LastFrameAssetID != "" {
		items = append(items, item{in.LastFrameAssetID, "last"})
	}
	for _, id := range in.ReferenceAssetIDs {
		items = append(items, item{id, "reference"})
	}
	if in.MotionReferenceAssetID != "" {
		items = append(items, item{in.MotionReferenceAssetID, "motion"})
	}
	total := 0
	result := []referenceMedia{}
	for _, entry := range items {
		path, mime, err := m.file(project, space, entry.id, false, false)
		if err != nil || (entry.role == "motion" && mime != "video/mp4") || (entry.role != "motion" && mime != "image/png" && mime != "image/jpeg") {
			return nil, projectInputError("参考素材必须属于当前项目且类型匹配")
		}
		st, err := os.Stat(path)
		if err != nil || st.Size() > 10<<20 {
			return nil, projectInputError("参考素材总量不得超过10MiB")
		}
		data, err := os.ReadFile(path)
		total += len(data)
		if err != nil || total > 10<<20 {
			return nil, projectInputError("参考素材总量不得超过10MiB")
		}
		result = append(result, referenceMedia{data, mime, entry.role})
	}
	return result, nil
}
func (s *FileStore) validateCreativeControls(project, space string, in CreativeJobInput, caps Capabilities) error {
	if !hasCreativeControls(in) {
		return nil
	}
	allowed := creativeControls(caps.Provider, caps.Model, in.Kind)
	if in.ReferenceAssetID != "" || in.MaskAssetID != "" || len(in.ReferenceAssetIDs) > allowed.MaxReferences || (len(in.ReferenceAssetIDs) > 0 && allowed.MaxReferences == 0) || ((in.FirstFrameAssetID != "" || in.LastFrameAssetID != "") && !allowed.Frames) || (in.LastFrameAssetID != "" && in.FirstFrameAssetID == "") || (in.MotionReferenceAssetID != "" && !allowed.MotionReference) {
		return projectInputError("所选模型不支持此参考组合，尾帧必须配合首帧，且不能混用旧单参考或遮罩")
	}
	seen := map[string]bool{}
	for _, id := range in.ReferenceAssetIDs {
		if id == "" || seen[id] {
			return projectInputError("参考图片不能为空或重复")
		}
		seen[id] = true
	}
	_, err := s.media.loadCreativeReferences(project, space, in)
	return err
}
func registerCreativeControls(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("GET /api/v1/projects/{id}/creative-controls", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		if _, err := s.GetProject(space, r.PathValue("id")); err != nil {
			storeError(w, err)
			return
		}
		q := r.URL.Query()
		respond(w, 200, creativeControls(q.Get("provider"), q.Get("model"), q.Get("kind")))
	})
}

// Build roles from ordered validated bytes. No external media URI is accepted.
func (p *GoogleMediaProvider) SubmitControlledVideo(ctx context.Context, prompt string, refs []referenceMedia) (VideoJob, error) {
	support := creativeControls(p.kind, p.model, "video")
	if !support.Frames {
		return VideoJob{}, providerFailure("unsupported_capability")
	}
	content := []map[string]string{}
	tags := []string{}
	images, referenceIndex, total := 0, 0, 0
	first, last, motion := false, false, false
	for _, r := range refs {
		total += len(r.Data)
		if total > 10<<20 {
			return VideoJob{}, providerFailure("provider_rejected")
		}
		kind := "image"
		if r.Role == "motion" {
			kind = "video"
			if motion || r.MIME != "video/mp4" || len(r.Data) < 12 || string(r.Data[4:8]) != "ftyp" {
				return VideoJob{}, providerFailure("provider_rejected")
			}
			motion = true
			tags = append(tags, "[# References <VIDEO_REF_0>@Video1] Use Video1 as motion and appearance guidance, not as a source to edit.")
		} else {
			if _, err := nativeImage(r.Data, r.MIME); err != nil {
				return VideoJob{}, err
			}
			images++
			switch r.Role {
			case "first":
				if first {
					return VideoJob{}, providerFailure("provider_rejected")
				}
				first = true
				tags = append(tags, fmt.Sprintf("[# Sources <FIRST_FRAME>@Image%d]", images))
			case "last":
				if last {
					return VideoJob{}, providerFailure("provider_rejected")
				}
				last = true
				tags = append(tags, fmt.Sprintf("[# Sources <LAST_FRAME>@Image%d]", images))
			case "reference":
				tags = append(tags, fmt.Sprintf("[# References <IMAGE_REF_%d>@Image%d]", referenceIndex, images))
				referenceIndex++
			default:
				return VideoJob{}, providerFailure("provider_rejected")
			}
		}
		content = append(content, map[string]string{"type": kind, "mime_type": r.MIME, "data": base64.StdEncoding.EncodeToString(r.Data)})
	}
	if len(refs) == 0 || referenceIndex > 4 || (last && !first) {
		return VideoJob{}, providerFailure("provider_rejected")
	}
	prompt, err := normalizeProjectText(prompt, "video", 3, 2000)
	if err != nil {
		return VideoJob{}, err
	}
	content = append(content, map[string]string{"type": "text", "text": strings.Join(tags, " ") + " " + prompt})
	return p.submitVideoInput(ctx, []map[string]any{{"type": "user_input", "content": content}})
}
