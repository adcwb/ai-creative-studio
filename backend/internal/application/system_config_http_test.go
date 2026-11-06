package application

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/deployconfig"
)

func TestSystemConfigurationRequiresDeploymentAdminAndCSRF(t *testing.T) {
	t.Setenv("STUDIO_CONFIG_FILE", filepath.Join(t.TempDir(), "backend-config.json"))
	config, err := deployconfig.Open()
	if err != nil {
		t.Fatal(err)
	}
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operatorUser, operator, operatorSession, err := auth.login("operator@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	_, owner, ownerSession, err := auth.login("owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(testStore(t), "http://localhost:5173", auth, AuthOptions{SystemConfig: config, SystemAdminUserIDs: []string{operatorUser.ID}, ReservedAdminEmails: []string{"operator@example.test"}})
	authRequest(t, h, "GET", "/api/v1/system-config", "", "", "", 401)
	authRequest(t, h, "GET", "/api/v1/system-config", "", owner, "", 403)
	authRequest(t, h, "PATCH", "/api/v1/system-config", `{}`, operator, "", 403)
	w := authRequest(t, h, "GET", "/api/v1/system-config", "", operator, "", 200)
	var report deployconfig.Report
	if err = json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"expected_version": report.Version, "changes": map[string]string{"DATABASE_URL": "postgres://synthetic:private@localhost/db"}})
	w = authRequest(t, h, "PATCH", "/api/v1/system-config", string(body), operator, operatorSession.CSRF, 200)
	if strings.Contains(w.Body.String(), "synthetic:private") {
		t.Fatal("sensitive value in response")
	}
	authRequest(t, h, "PATCH", "/api/v1/system-config", string(body), operator, operatorSession.CSRF, 409)
	authRequest(t, h, "PATCH", "/api/v1/system-config", string(body), owner, ownerSession.CSRF, 403)
}
