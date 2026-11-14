// Package deployconfig owns the private, restart-applied backend configuration.
package deployconfig

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

//go:embed schema.env
var schema string

var ErrConflict = errors.New("configuration version conflict")
var ErrInput = errors.New("invalid backend configuration")

type Field struct {
	Key        string `json:"key"`
	Group      string `json:"group"`
	Secret     bool   `json:"secret"`
	ReadOnly   bool   `json:"read_only"`
	Value      string `json:"value"`
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
	Pending    bool   `json:"pending"`
}
type Report struct {
	BuildRevision   string  `json:"build_revision,omitempty"`
	Version         string  `json:"version"`
	Fields          []Field `json:"fields"`
	RestartRequired bool    `json:"restart_required"`
}
type Store struct {
	mu     sync.Mutex
	path   string
	active map[string]string
	base   map[string]string
}

var beforeLoad map[string]string

func definitions() []Field {
	var out []Field
	for _, line := range strings.Split(schema, "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		group := "providers"
		switch {
		case strings.HasPrefix(key, "AUTH_") || strings.HasPrefix(key, "OIDC_"):
			group = "authentication"
		case strings.HasPrefix(key, "BLOB_") || strings.HasPrefix(key, "STORAGE_") || strings.HasPrefix(key, "QUEUE_") || key == "DATABASE_URL":
			group = "storage"
		case strings.HasPrefix(key, "MODEL_") || strings.HasPrefix(key, "GATEWAY_") || key == "EXECUTION_POLICY_FILE" || key == "CONTENT_MODERATION_MODE":
			group = "gateway"
		case strings.HasPrefix(key, "REFERENCE_"):
			group = "references"
		case strings.HasPrefix(key, "TRACE_"):
			group = "tracing"
		case key == "HTTP_ADDR" || key == "FRONTEND_ORIGIN" || key == "DATA_DIR" || key == "METRICS_ADDR" || strings.HasPrefix(key, "STUDIO_"):
			group = "service"
		}
		secret := strings.Contains(key, "SECRET") || strings.Contains(key, "API_KEY") || strings.Contains(key, "ACCESS_KEY") || strings.Contains(key, "TOKEN") || strings.Contains(key, "PASSWORD") || strings.Contains(key, "HEADERS") || key == "DATABASE_URL" || key == "MODEL_CONFIG_KEK_BASE64"
		out = append(out, Field{Key: key, Group: group, Secret: secret, ReadOnly: key == "DATA_DIR" || key == "AUTH_MODE" || key == "MODEL_CONFIG_KEK_BASE64" || key == "MODEL_CONFIG_KEK_FILE" || strings.HasPrefix(key, "STUDIO_")})
	}
	return out
}

func Path() string {
	if p := os.Getenv("STUDIO_CONFIG_FILE"); p != "" {
		return p
	}
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "./data"
	}
	return filepath.Join(dir, "backend-config.json")
}

func read(path string) (map[string]string, string, error) {
	if err := nativefs.CheckPrivateFile(path); err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		raw = []byte("{}")
		err = nil
	}
	if err != nil {
		return nil, "", err
	}
	if len(raw) > 256<<10 {
		return nil, "", ErrInput
	}
	values := map[string]string{}
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, "", ErrInput
	}
	allowed := map[string]Field{}
	for _, f := range definitions() {
		allowed[f.Key] = f
	}
	for k, v := range values {
		f, ok := allowed[k]
		if !ok || f.ReadOnly || !validValue(k, v) {
			return nil, "", ErrInput
		}
	}
	hash := sha256.Sum256(raw)
	return values, hex.EncodeToString(hash[:]), nil
}

