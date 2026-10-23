package studio

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ModelAdapter struct {
	Provider       string   `json:"provider"`
	Protocol       string   `json:"protocol"`
	Capabilities   []string `json:"capabilities"`
	Regions        []string `json:"regions"`
	Endpoint       string   `json:"endpoint"`
	CredentialRefs []string `json:"credential_refs"`
}

func ModelAdapters() []ModelAdapter {
	out := []ModelAdapter{}
	add := func(provider, protocol, region, endpoint, reference string, capabilities ...string) {
		out = append(out, ModelAdapter{provider, protocol, capabilities, []string{region}, endpoint, strings.Split(reference, ",")})
	}
	for _, v := range []struct{ prefix, endpoint, ref string }{{"openai", "https://api.openai.com/v1", "OPENAI_API_KEY"}, {"gemini", "https://generativelanguage.googleapis.com/v1beta", "GEMINI_API_KEY"}, {"ark", "https://ark.cn-beijing.volces.com/api/v3", "ARK_API_KEY"}, {"minimax", "https://api.minimax.io/v1", "MINIMAX_API_KEY"}} {
		region := "global"
		if v.prefix == "ark" {
			region = "cn-beijing"
		}
		add(v.prefix+"-text", "text", region, v.endpoint, v.ref, "text", "vision")
		add(v.prefix+"-images", "image", region, v.endpoint, v.ref, "image")
		if v.prefix != "openai" {
			add(v.prefix+"-video", "video", region, v.endpoint, v.ref, "video")
		}
		if v.prefix != "ark" {
			add(v.prefix+"-speech", "speech", region, v.endpoint, v.ref, "speech")
		}
	}
	add("anthropic-text", "messages", "global", "https://api.anthropic.com/v1", "ANTHROPIC_API_KEY", "text", "vision")
	add("gemini-interactions-images", "interactions", "global", "https://generativelanguage.googleapis.com/v1beta", "GEMINI_API_KEY", "image")
	add("jimeng-images", "volcengine-signed-cv", "cn-north-1", "https://visual.volcengineapi.com", "JIMENG_ACCESS_KEY,JIMENG_SECRET_KEY", "image")
	add("bytedance-speech", "streaming-tts", "cn", "https://openspeech.bytedance.com", "BYTEDANCE_SPEECH_API_KEY", "speech")
	add("minimax-music", "music-generation", "global", "https://api.minimax.io/v1", "MINIMAX_API_KEY", "music")
	add("openai-asr", "audio-transcriptions", "global", "https://api.openai.com/v1", "OPENAI_API_KEY", "asr")
	protocols := map[string]string{
		"openai-text": "chat-completions", "gemini-text": "generateContent", "ark-text": "openai-compatible-chat", "minimax-text": "openai-compatible-chat",
		"openai-images": "images-generations/edits", "gemini-images": "generateContent", "ark-images": "images-generations", "minimax-images": "image-generation",
		"gemini-video": "interactions", "ark-video": "contents-generations", "minimax-video": "video-generation-v2",
		"openai-speech": "audio-speech", "gemini-speech": "interactions", "minimax-speech": "t2a",
	}
	for i := range out {
		if protocol, ok := protocols[out[i].Provider]; ok {
			out[i].Protocol = protocol
		}
	}
	return out
}

