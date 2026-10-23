package studio

// ConfigureWorkerProvidersFromEnv uses the same legacy/local provider defaults
// as the API. Ready remote environment adapters are rejected by runtime policy;
// paid execution requires a governed managed selection instead.
func ConfigureWorkerProvidersFromEnv(s *FileStore) (ImageProvider, Capabilities, error) {
	image, caps := ConfigureImageFromEnv()
	creative, creativeCaps := ConfigureCreativeFromEnv()
	if err := ConfigureCreative(s, creative, creativeCaps); err != nil {
		return nil, caps, err
	}
	speech, speechCaps := ConfigureSpeechFromEnv()
	SetCreativeSpeech(s, speech, speechCaps)
	video, videoCaps := ConfigureVideoFromEnv()
	SetCreativeVideo(s, video, videoCaps)
	music, musicCaps := ConfigureMusicFromEnv()
	SetCreativeMusic(s, music, musicCaps)
	effect, effectCaps := ConfigureSoundEffectFromEnv()
	SetCreativeSoundEffect(s, effect, effectCaps)
	forced, forcedCaps := ConfigureForcedAlignmentFromEnv()
	SetForcedAlignmentProvider(s, forced, forcedCaps)
	lipSync, lipSyncCaps := ConfigureLipSyncFromEnv()
	SetCreativeLipSync(s, lipSync, lipSyncCaps)
	asr, asrCaps := ConfigureAlignmentFromEnv()
	SetAlignmentProvider(s, asr, asrCaps)
	SetCreativeImage(s, image, caps)
	if err := ValidateRuntimeFromEnv(caps, creativeCaps, speechCaps, videoCaps, asrCaps, musicCaps, forcedCaps, lipSyncCaps, effectCaps); err != nil {
		return nil, caps, err
	}
	return image, caps, nil
}
