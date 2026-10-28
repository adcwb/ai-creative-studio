package bootstrap_test

import (
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/application"
	"github.com/adcwb/ai-creative-studio/backend/internal/bootstrap"
)

func TestConfigureProvidersSharesLocalDefaultsAndFailsClosed(t *testing.T) {
	t.Setenv("IMAGE_PROVIDER", "mock-svg")
	for _, key := range []string{"TEXT_PROVIDER", "SPEECH_PROVIDER", "VIDEO_PROVIDER", "MUSIC_PROVIDER", "ASR_PROVIDER", "FORCED_ALIGNMENT_COMMAND", "FORCED_ALIGNMENT_MODEL", "LIP_SYNC_COMMAND", "LIP_SYNC_MODEL", "SOUND_EFFECT_COMMAND", "SOUND_EFFECT_MODEL", "STORAGE_BACKEND", "QUEUE_BACKEND", "MEDIA_BACKEND"} {
		t.Setenv(key, "")
	}
	image, caps := application.ConfigureImageFromEnv()
	if _, err := bootstrap.ConfigureProviders(nil, image, caps); err == nil {
		t.Fatal("missing store accepted")
	}
	store, err := application.OpenStore(t.TempDir(), caps)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	providers, err := bootstrap.ConfigureProviders(store, image, caps)
	if err != nil {
		t.Fatal(err)
	}
	if providers.Image != image || providers.ImageCapabilities.Provider != caps.Provider {
		t.Fatal("image provenance changed during startup")
	}
	t.Setenv("STORAGE_BACKEND", "postgres")
	t.Setenv("QUEUE_BACKEND", "inprocess")
	if _, err := bootstrap.ConfigureProviders(store, image, caps); err == nil {
		t.Fatal("unsupported storage/queue pair accepted")
	}
}
