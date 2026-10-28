// Package bootstrap assembles the application dependencies shared by API and workers.
package bootstrap

import (
	"errors"

	"github.com/adcwb/ai-creative-studio/backend/internal/application"
)

// Providers contains the startup defaults. Managed per-request models continue
// to be authorized and resolved by the application at execution time.
type Providers struct {
	Image             application.ImageProvider
	ImageCapabilities application.Capabilities
	Creative          application.CreativeProvider
}

// ConfigureProviders installs the same defaults and runtime policy in each process.
// The image provider is constructed before opening storage because its provenance
// participates in durable image task recovery.
func ConfigureProviders(store *application.FileStore, image application.ImageProvider, imageCaps application.Capabilities) (Providers, error) {
	if store == nil {
		return Providers{}, errors.New("application store required")
	}
	creative, creativeCaps := application.ConfigureCreativeFromEnv()
	if err := application.ConfigureCreative(store, creative, creativeCaps); err != nil {
		return Providers{}, err
	}
	speech, speechCaps := application.ConfigureSpeechFromEnv()
	application.SetCreativeSpeech(store, speech, speechCaps)
	music, musicCaps := application.ConfigureMusicFromEnv()
	application.SetCreativeMusic(store, music, musicCaps)
	effects, effectCaps := application.ConfigureSoundEffectFromEnv()
	application.SetCreativeSoundEffect(store, effects, effectCaps)
	video, videoCaps := application.ConfigureVideoFromEnv()
	application.SetCreativeVideo(store, video, videoCaps)
	application.SetCreativeImage(store, image, imageCaps)
	asr, asrCaps := application.ConfigureAlignmentFromEnv()
	application.SetAlignmentProvider(store, asr, asrCaps)
	forced, forcedCaps := application.ConfigureForcedAlignmentFromEnv()
	application.SetForcedAlignmentProvider(store, forced, forcedCaps)
	lipSync, lipSyncCaps := application.ConfigureLipSyncFromEnv()
	application.SetCreativeLipSync(store, lipSync, lipSyncCaps)
	application.ConfigureDigitalHumanFromEnv(store)
	if err := application.ValidateRuntimeFromEnv(imageCaps, creativeCaps, speechCaps, videoCaps, asrCaps, musicCaps, forcedCaps, lipSyncCaps, effectCaps); err != nil {
		return Providers{}, err
	}
	return Providers{Image: image, ImageCapabilities: imageCaps, Creative: creative}, nil
}
