package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/studio"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	flushTraces, traceErr := studio.ConfigureTracingFromEnv("api")
	if traceErr != nil {
		return traceErr
	}
	defer func() {
		if flushTraces() != nil {
			slog.Warn("trace export incomplete")
		}
	}()
	provider, capabilities := studio.ConfigureImageFromEnv()
	store, db, err := openStorage(capabilities)
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}
	defer store.Close()
	if err := studio.ConfigureBlobStorageFromEnv(store); err != nil {
		return err
	}
	if err := studio.ConfigureExecutionFromEnv(store); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pipeline, err := studio.NewPipeline(ctx, provider, capabilities)
	if err != nil {
		return err
	}
	creative, creativeCaps := studio.ConfigureCreativeFromEnv()
	if err = studio.ConfigureCreative(store, creative, creativeCaps); err != nil {
		return err
	}
	speech, speechCaps := studio.ConfigureSpeechFromEnv()
	studio.SetCreativeSpeech(store, speech, speechCaps)
	music, musicCaps := studio.ConfigureMusicFromEnv()
	studio.SetCreativeMusic(store, music, musicCaps)
	effects, effectCaps := studio.ConfigureSoundEffectFromEnv()
	studio.SetCreativeSoundEffect(store, effects, effectCaps)
	video, videoCaps := studio.ConfigureVideoFromEnv()
	studio.SetCreativeVideo(store, video, videoCaps)
	studio.SetCreativeImage(store, provider, capabilities)
	asr, asrCaps := studio.ConfigureAlignmentFromEnv()
	studio.SetAlignmentProvider(store, asr, asrCaps)
	forced, forcedCaps := studio.ConfigureForcedAlignmentFromEnv()
	studio.SetForcedAlignmentProvider(store, forced, forcedCaps)
	lipSync, lipSyncCaps := studio.ConfigureLipSyncFromEnv()
	studio.SetCreativeLipSync(store, lipSync, lipSyncCaps)
	if err := studio.ValidateRuntimeFromEnv(capabilities, creativeCaps, speechCaps, videoCaps, asrCaps, musicCaps, forcedCaps, lipSyncCaps, effectCaps); err != nil {
		return err
	}
	scriptPipeline, err := studio.NewScriptPipeline(ctx, studio.CreativeScriptProvider(creative))
	if err != nil {
		return err
	}
	storyboardPipeline, err := studio.NewStoryboardPipeline(ctx, studio.CreativeStoryboardProvider(creative))
	if err != nil {
		return err
	}
	handler, err := studio.HandlerFromEnv(store, env("FRONTEND_ORIGIN", "http://localhost:5173"), env("DATA_DIR", "./data"))
	if err != nil {
		return err
	}
	var ops *studio.Operations
	if db != nil {
		ops, err = studio.OpenPostgresOperations(db)
	} else {
		ops, err = studio.OpenOperations(env("DATA_DIR", "./data"))
	}
	if err != nil {
		return err
	}
	defer ops.Close()
	handler = ops.Wrap(store.GuardPersistence(handler))
	srv := &http.Server{Addr: env("HTTP_ADDR", "127.0.0.1:8080"), Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	metrics, listener, err := metricsServer(os.Getenv("METRICS_ADDR"), ops.MetricsHandler())
	if err != nil {
		return err
	}
	if metrics != nil {
		defer metrics.Close()
	}
	errs := make(chan error, 6)
	if metrics != nil {
		go func() { errs <- metrics.Serve(listener) }()
	}
	workerDone := make(chan struct{})
	creativeWorkerDone := make(chan struct{})
	if db == nil {
		go func() { defer close(creativeWorkerDone); errs <- studio.RunCreativeWorker(ctx, store) }()
	} else {
		close(creativeWorkerDone)
	}
	scriptWorkerDone := make(chan struct{})
	storyboardWorkerDone := make(chan struct{})
	if db == nil {
		go func() {
			defer close(storyboardWorkerDone)
			errs <- studio.RunStoryboardWorker(ctx, store, storyboardPipeline)
		}()
		go func() { defer close(workerDone); errs <- studio.RunWorker(ctx, store, pipeline) }()
		go func() { defer close(scriptWorkerDone); errs <- studio.RunScriptWorker(ctx, store, scriptPipeline) }()
	} else {
		close(storyboardWorkerDone)
		close(workerDone)
		close(scriptWorkerDone)
	}
	go func() {
		slog.Info("studio API listening", "address", srv.Addr, "provider", capabilities.Provider, "provider_ready", capabilities.Ready)
		errs <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err = <-errs:
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdown)
	<-creativeWorkerDone
	<-workerDone
	<-scriptWorkerDone
	<-storyboardWorkerDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}
