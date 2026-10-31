package deployconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("STUDIO_CONFIG_FILE", filepath.Join(t.TempDir(), "backend-config.json"))
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func pointer(v string) *string { return &v }
func findField(t *testing.T, r Report, key string) Field {
	t.Helper()
	for _, f := range r.Fields {
		if f.Key == key {
			return f
		}
	}
	t.Fatal("missing field", key)
	return Field{}
}
func TestPatchRedactionCASAndRestore(t *testing.T) {
	t.Setenv("FRONTEND_ORIGIN", "https://environment.example")
	s := configStore(t)
	initial, err := s.Report()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.Save(initial.Version, map[string]*string{"DATABASE_URL": pointer("postgres://synthetic:password@localhost/db"), "FRONTEND_ORIGIN": pointer("https://configured.example")})
	if err != nil || !saved.RestartRequired {
		t.Fatal("save failed", err)
	}
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), "synthetic:password") {
		t.Fatal("report leaked password")
	}
	if !findField(t, saved, "DATABASE_URL").Configured || findField(t, saved, "DATABASE_URL").Value != "" {
		t.Fatal("incorrect secret report")
	}
	if os.Getenv("FRONTEND_ORIGIN") != "https://environment.example" {
		t.Fatal("save changed running process")
	}
	if _, err = s.Save(initial.Version, map[string]*string{}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale write accepted", err)
	}
	kept, err := s.Save(saved.Version, map[string]*string{"FRONTEND_ORIGIN": nil})
	if err != nil {
		t.Fatal(err)
	}
	if f := findField(t, kept, "FRONTEND_ORIGIN"); f.Source != "environment" || f.Value != "https://environment.example" || f.Pending {
		t.Fatal("restore did not use original environment", f)
	}
	disk, _, err := read(s.path)
	if err != nil || disk["DATABASE_URL"] != "postgres://synthetic:password@localhost/db" {
		t.Fatal("omitted credential lost", err)
	}
	cleared, err := s.Save(kept.Version, map[string]*string{"DATABASE_URL": pointer("")})
	if err != nil || findField(t, cleared, "DATABASE_URL").Configured {
		t.Fatal("explicit clear failed", err)
	}
}
func TestRejectsProtectedUnknownAndMalformedConfig(t *testing.T) {
	s := configStore(t)
	initial, _ := s.Report()
	for _, key := range []string{"DATA_DIR", "AUTH_MODE", "MODEL_CONFIG_KEK_BASE64", "STUDIO_ADMIN_EMAILS", "UNKNOWN"} {
		if _, err := s.Save(initial.Version, map[string]*string{key: pointer("x")}); !errors.Is(err, ErrInput) {
			t.Fatal("protected key accepted", key, err)
		}
	}
	for key, value := range map[string]string{"FRONTEND_ORIGIN": "bad\nvalue", "AUTH_COOKIE_SECURE": "yes"} {
		if _, err := s.Save(initial.Version, map[string]*string{key: pointer(value)}); !errors.Is(err, ErrInput) {
			t.Fatal("bad value accepted", key, err)
		}
	}
	if err := os.WriteFile(s.path, []byte(`{"DATA_DIR":"elsewhere"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Report(); !errors.Is(err, ErrInput) {
		t.Fatal("malformed config not rejected", err)
	}
}
func TestDeploymentAdminIsExplicit(t *testing.T) {
	t.Setenv("STUDIO_ADMIN_EMAILS", "operator@example.test, Second@example.test ")
	if !IsAdmin("SECOND@example.test", Admins()) || IsAdmin("owner@example.test", Admins()) || IsAdmin("", []string{""}) {
		t.Fatal("administrator boundary failed")
	}
}

func TestStartupLoadAndRemovingOverride(t *testing.T) {
	old := beforeLoad
	t.Cleanup(func() { beforeLoad = old })
	t.Setenv("FRONTEND_ORIGIN", "https://environment.example")
	s := configStore(t)
	initial, _ := s.Report()
	if _, err := s.Save(initial.Version, map[string]*string{"FRONTEND_ORIGIN": pointer("https://file.example")}); err != nil {
		t.Fatal(err)
	}
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FRONTEND_ORIGIN") != "https://file.example" {
		t.Fatal("startup did not apply file")
	}
	restarted, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	active, _ := restarted.Report()
	if active.RestartRequired {
		t.Fatal("restart still pending after load")
	}
	removed, err := restarted.Save(active.Version, map[string]*string{"FRONTEND_ORIGIN": nil})
	if err != nil {
		t.Fatal(err)
	}
	f := findField(t, removed, "FRONTEND_ORIGIN")
	if f.Value != "https://environment.example" || !f.Pending || !removed.RestartRequired {
		t.Fatal("removing override lost environment baseline", f)
	}
}
