package application

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Gateway credentials belong to the connection, never to individual models.
type AIGateway struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	BaseURL        string    `json:"base_url"`
	Enabled        bool      `json:"enabled"`
	TimeoutSeconds int       `json:"timeout_seconds"`
	Version        int64     `json:"version"`
	SecretStatus   string    `json:"secret_status"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type storedGateway struct {
	Gateway    AIGateway `json:"gateway"`
	Ciphertext string    `json:"ciphertext,omitempty"`
}
type GatewayModel struct {
	ID              string    `json:"id"`
	GatewayID       string    `json:"gateway_id"`
	Name            string    `json:"name"`
	Model           string    `json:"model"`
	Capabilities    []string  `json:"capabilities"`
	Enabled         bool      `json:"enabled"`
	Version         int64     `json:"version"`
	MaxOutputTokens int       `json:"max_output_tokens"`
	JSONOutput      bool      `json:"json_output"`
	VideoSeconds    int       `json:"video_seconds"`
	VideoSize       string    `json:"video_size"`
	Currency        string    `json:"currency"`
	MaximumMinor    int64     `json:"maximum_minor"`
	PriceSource     string    `json:"price_source"`
	UpdatedAt       time.Time `json:"updated_at"`
}
type FeatureDefinition struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Capability string `json:"capability"`
}
type FeatureBinding struct {
	FeatureKey    string    `json:"feature_key"`
	ModelConfigID string    `json:"model_config_id"`
	Enabled       bool      `json:"enabled"`
	AllowOverride bool      `json:"allow_override"`
	Voice         string    `json:"voice,omitempty"`
	Version       int64     `json:"version"`
	UpdatedBy     string    `json:"updated_by"`
	UpdatedAt     time.Time `json:"updated_at"`
}
type ModelManagementReport struct {
	Version                int64               `json:"version"`
	Gateways               []AIGateway         `json:"gateways"`
	Models                 []GatewayModel      `json:"models"`
	Bindings               []FeatureBinding    `json:"bindings"`
	Features               []FeatureDefinition `json:"features"`
	SecretStorageAvailable bool                `json:"secret_storage_available"`
	GovernanceReady        bool                `json:"governance_ready"`
	LegacyModels           int                 `json:"legacy_models"`
}

func modelFeatures() []FeatureDefinition {
	return []FeatureDefinition{
		{"brief_analysis", "创意分析", "text"}, {"script_generation", "剧本生成", "text"}, {"storyboard_planning", "分镜规划", "text"},
		{"image_generation", "图像创作", "image"}, {"character_image", "角色参考图", "image"}, {"shot_image", "镜头图片", "image"},
		{"image_edit", "图片编辑", "image_edit"}, {"shot_video", "镜头视频", "video"}, {"speech_synthesis", "配音", "speech"},
		{"speech_transcription", "语音转写", "asr"}, {"subtitle_alignment", "字幕识别", "asr_timestamps"},
		{"vision_analysis", "图片分析", "vision"}, {"consistency_check", "跨镜头一致性检查", "vision"},
	}
}
func featureDefinition(key string) (FeatureDefinition, bool) {
	for _, f := range modelFeatures() {
		if f.Key == key {
			return f, true
		}
	}
	return FeatureDefinition{}, false
}
func modelHasCapability(m GatewayModel, capability string) bool {
	for _, c := range m.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}
func validGatewayModelID(s string) bool {
	if len(s) < 1 || len(s) > 200 || strings.TrimSpace(s) != s {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 || strings.ContainsRune("\\\"?#", c) {
			return false
		}
	}
	return true
}
func normalizeGatewayURL(raw string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || len(raw) > 2048 || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Scheme != "https" && u.Scheme != "http") || strings.ContainsAny(u.Path, "\\\x00") {
		return "", errModelInput
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	for _, p := range strings.Split(u.Path, "/") {
		if p == "." || p == ".." {
			return "", errModelInput
		}
	}
	if u.Path == "" {
		u.Path = "/v1"
	}
	return u.String(), nil
}
func gatewayAAD(tenant string, g AIGateway) []byte {
	return []byte("gateway\x00" + tenant + "\x00" + g.ID + "\x00" + g.BaseURL)
}
func gatewaySelectionCurrent(d modelSnapshot, q RuntimeSelection) bool {
	t := d.Tenants[q.TenantID]
	if q.GatewayID == "" {
		return t.Version == q.Version
	}
	m, ok := t.Models[q.ModelID]
	g, exists := t.Gateways[q.GatewayID]
	return ok && exists && !gatewayCapabilityRetired(g.Gateway, q.Capability) && m.Enabled && g.Gateway.Enabled && m.GatewayID == q.GatewayID && m.Version == q.Version && g.Gateway.Version == q.GatewayVersion && g.Gateway.SecretStatus == "encrypted" && supportsRuntimeCapability(m, q.Capability)
}

// A configurable compatible gateway must not revive a known retired official
// endpoint. Private implementations of this protocol retain their own lifecycle.
func gatewayCapabilityRetired(g AIGateway, capability string) bool {
	u, err := url.Parse(g.BaseURL)
	return err == nil && capability == "video" && strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), "api.openai.com")
}

func supportsRuntimeCapability(m GatewayModel, capability string) bool {
	return modelHasCapability(m, capability) || capability == "image" && modelHasCapability(m, "image_edit") || capability == "asr" && modelHasCapability(m, "asr_timestamps")
}
func gatewayModelEntry(m GatewayModel, g AIGateway, capability string) ModelConfigEntry {
	provider := "gateway-" + capability
	if capability == "image" {
		provider = "gateway-images"
	}
	return ModelConfigEntry{ID: m.ID, Provider: provider, Model: m.Model, Capability: capability, Region: "gateway", Endpoint: g.BaseURL, Protocol: "openai-compatible", Enabled: m.Enabled && g.Enabled, Lifecycle: "active", SecretStatus: g.SecretStatus}
}
func validateGatewaySnapshot(d modelSnapshot) error {
	for tenant, t := range d.Tenants {
		for id, g := range t.Gateways {
			u, e := normalizeGatewayURL(g.Gateway.BaseURL)
			if tenant == "" || id != g.Gateway.ID || id == "" || e != nil || u != g.Gateway.BaseURL || g.Gateway.Version < 1 || g.Gateway.TimeoutSeconds < 5 || g.Gateway.TimeoutSeconds > 600 {
				return errModelInput
			}
			if g.Gateway.SecretStatus == "encrypted" {
				raw, e := base64.StdEncoding.DecodeString(g.Ciphertext)
				if e != nil || len(raw) < 28 {
					return errModelInput
				}
			} else if (g.Gateway.SecretStatus != "not_configured" && g.Gateway.SecretStatus != "revoked") || g.Ciphertext != "" {
				return errModelInput
			}
		}
		for id, m := range t.Models {
			if id != m.ID || m.Version < 1 || validateGatewayModel(m) != nil || t.Gateways[m.GatewayID].Gateway.ID == "" {
				return errModelInput
			}
		}
		for key, b := range t.Bindings {
			f, ok := featureDefinition(key)
			m := t.Models[b.ModelConfigID]
			if !ok || key != b.FeatureKey || b.Version < 1 || !modelHasCapability(m, f.Capability) {
				return errModelInput
			}
		}
	}
	return nil
}
func validateGatewayModel(m GatewayModel) error {
	if m.Currency == "" {
		return errModelInput
	}
	if !validGatewayModelID(m.Model) || utf8.RuneCountInString(m.Name) < 1 || utf8.RuneCountInString(m.Name) > 80 || len(m.Capabilities) == 0 || len(m.Capabilities) > 8 || m.MaxOutputTokens < 1 || m.MaxOutputTokens > 6000 || m.MaximumMinor < 0 || m.MaximumMinor > 9007199254740991 || len(m.PriceSource) > 240 {
		return errModelInput
	}
	if _, e := CurrencyMinorUnits(m.Currency); e != nil {
		return errModelInput
	}
	allowed := map[string]bool{"text": true, "vision": true, "image": true, "image_edit": true, "video": true, "speech": true, "asr": true, "asr_timestamps": true}
	seen := map[string]bool{}
	for _, c := range m.Capabilities {
		if !allowed[c] || seen[c] {
			return errModelInput
		}
		seen[c] = true
	}
	if modelHasCapability(m, "video") && ((m.VideoSeconds != 4 && m.VideoSeconds != 8 && m.VideoSeconds != 12) || (m.VideoSize != "1280x720" && m.VideoSize != "720x1280")) {
		return errModelInput
	}
	return nil
}
func (s *ModelConfigStore) cloneGatewayData() modelSnapshot {
	raw, _ := json.Marshal(s.data)
	var d modelSnapshot
	_ = json.Unmarshal(raw, &d)
	return d
}
func gatewayTenant(d modelSnapshot, tenant string) modelTenant {
	t := d.Tenants[tenant]
	if t.Entries == nil {
		t.Entries = map[string]storedModel{}
	}
	if t.Gateways == nil {
		t.Gateways = map[string]storedGateway{}
	}
	if t.Models == nil {
		t.Models = map[string]GatewayModel{}
	}
	if t.Bindings == nil {
		t.Bindings = map[string]FeatureBinding{}
	}
	return t
}
func (s *ModelConfigStore) managementReport(tenant string) (ModelManagementReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil || (s.pg != nil && s.pg.err != nil) {
		return ModelManagementReport{}, errRuntimeUnavailable
	}
	return s.managementReportLocked(tenant), nil
}
func (s *ModelConfigStore) managementReportLocked(tenant string) ModelManagementReport {
	t := s.data.Tenants[tenant]
	out := ModelManagementReport{Version: t.Version, Gateways: []AIGateway{}, Models: []GatewayModel{}, Bindings: []FeatureBinding{}, Features: modelFeatures(), SecretStorageAvailable: s.aead != nil, GovernanceReady: s.runtimeReady != nil && s.runtimeReady(), LegacyModels: len(t.Entries)}
	for _, g := range t.Gateways {
		out.Gateways = append(out.Gateways, g.Gateway)
	}
	for _, m := range t.Models {
		out.Models = append(out.Models, m)
	}
	for _, b := range t.Bindings {
		out.Bindings = append(out.Bindings, b)
	}
	sort.Slice(out.Gateways, func(i, j int) bool { return out.Gateways[i].ID < out.Gateways[j].ID })
	sort.Slice(out.Models, func(i, j int) bool { return out.Models[i].ID < out.Models[j].ID })
	sort.Slice(out.Bindings, func(i, j int) bool { return out.Bindings[i].FeatureKey < out.Bindings[j].FeatureKey })
	return out
}
func (s *ModelConfigStore) saveGateway(tenant string, expected int64, in AIGateway, credential ModelCredentialInput) (ModelManagementReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return ModelManagementReport{}, s.failed
	}
	d := s.cloneGatewayData()
	t := gatewayTenant(d, tenant)
	old, exists := t.Gateways[in.ID]
	if in.ID != "" && !exists {
		return ModelManagementReport{}, ErrNotFound
	}
	if old.Gateway.Version != expected {
		return ModelManagementReport{}, errModelConflict
	}
	u, e := normalizeGatewayURL(in.BaseURL)
	if e != nil || utf8.RuneCountInString(strings.TrimSpace(in.Name)) < 1 || utf8.RuneCountInString(in.Name) > 80 || in.TimeoutSeconds < 5 || in.TimeoutSeconds > 600 || len(t.Gateways) >= 50 && !exists {
		return ModelManagementReport{}, errModelInput
	}
	if in.ID == "" {
		in.ID = authRandom(16)
	}
	in.BaseURL = u
	in.Name = strings.TrimSpace(in.Name)
	in.Version = expected + 1
	in.UpdatedAt = time.Now().UTC()
	in.SecretStatus = old.Gateway.SecretStatus
	if in.SecretStatus == "" {
		in.SecretStatus = "not_configured"
	}
	next := storedGateway{Gateway: in, Ciphertext: old.Ciphertext}
	switch credential.Action {
	case "keep":
		if credential.Secret != "" || credential.Reference != "" || (exists && old.Gateway.BaseURL != u && old.Ciphertext != "") {
			return ModelManagementReport{}, errModelInput
		}
	case "set":
		if s.aead == nil {
			return ModelManagementReport{}, errModelKey
		}
		if !validCredential(credential.Secret) || len(credential.Secret) > 4096 || credential.Reference != "" {
			return ModelManagementReport{}, errModelInput
		}
		nonce := make([]byte, s.aead.NonceSize())
		if _, e = rand.Read(nonce); e != nil {
			return ModelManagementReport{}, e
		}
		next.Ciphertext = base64.StdEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(credential.Secret), gatewayAAD(tenant, in)))
		next.Gateway.SecretStatus = "encrypted"
	case "revoke":
		if credential.Secret != "" || credential.Reference != "" {
			return ModelManagementReport{}, errModelInput
		}
		next.Ciphertext = ""
		next.Gateway.SecretStatus = "revoked"
		next.Gateway.Enabled = false
	default:
		return ModelManagementReport{}, errModelInput
	}
	if next.Gateway.SecretStatus == "revoked" {
		next.Gateway.Enabled = false
	}
	t.Gateways[in.ID] = next
	t.Version++
	d.Tenants[tenant] = t
	if e = s.commit(d); e != nil {
		return ModelManagementReport{}, e
	}
	return s.managementReportLocked(tenant), nil
}
func (s *ModelConfigStore) saveGatewayModel(tenant string, expected int64, in GatewayModel) (ModelManagementReport, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.PriceSource = strings.TrimSpace(in.PriceSource)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return ModelManagementReport{}, s.failed
	}
	d := s.cloneGatewayData()
	t := gatewayTenant(d, tenant)
	old, exists := t.Models[in.ID]
	if in.ID != "" && !exists {
		return ModelManagementReport{}, ErrNotFound
	}
	if old.Version != expected {
		return ModelManagementReport{}, errModelConflict
	}
	if validateGatewayModel(in) != nil || t.Gateways[in.GatewayID].Gateway.ID == "" || exists && old.GatewayID != in.GatewayID || len(t.Models) >= 200 && !exists {
		return ModelManagementReport{}, errModelInput
	}
	for _, b := range t.Bindings {
		if b.ModelConfigID == in.ID {
			f, _ := featureDefinition(b.FeatureKey)
			if !modelHasCapability(in, f.Capability) {
				return ModelManagementReport{}, errors.New("model capability is referenced by a feature")
			}
		}
	}
	if in.ID == "" {
		in.ID = authRandom(16)
	}
	in.Version = expected + 1
	in.UpdatedAt = time.Now().UTC()
	t.Models[in.ID] = in
	t.Version++
	d.Tenants[tenant] = t
	if e := s.commit(d); e != nil {
		return ModelManagementReport{}, e
	}
	return s.managementReportLocked(tenant), nil
}
func (s *ModelConfigStore) saveFeatureBinding(tenant, actor string, expected int64, in FeatureBinding) (ModelManagementReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return ModelManagementReport{}, s.failed
	}
	d := s.cloneGatewayData()
	t := gatewayTenant(d, tenant)
	old := t.Bindings[in.FeatureKey]
	if old.Version != expected {
		return ModelManagementReport{}, errModelConflict
	}
	f, ok := featureDefinition(in.FeatureKey)
	m, exists := t.Models[in.ModelConfigID]
	g := t.Gateways[m.GatewayID].Gateway
	if !ok || !exists || !modelHasCapability(m, f.Capability) || in.Enabled && (!m.Enabled || !g.Enabled) || len(in.Voice) > 128 || in.Voice != "" && (f.Capability != "speech" || !configToken.MatchString(in.Voice)) {
		return ModelManagementReport{}, errModelInput
	}
	in.Version = expected + 1
	in.UpdatedBy = actor
	in.UpdatedAt = time.Now().UTC()
	t.Bindings[in.FeatureKey] = in
	t.Version++
	d.Tenants[tenant] = t
	if e := s.commit(d); e != nil {
		return ModelManagementReport{}, e
	}
	return s.managementReportLocked(tenant), nil
}
