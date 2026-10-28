package main

import (
	"strings"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/application"
)

func TestStorageConfigurationDoesNotFallbackOrExposeDSN(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("STORAGE_BACKEND", "unknown")
	if store, db, err := openStorage(application.Capabilities{}); err == nil || store != nil || db != nil {
		t.Fatal("unknown backend silently opened local storage")
	}
	t.Setenv("STORAGE_BACKEND", "postgres")
	t.Setenv("DATABASE_URL", "")
	if store, db, err := openStorage(application.Capabilities{}); err == nil || store != nil || db != nil {
		t.Fatal("missing database configuration silently opened local storage")
	}
	t.Setenv("DATABASE_URL", "host=localhost port=invalid password=SYNTHETIC-PRIVATE-TEST")
	store, db, err := openStorage(application.Capabilities{})
	if err == nil || store != nil || db != nil || strings.Contains(err.Error(), "SYNTHETIC") {
		t.Fatal("invalid database configuration not safely rejected")
	}
}
