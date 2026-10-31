package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func syntheticModel() ModelConfigInput {
	return ModelConfigInput{Provider: "openai-text", Model: "synthetic-model", Capability: "text", Region: "global", Enabled: true, IsDefault: true, Lifecycle: "active"}
}
func TestModelConfigEncryptionIsolationRevocationAndCAS(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{42}, 32)
	s, err := OpenModelConfigStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	secret := "synthetic-secret-DO-NOT-LOG"
	in := syntheticModel()
	report, err := s.update("tenant-a", 0, in, ModelCredentialInput{Action: "set", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	id := report.Entries[0].ID
	in.ID = id
	raw, _ := json.Marshal(report)
	if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte("ciphertext")) {
		t.Fatal("credential leaked in response")
	}
	disk, err := os.ReadFile(filepath.Join(dir, "model-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(disk, []byte(secret)) {
		t.Fatal("plaintext credential on disk")
	}
	if report.Entries[0].SecretMask != "••••••••" || report.Version != 1 || report.RuntimeApplied || report.LiveEnabled {
		t.Fatal("misleading report", report)
	}
	if _, err = s.update("tenant-a", 0, in, ModelCredentialInput{Action: "revoke"}); !errors.Is(err, errModelConflict) {
		t.Fatal("stale version accepted", err)
	}
	if _, err = s.update("tenant-b", 0, in, ModelCredentialInput{Action: "keep"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant ID accepted", err)
	}
	restored, err := OpenModelConfigStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	result, err := restored.validate("tenant-a", id)
	if err != nil || result["credential_status"] != "encrypted" {
		t.Fatal("encrypted credential recovery", result, err)
	}
	wrong, err := OpenModelConfigStore(dir, bytes.Repeat([]byte{99}, 32))
	if err != nil {
		t.Fatal(err)
	}
	result, err = wrong.validate("tenant-a", id)
	if err != nil || result["credential_status"] != "credential_unreadable" {
		t.Fatal("wrong KEK accepted", result, err)
	}
	// Ciphertext is cryptographically bound to tenant, record ID and provider.
	copied := restored.data.Tenants["tenant-a"].Entries[id]
	restored.data.Tenants["tenant-b"] = modelTenant{Version: 1, Entries: map[string]storedModel{id: copied}}
	result, err = restored.validate("tenant-b", id)
	if err != nil || result["credential_status"] != "credential_unreadable" {
		t.Fatal("ciphertext crossed tenant", result, err)
	}
	report, err = s.update("tenant-a", 1, in, ModelCredentialInput{Action: "revoke"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Entries[0].Enabled || report.Entries[0].SecretStatus != "revoked" {
		t.Fatal("revocation failed")
	}
	if _, err = s.update("tenant-a", 1, in, ModelCredentialInput{Action: "set", Secret: secret}); !errors.Is(err, errModelConflict) {
		t.Fatal("stale write resurrected key", err)
	}
	report, err = s.update("tenant-a", 2, in, ModelCredentialInput{Action: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Entries[0].SecretStatus != "revoked" || s.data.Tenants["tenant-a"].Entries[id].Ciphertext != "" {
		t.Fatal("metadata resurrected key")
	}
	disk, _ = os.ReadFile(filepath.Join(dir, "model-config.json"))
	if bytes.Contains(disk, []byte(copied.Ciphertext)) {
		t.Fatal("revoked ciphertext retained")
	}
}
func TestModelConfigNoKeyAllowlistAndSecretReference(t *testing.T) {
	s, err := OpenModelConfigStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	in := syntheticModel()
	if _, err = s.update("tenant", 0, in, ModelCredentialInput{Action: "set", Secret: "synthetic-key"}); !errors.Is(err, errModelKey) {
		t.Fatal("plaintext fallback", err)
	}
	for _, provider := range []string{"http://127.0.0.1:99", "openai-video", "unknown"} {
		bad := in
		bad.Provider = provider
		if _, err = s.update("tenant", 0, bad, ModelCredentialInput{Action: "keep"}); !errors.Is(err, errModelInput) {
			t.Fatal("unsupported/SSRF provider accepted", err)
		}
	}
	bad := in
	bad.Region = "https://attacker.test"
	if _, err = s.update("tenant", 0, bad, ModelCredentialInput{Action: "keep"}); !errors.Is(err, errModelInput) {
		t.Fatal("SSRF region accepted")
	}
	for _, reference := range []string{"PATH", "DATABASE_URL", "/etc/passwd", "ANTHROPIC_API_KEY"} {
		if _, err = s.update("tenant", 0, in, ModelCredentialInput{Action: "reference", Reference: reference}); !errors.Is(err, errModelInput) {
			t.Fatal("arbitrary reference accepted", reference, err)
		}
	}
	report, err := s.update("tenant", 0, in, ModelCredentialInput{Action: "reference", Reference: "OPENAI_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Entries[0].SecretStatus != "reference_unverified" {
		t.Fatal("reference falsely verified")
	}
	if _, err = OpenModelConfigStore(t.TempDir(), []byte("short")); err == nil {
		t.Fatal("weak KEK accepted")
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "model-config.json"), []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenModelConfigStore(dir, nil); err == nil {
		t.Fatal("corrupt configuration reset")
	}
}
func TestModelConfigHTTPRequiresAdminTenantCSRFAndRejectsEndpoint(t *testing.T) {
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, ta, sa, err := auth.login("model-owner@example.test", "synthetic-password-A", true)
	if err != nil {
		t.Fatal(err)
	}
	b, tb, sb, err := auth.login("model-other@example.test", "synthetic-password-B", true)
	if err != nil {
		t.Fatal(err)
	}
	org := auth.spaces(a.ID)[0]["id"]
	other := auth.spaces(b.ID)[0]["id"]
	h := SecureHandler(testStore(t), "http://localhost:5173", auth, AuthOptions{})
	route := "/api/v1/model-config?space_id=" + org
	authRequest(t, h, "GET", route, "", "", "", 401)
	authRequest(t, h, "GET", route, "", tb, "", 403)
	authRequest(t, h, "GET", "/api/v1/model-config?space_id=platform", "", ta, "", 403)
	input := map[string]any{"expected_version": 0, "entry": syntheticModel(), "credential": ModelCredentialInput{Action: "reference", Reference: "OPENAI_API_KEY"}}
	raw, _ := json.Marshal(input)
	authRequest(t, h, "PUT", route, string(raw), ta, "", 403)
	authRequest(t, h, "PUT", route, string(raw), ta, sa.CSRF, 410)
	// Seed a pre-upgrade record directly; the retired HTTP editor cannot create it.
	report, err := auth.models.update(org, 0, syntheticModel(), ModelCredentialInput{Action: "reference", Reference: "OPENAI_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	id := report.Entries[0].ID
	authRequest(t, h, "POST", "/api/v1/model-config/validate?space_id="+other, `{"id":"`+id+`"}`, tb, sb.CSRF, 404)
	authRequest(t, h, "POST", "/api/v1/model-config/live?space_id="+org, `{}`, ta, sa.CSRF, 403)
	bad := strings.Replace(string(raw), `"provider":"openai-text"`, `"endpoint":"http://127.0.0.1","provider":"openai-text"`, 1)
	authRequest(t, h, "PUT", route, bad, ta, sa.CSRF, 410)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+org+"/members", `{"email":"model-other@example.test","role":"editor"}`, ta, sa.CSRF, 200)
	authRequest(t, h, "GET", route, "", tb, "", 403)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+org+"/members", `{"email":"model-other@example.test","role":"admin"}`, ta, sa.CSRF, 200)
	authRequest(t, h, "GET", route, "", tb, "", 200)
	authRequest(t, h, "POST", "/api/v1/model-config/revoke?space_id="+other, `{"id":"`+id+`","expected_version":0}`, tb, sb.CSRF, 404)
	authRequest(t, h, "POST", "/api/v1/model-config/revoke?space_id="+org, `{"id":"`+id+`","expected_version":1}`, ta, sa.CSRF, 200)
	after, err := auth.models.report(org)
	if err != nil || after.Entries[0].SecretStatus != "revoked" || after.Entries[0].Enabled {
		t.Fatal("legacy revocation failed", err)
	}
}
