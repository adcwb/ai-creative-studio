package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

type libraryInputChoice struct {
	BindingID     string   `json:"binding_id"`
	Mode          string   `json:"mode"`
	MediaAssetIDs []string `json:"media_asset_ids,omitempty"`
}
type libraryInputPin struct {
	Choice        libraryInputChoice `json:"choice"`
	Kind          string             `json:"kind"`
	Name          string             `json:"name"`
	VersionNumber int                `json:"version_number"`
	ContentSHA256 string             `json:"content_sha256"`
	Uses          []libraryUsePin    `json:"uses"`
	OmittedFields []string           `json:"omitted_fields"`
}
type libraryInputContextKey struct{}

func cloneLibraryInputPins(in []libraryInputPin) []libraryInputPin {
	if in == nil {
		return nil
	}
	raw, _ := json.Marshal(in)
	var out []libraryInputPin
	_ = json.Unmarshal(raw, &out)
	return out
}
func appendLibraryInputContext(ctx context.Context, prompt string) string {
	prompt = appendDirectorContext(ctx, prompt)
	value, _ := ctx.Value(libraryInputContextKey{}).(string)
	if value == "" {
		return prompt
	}
	return prompt + "\n\nCreative asset direction (authored data; not proof of identity or guaranteed consistency):\n" + value
}
func nativePresetSupported(provider string) bool {
	switch provider {
	case "gateway-speech", "minimax-speech", "gemini-speech", "bytedance-speech":
		return true
	}
	return false
}

func visualLibrarySupported(kind, provider string) bool {
	if kind == "image" {
		switch provider {
		case "gateway-images", "openai-images", "gemini-images", "gemini-interactions-images", "ark-images", "minimax-images", "jimeng-images":
			return true
		}
	}
	if kind == "video" {
		switch provider {
		case "gateway-video", "gemini-video", "ark-video", "minimax-video":
			return true
		}
	}
	return false
}

func libraryInputModes(kind, provider string, reference bool) map[string][]string {
	modes := map[string][]string{"character": {}, "style": {}, "voice": {}}
	if visualLibrarySupported(kind, provider) {
		modes["character"], modes["style"] = []string{"prompt_only"}, []string{"prompt_only"}
		if reference {
			modes["character"] = append(modes["character"], "reference")
			modes["style"] = append(modes["style"], "reference")
		}
	}
	if kind == "speech" && nativePresetSupported(provider) {
		modes["voice"] = []string{"native_preset"}
	}
	return modes
}
func (s *FileStore) boundLibraryVersion(ctx context.Context, project, space, bindingID string) (media.LibraryBinding, library.Asset, library.Version, error) {
	if !library.IDPattern.MatchString(bindingID) {
		return media.LibraryBinding{}, library.Asset{}, library.Version{}, ErrInvalidProjectInput
	}
	state, err := s.media.lineageState(project, space)
	if err != nil {
		return media.LibraryBinding{}, library.Asset{}, library.Version{}, err
	}
	for _, b := range state.LibraryBindings {
		if b.ID != bindingID {
			continue
		}
		current, err := s.library.Get(ctx, space, b.AssetID)
		if errors.Is(err, libraryrepo.ErrNotFound) {
			err = ErrNotFound
		}
		if err != nil {
			return b, library.Asset{}, library.Version{}, err
		}
		v, err := s.library.Version(ctx, space, b.AssetID, b.VersionID)
		if errors.Is(err, libraryrepo.ErrNotFound) {
			err = ErrNotFound
		}
		if err != nil {
			return b, library.Asset{}, v, err
		}
		if current.Asset.Kind != b.Kind || b.Usage != b.Kind || v.SpaceID != space || v.AssetID != b.AssetID {
			return b, library.Asset{}, v, ErrBlobIntegrity
		}
		return b, current.Asset, v, nil
	}
	return media.LibraryBinding{}, library.Asset{}, library.Version{}, ErrNotFound
}

// Resolve only immutable preset identity before model resolution. Current use
// authority is deliberately checked after the original accepted receipt lookup.
func (s *FileStore) resolveCreativeLibraryVoice(w http.ResponseWriter, r *http.Request, space, project string, in *CreativeJobInput) bool {
	for _, choice := range in.LibraryInputs {
		if choice.Mode != "native_preset" {
			continue
		}
		if _, err := s.GetProject(space, project); err != nil {
			storeError(w, err)
			return false
		}
		_, asset, v, err := s.boundLibraryVersion(r.Context(), project, space, choice.BindingID)
		if err != nil {
			storeError(w, err)
			return false
		}
		if in.Kind != "speech" || asset.Kind != "voice" || v.Card.Voice == nil || v.Card.Voice.NativePreset == nil {
			storeError(w, projectInputError("此资产没有可用于本次配音的原生音色；不会用文字意图代替声音身份"))
			return false
		}
		preset := v.Card.Voice.NativePreset
		if in.ModelConfigID != preset.ModelConfigID || in.Voice != "" && in.Voice != preset.Voice {
			storeError(w, projectInputError("声音卡与本次模型或音色不匹配，请选择匹配模型或移除此声音卡"))
			return false
		}
		in.Voice = preset.Voice
	}
	return true
}