func validValue(key, value string) bool {
	if len(value) > 8192 || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	if value != "" {
		switch key {
		case "STORAGE_BACKEND":
			return value == "file" || value == "postgres"
		case "QUEUE_BACKEND":
			return value == "inprocess" || value == "postgres"
		case "BLOB_STORE":
			return value == "file" || value == "shared-file" || value == "s3"
		case "AUTH_ALLOWED_ORIGINS":
			_, err := TrustedOrigins("", strings.Split(value, ","))
			return err == nil
		case "DIGITAL_HUMAN_PHOTO_PROVIDER":
			return value == "local" || value == "dashscope-s2v"
		case "DIGITAL_HUMAN_CLOUD_RESOLUTION":
			return value == "480P" || value == "720P"
		case "DIGITAL_HUMAN_PHOTO_PROTOCOL", "LIP_SYNC_PROTOCOL":
			return value == "json-v1" || value == "files-v1"
		case "DIGITAL_HUMAN_PHOTO_TIMEOUT_SECONDS", "LIP_SYNC_TIMEOUT_SECONDS":
			n, err := strconv.ParseInt(value, 10, 32)
			return err == nil && n >= 1
		case "CONTENT_MODERATION_MODE":
			return value == "disabled" || value == "required"
		case "TRACE_MODE":
			return value == "off" || value == "json"
		case "HTTP_ADDR", "METRICS_ADDR":
			_, port, err := net.SplitHostPort(value)
			n, e := strconv.Atoi(port)
			return err == nil && e == nil && n > 0 && n <= 65535
		case "FRONTEND_ORIGIN":
			_, err := CanonicalOrigin(value)
			return err == nil
		case "OIDC_ISSUER", "OIDC_REDIRECT_URL":
			u, err := url.Parse(value)
			return err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil
		}
	}
	if value != "" && (strings.HasSuffix(key, "_ENABLED") || strings.HasSuffix(key, "_SECURE") || key == "AUTH_ALLOW_REGISTRATION" || key == "STORAGE_AUTO_MIGRATE" || key == "MUSIC_EXISTING_ACCOUNT_ACK" || key == "DIGITAL_HUMAN_LICENSE_ACK" || key == "BLOB_S3_ALLOW_LOOPBACK_HTTP") {
		return value == "true" || value == "false"
	}
	return true
}

// Load applies the managed file before any service is initialized. Bootstrap
// location, administrator identity and KEK remain deployment-owned.
func Load() error {
	values, _, err := read(Path())
	if err != nil {
		return err
	}
	beforeLoad = map[string]string{}
	for _, f := range definitions() {
		beforeLoad[f.Key] = os.Getenv(f.Key)
	}
	for k, v := range values {
		if err = os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

func Open() (*Store, error) {
	if _, _, err := read(Path()); err != nil {
		return nil, err
	}
	s := &Store{path: Path(), active: map[string]string{}, base: map[string]string{}}
	for _, f := range definitions() {
		s.active[f.Key] = os.Getenv(f.Key)
	}
	for k, v := range s.active {
		s.base[k] = v
	}
	for k, v := range beforeLoad {
		s.base[k] = v
	}
	return s, nil
}

func (s *Store) Report() (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.report()
}
func (s *Store) report() (Report, error) {
	values, version, err := read(s.path)
	if err != nil {
		return Report{}, err
	}
	out := Report{Version: version, Fields: definitions()}
	for i := range out.Fields {
		f := &out.Fields[i]
		value, ok := values[f.Key]
		f.Source = "environment"
		if ok {
			f.Source = "file"
		} else {
			value = s.base[f.Key]
		}
		// A URL may embed credentials even when its variable name is harmless.
		if u, e := url.Parse(value); e == nil && u.Host != "" && (u.User != nil || u.RawQuery != "") {
			f.Secret = true
		}
		f.Configured = value != ""
		f.Pending = value != s.active[f.Key]
		out.RestartRequired = out.RestartRequired || f.Pending
		if !f.Secret {
			f.Value = value
		}
	}
	return out, nil
}

// Save is a patch: omitted values, including passwords, remain untouched.
// Null removes the file override. Reports never contain sensitive values.
func (s *Store) Save(expected string, changes map[string]*string) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, version, err := read(s.path)
	if err != nil {
		return Report{}, err
	}
	if version != expected {
		return Report{}, ErrConflict
	}
	allowed := map[string]Field{}
	for _, f := range definitions() {
		allowed[f.Key] = f
	}
	for k, v := range changes {
		f, ok := allowed[k]
		if !ok || f.ReadOnly || v != nil && !validValue(k, *v) {
			return Report{}, ErrInput
		}
		if v == nil {
			delete(values, k)
		} else {
			values[k] = *v
		}
	}
	if err = nativefs.EnsurePrivateDirectory(filepath.Dir(s.path)); err != nil {
		return Report{}, err
	}
	raw, err := json.MarshalIndent(values, "", "  ")
	if len(raw) > 256<<10 {
		return Report{}, ErrInput
	}
	if err != nil {
		return Report{}, err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".backend-config-*")
	if err != nil {
		return Report{}, err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = nativefs.SyncFile(f)
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = nativefs.Rename(name, s.path)
	}
	if err == nil {
		err = syncDirectory(filepath.Dir(s.path))
	}
	if err != nil {
		return Report{}, err
	}
	return s.report()
}

// A synced temporary file alone does not make its directory entry durable.
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return nativefs.SyncFile(dir)
}

func ReservedEmails() []string {
	out := strings.Split(strings.ToLower(os.Getenv("STUDIO_ADMIN_EMAILS")), ",")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	sort.Strings(out)
	return out
}
func IsReservedEmail(email string, admins []string) bool {
	for _, v := range admins {
		if v != "" && strings.EqualFold(v, email) {
			return true
		}
	}
	return false
}