type ModelConfigEntry struct {
	ID              string    `json:"id"`
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	Capability      string    `json:"capability"`
	Region          string    `json:"region"`
	Endpoint        string    `json:"endpoint"`
	Protocol        string    `json:"protocol"`
	Enabled         bool      `json:"enabled"`
	IsDefault       bool      `json:"is_default"`
	Lifecycle       string    `json:"lifecycle"`
	SecretStatus    string    `json:"secret_status"`
	SecretMask      string    `json:"secret_mask"`
	SecretReference string    `json:"secret_reference"`
	UpdatedAt       time.Time `json:"updated_at"`
}
type storedModel struct {
	Entry      ModelConfigEntry `json:"entry"`
	Ciphertext string           `json:"ciphertext,omitempty"`
}
type modelTenant struct {
	Version int64                  `json:"version"`
	Entries map[string]storedModel `json:"entries"`
}
type modelSnapshot struct {
	Version int                    `json:"version"`
	Tenants map[string]modelTenant `json:"tenants"`
}
type ModelConfigStore struct {
	mu     sync.Mutex
	path   string
	data   modelSnapshot
	aead   cipher.AEAD
	failed error
}
type ModelConfigInput struct {
	ID         string `json:"id"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Capability string `json:"capability"`
	Region     string `json:"region"`
	Enabled    bool   `json:"enabled"`
	IsDefault  bool   `json:"is_default"`
	Lifecycle  string `json:"lifecycle"`
}
type ModelCredentialInput struct {
	Action    string `json:"action"`
	Secret    string `json:"secret,omitempty"`
	Reference string `json:"reference,omitempty"`
}
type ModelConfigReport struct {
	Version                int64              `json:"version"`
	Entries                []ModelConfigEntry `json:"entries"`
	Adapters               []ModelAdapter     `json:"adapters"`
	SecretStorageAvailable bool               `json:"secret_storage_available"`
	LiveEnabled            bool               `json:"live_enabled"`
	RuntimeApplied         bool               `json:"runtime_applied"`
	Scope                  string             `json:"scope"`
}

var errModelConflict = errors.New("model configuration version conflict")
var errModelInput = errors.New("invalid model configuration")
var errModelKey = errors.New("credential encryption key unavailable")

func OpenModelConfigStore(dir string, key []byte) (*ModelConfigStore, error) {
	s := &ModelConfigStore{path: filepath.Join(dir, "model-config.json"), data: modelSnapshot{Version: 1, Tenants: map[string]modelTenant{}}}
	if len(key) > 0 {
		if len(key) != 32 {
			return nil, errModelKey
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		s.aead, err = cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
	}
	raw, err := os.ReadFile(s.path)
	if err == nil {
		var d modelSnapshot
		err = json.Unmarshal(raw, &d)
		s.data = d
	} else if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return nil, errors.New("invalid model configuration state")
	}
	if s.data.Version != 1 || s.data.Tenants == nil {
		return nil, errors.New("incomplete model configuration state")
	}
	for tenant, t := range s.data.Tenants {
		if tenant == "" || t.Version < 1 || t.Entries == nil {
			return nil, errors.New("invalid tenant configuration")
		}
		for id, m := range t.Entries {
			if id != m.Entry.ID {
				return nil, errors.New("invalid model identity")
			}
			adapter, validationErr := validateModelInput(ModelConfigInput{ID: id, Provider: m.Entry.Provider, Model: m.Entry.Model, Capability: m.Entry.Capability, Region: m.Entry.Region, Enabled: m.Entry.Enabled, IsDefault: m.Entry.IsDefault, Lifecycle: m.Entry.Lifecycle})
			if validationErr != nil || adapter.Endpoint != m.Entry.Endpoint || adapter.Protocol != m.Entry.Protocol {
				return nil, errModelInput
			}
			switch m.Entry.SecretStatus {
			case "encrypted":
				cipherBytes, decodeErr := base64.StdEncoding.DecodeString(m.Ciphertext)
				if decodeErr != nil || len(cipherBytes) < 28 || m.Entry.SecretReference != "" {
					return nil, errModelInput
				}
			case "not_configured", "revoked":
				if m.Ciphertext != "" || m.Entry.SecretReference != "" {
					return nil, errModelInput
				}
			case "reference_unverified":
				allowed := false
				for _, ref := range adapter.CredentialRefs {
					allowed = allowed || ref == m.Entry.SecretReference
				}
				if m.Ciphertext != "" || !allowed {
					return nil, errModelInput
				}
			default:
				return nil, errModelInput
			}
		}
	}
	return s, nil
}
func modelKeyFromEnv() ([]byte, error) {
	raw := os.Getenv("MODEL_CONFIG_KEK_BASE64")
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errModelKey
	}
	return key, nil
}
func validateModelInput(in ModelConfigInput) (ModelAdapter, error) {
	if !configToken.MatchString(in.Model) || len(in.Model) > 200 || (in.Lifecycle != "active" && in.Lifecycle != "deprecated" && in.Lifecycle != "disabled") || (in.IsDefault && (!in.Enabled || in.Lifecycle != "active")) || (in.Enabled && in.Lifecycle == "disabled") {
		return ModelAdapter{}, errModelInput
	}
	for _, a := range ModelAdapters() {
		if a.Provider == in.Provider {
			validCap, validRegion := false, false
			for _, c := range a.Capabilities {
				validCap = validCap || c == in.Capability
			}
			for _, r := range a.Regions {
				validRegion = validRegion || r == in.Region
			}
			if validCap && validRegion {
				return a, nil
			}
		}
	}
	return ModelAdapter{}, errModelInput
}
func (s *ModelConfigStore) report(tenant string) (ModelConfigReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reportLocked(tenant)
}
func (s *ModelConfigStore) reportLocked(tenant string) (ModelConfigReport, error) {
	if s.failed != nil {
		return ModelConfigReport{}, s.failed
	}
	t := s.data.Tenants[tenant]
	out := ModelConfigReport{Version: t.Version, Entries: []ModelConfigEntry{}, Adapters: ModelAdapters(), SecretStorageAvailable: s.aead != nil, Scope: "tenant"}
	for _, m := range t.Entries {
		e := m.Entry
		e.SecretMask = ""
		if e.SecretStatus == "encrypted" {
			e.SecretMask = "••••••••"
		}
		out.Entries = append(out.Entries, e)
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].ID < out.Entries[j].ID })
	return out, nil
}
func (s *ModelConfigStore) commit(d modelSnapshot) error {
	if s.failed != nil {
		return s.failed
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".model-config-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err == nil {
		err = closed
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err == nil {
		err = dir.Sync()
		_ = dir.Close()
	}
	if err != nil {
		s.failed = err
		return err
	}
	s.data = d
	return nil
}
func (s *ModelConfigStore) update(tenant string, expected int64, in ModelConfigInput, credential ModelCredentialInput) (ModelConfigReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return ModelConfigReport{}, s.failed
	}
	adapter, err := validateModelInput(in)
	if err != nil || tenant == "" {
		return ModelConfigReport{}, errModelInput
	}
	current := s.data.Tenants[tenant]
	if current.Version != expected {
		return ModelConfigReport{}, errModelConflict
	}
	if len(current.Entries) >= 100 && in.ID == "" {
		return ModelConfigReport{}, errModelInput
	}
	var old storedModel
	if in.ID != "" {
		var ok bool
		old, ok = current.Entries[in.ID]
		if !ok {
			return ModelConfigReport{}, ErrNotFound
		}
	} else {
		in.ID = authRandom(16)
	}
	next := storedModel{Entry: ModelConfigEntry{ID: in.ID, Provider: in.Provider, Model: in.Model, Capability: in.Capability, Region: in.Region, Endpoint: adapter.Endpoint, Protocol: adapter.Protocol, Enabled: in.Enabled, IsDefault: in.IsDefault, Lifecycle: in.Lifecycle, SecretStatus: "not_configured", UpdatedAt: time.Now().UTC()}}
	switch credential.Action {
	case "keep":
		if credential.Secret != "" || credential.Reference != "" || (old.Entry.ID != "" && old.Entry.Provider != in.Provider && (old.Ciphertext != "" || old.Entry.SecretReference != "")) {
			return ModelConfigReport{}, errModelInput
		}
		if old.Entry.ID != "" {
			next.Ciphertext = old.Ciphertext
			next.Entry.SecretStatus = old.Entry.SecretStatus
			next.Entry.SecretReference = old.Entry.SecretReference
		}
	case "set":
		if s.aead == nil {
			return ModelConfigReport{}, errModelKey
		}
		if credential.Reference != "" || len(credential.Secret) > 4096 || !validCredential(credential.Secret) {
			return ModelConfigReport{}, errModelInput
		}
		nonce := make([]byte, s.aead.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			return ModelConfigReport{}, err
		}
		ciphertext := s.aead.Seal(nonce, nonce, []byte(credential.Secret), []byte(tenant+"\x00"+in.ID+"\x00"+in.Provider))
		next.Ciphertext = base64.StdEncoding.EncodeToString(ciphertext)
		next.Entry.SecretStatus = "encrypted"
	case "reference":
		valid := false
		for _, ref := range adapter.CredentialRefs {
			valid = valid || ref == credential.Reference
		}
		if !valid || credential.Secret != "" {
			return ModelConfigReport{}, errModelInput
		}
		next.Entry.SecretReference = credential.Reference
		next.Entry.SecretStatus = "reference_unverified"
	case "revoke":
		if credential.Secret != "" || credential.Reference != "" {
			return ModelConfigReport{}, errModelInput
		}
		next.Entry.SecretStatus = "revoked"
		next.Entry.Enabled = false
		next.Entry.IsDefault = false
	default:
		return ModelConfigReport{}, errModelInput
	}
	if next.Entry.SecretStatus == "revoked" {
		next.Entry.Enabled = false
		next.Entry.IsDefault = false
	}
	raw, _ := json.Marshal(s.data)
	var d modelSnapshot
	_ = json.Unmarshal(raw, &d)
	t := d.Tenants[tenant]
	if t.Entries == nil {
		t.Entries = map[string]storedModel{}
	}
	if next.Entry.IsDefault {
		for id, m := range t.Entries {
			if m.Entry.Capability == in.Capability {
				m.Entry.IsDefault = false
				t.Entries[id] = m
			}
		}
	}
	t.Entries[in.ID] = next
	t.Version++
	d.Tenants[tenant] = t
	if err = s.commit(d); err != nil {
		return ModelConfigReport{}, err
	}
	return s.reportLocked(tenant)
}
func (s *ModelConfigStore) validate(tenant, id string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return nil, s.failed
	}
	m, ok := s.data.Tenants[tenant].Entries[id]
	if !ok {
		return nil, ErrNotFound
	}
	status := m.Entry.SecretStatus
	if status == "encrypted" {
		if s.aead == nil {
			status = "encryption_key_unavailable"
		} else {
			raw, err := base64.StdEncoding.DecodeString(m.Ciphertext)
			if err != nil || len(raw) < s.aead.NonceSize() {
				status = "credential_unreadable"
			} else {
				plain, err := s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], []byte(tenant+"\x00"+id+"\x00"+m.Entry.Provider))
				if err != nil {
					status = "credential_unreadable"
				}
				for i := range plain {
					plain[i] = 0
				}
			}
		}
	}
	return map[string]any{"configuration_valid": true, "credential_status": status, "remote_verified": false, "live_enabled": false, "runtime_applied": false, "model_verified": false}, nil
}