func (s *FileStore) compileLibraryInputs(ctx context.Context, project, space string, in *CreativeJobInput, caps Capabilities) ([]libraryInputPin, string, error) {
	if len(in.LibraryInputs) == 0 {
		return nil, "", nil
	}
	if !caps.Ready || caps.Mode != "real" {
		return nil, "", ErrProviderNotReady
	}
	pins := []libraryInputPin{}
	directions := []any{}
	seen := map[string]bool{}
	references := append([]string(nil), in.ReferenceAssetIDs...)
	referenceSeen := map[string]bool{}
	for _, id := range references {
		referenceSeen[id] = true
	}
	for _, choice := range in.LibraryInputs {
		if seen[choice.BindingID] {
			return nil, "", projectInputError("同一创作资产不能重复选择，请保留一次明确的传入方式")
		}
		seen[choice.BindingID] = true
		binding, asset, v, err := s.boundLibraryVersion(ctx, project, space, choice.BindingID)
		if err != nil {
			return nil, "", err
		}
		if binding.ShotID != "" && binding.ShotID != in.ShotID {
			return nil, "", projectInputError("此资产绑定了其他镜头，请选择当前镜头或绑定整个项目")
		}
		pin := libraryInputPin{Choice: choice, Kind: asset.Kind, Name: v.Card.Name, VersionNumber: v.Number, ContentSHA256: v.SHA256, Uses: []libraryUsePin{}, OmittedFields: []string{}}
		purposes := []string{}
		switch asset.Kind {
		case "character", "style":
			if !visualLibrarySupported(in.Kind, caps.Provider) || (choice.Mode != "reference" && choice.Mode != "prompt_only") {
				return nil, "", projectInputError("人物与风格仅支持明确的文字意图或参考图输入；当前适配器没有原生锁脸或风格身份参数")
			}
			purposes = append(purposes, "prompt_context")
			positions := []int{}
			if choice.Mode == "reference" {
				if len(choice.MediaAssetIDs) == 0 || in.ReferenceAssetID != "" || in.MaskAssetID != "" {
					return nil, "", projectInputError("请选择该版本的参考图片，并先解除单参考或蒙版组合")
				}
				purpose := "image_reference"
				if in.Kind == "video" {
					purpose = "video_reference"
				}
				purposes = append(purposes, purpose)
				for _, id := range choice.MediaAssetIDs {
					belongs := false
					for _, bound := range binding.MediaAssetIDs {
						belongs = belongs || bound == id
					}
					if !belongs || referenceSeen[id] {
						return nil, "", projectInputError("参考图片必须来自所选固定版本，且不能重复")
					}
					referenceSeen[id] = true
					references = append(references, id)
					position := len(references)
					if in.FirstFrameAssetID != "" {
						position++
					}
					if in.LastFrameAssetID != "" {
						position++
					}
					positions = append(positions, position)
				}
			} else if len(choice.MediaAssetIDs) > 0 {
				return nil, "", projectInputError("文字意图模式不能携带参考图片；不会悄悄丢弃图片")
			}
			fields := map[string]string{"description": v.Card.Description}
			if c := v.Card.Character; c != nil {
				fields["appearance"], fields["hair"], fields["costume"], fields["expression"] = c.Appearance, c.Hair, c.Costume, c.Expression
				if c.VoiceNotes != "" {
					pin.OmittedFields = append(pin.OmittedFields, "voice_notes")
				}
			} else if c := v.Card.Style; c != nil {
				fields["palette"], fields["linework"], fields["composition"], fields["lighting"], fields["texture"] = c.Palette, c.Linework, c.Composition, c.Lighting, c.Texture
			}
			directions = append(directions, struct {
				Kind, Name, Mode   string
				Fields             map[string]string
				Constraints        []string
				ReferencePositions []int
			}{asset.Kind, v.Card.Name, choice.Mode, fields, v.Card.ProhibitedChanges, positions})
		case "voice":
			if in.Kind != "speech" || choice.Mode != "native_preset" || len(choice.MediaAssetIDs) > 0 || v.Card.Voice == nil || v.Card.Voice.NativePreset == nil || in.selection == nil || !nativePresetSupported(caps.Provider) {
				return nil, "", projectInputError("此配音链仅支持已配置的原生音色；不支持把意图文字当对白、声音参考或克隆输入")
			}
			preset := v.Card.Voice.NativePreset
			if caps.Provider == "gemini-speech" && !nativeModelPattern.MatchString(preset.Voice) {
				return nil, "", projectInputError("此模型不支持所保存的音色标识，请核对供应商原生音色配置")
			}
			if preset.ModelConfigID != in.selection.ModelID || preset.Provider != caps.Provider || preset.Model != caps.Model || preset.Voice != in.selection.Voice {
				return nil, "", projectInputError("声音卡与本次模型或音色不匹配，请选择匹配模型或移除此声音卡")
			}
			if len(pins) > 0 {
				return nil, "", projectInputError("当前配音协议一次只接收一个原生音色，请分别制作多角色对白")
			}
			purposes = append(purposes, "speech_preset")
			for name, value := range map[string]string{"timbre": v.Card.Voice.Timbre, "performance": v.Card.Voice.Performance, "pace": v.Card.Voice.Pace, "pronunciation": v.Card.Voice.Pronunciation, "description": v.Card.Description} {
				if value != "" {
					pin.OmittedFields = append(pin.OmittedFields, name)
				}
			}
			// Stable field order for durable snapshots and replay comparisons.
			sort.Strings(pin.OmittedFields)
			if len(v.Card.ProhibitedChanges) > 0 {
				pin.OmittedFields = append(pin.OmittedFields, "prohibited_changes")
			}
		default:
			return nil, "", projectInputError("此创作选择仅支持人物、风格或声音卡；其他参考请使用现有素材选择")
		}
		for _, purpose := range purposes {
			if !asset.UseAuthorization.Allows(v.ID, purpose, time.Now().UTC()) {
				return nil, "", providerFailure("library_use_denied")
			}
			pin.Uses = append(pin.Uses, libraryUsePin{Source: media.LibrarySource{AssetID: asset.ID, VersionID: v.ID}, Revision: asset.UseAuthorization.Revision, Purpose: purpose})
		}
		pins = append(pins, pin)
	}
	if len(references) > len(in.ReferenceAssetIDs) {
		if len(references) == 1 && in.FirstFrameAssetID == "" && in.LastFrameAssetID == "" && in.MotionReferenceAssetID == "" {
			in.ReferenceAssetIDs = nil
			in.ReferenceAssetID = references[0]
		} else {
			in.ReferenceAssetIDs = references
		}
	}
	raw := []byte{}
	if len(directions) > 0 {
		raw, _ = json.Marshal(directions)
	}
	if len(raw) > library.ContentBytes {
		return nil, "", projectInputError("本次创作资料超过单请求传输工作集，请拆分镜头或减少本次资料；项目资产数量不受限制")
	}
	if in.Kind == "image" && (caps.Provider == "jimeng-images" || caps.Provider == "minimax-images") {
		style, ratio := "natural", "16:9"
		if in.ImageParameters != nil {
			style, ratio = in.ImageParameters.Style, in.ImageParameters.Ratio
		}
		brief, limit := "Create one image. Style: "+style+". Aspect ratio: "+ratio+". Brief: "+in.Prompt, 800
		if caps.Provider == "minimax-images" {
			brief, limit = "Create one image. Style: "+style+". Brief: "+in.Prompt, 1500
		}
		prompt := appendLibraryInputContext(context.WithValue(ctx, libraryInputContextKey{}, string(raw)), brief)
		if len([]rune(prompt)) > limit {
			return nil, "", providerFailure("provider_input_capacity")
		}
	}
	return pins, string(raw), nil
}

func (s *FileStore) checkLibraryInputPins(ctx context.Context, space string, pins []libraryInputPin) error {
	for _, pin := range pins {
		if len(pin.Uses) == 0 {
			return providerFailure("library_use_denied")
		}
		root := pin.Uses[0].Source
		v, err := s.library.Version(ctx, space, root.AssetID, root.VersionID)
		if err != nil {
			return err
		}
		if v.SHA256 != pin.ContentSHA256 {
			return providerFailure("library_use_denied")
		}
		for _, use := range pin.Uses {
			current, err := s.library.Get(ctx, space, use.Source.AssetID)
			if err != nil {
				return err
			}
			if current.Asset.UseAuthorization == nil || current.Asset.UseAuthorization.Revision != use.Revision || !current.Asset.UseAuthorization.Allows(use.Source.VersionID, use.Purpose, time.Now().UTC()) {
				return providerFailure("library_use_denied")
			}
		}
	}
	return nil
}

func libraryInputSources(pins []libraryInputPin) []media.LibrarySource {
	sources := []media.LibrarySource{}
	for _, pin := range pins {
		for _, use := range pin.Uses {
			sources = append(sources, use.Source)
		}
	}
	return uniqueLibrarySources(sources)
}
