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
	providerName := env("IMAGE_PROVIDER", "mock-svg")
	key := ""
	if providerName == "openai-images" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	provider, capabilities := studio.ConfigureProvider(providerName, key)
	store, err := studio.OpenStore(env("DATA_DIR", "./data"), capabilities)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pipeline, err := studio.NewPipeline(ctx, provider, capabilities)
	if err != nil {
		return err
	}
	scriptPipeline, err := studio.NewScriptPipeline(ctx, studio.MockScriptProvider{Delay: 900 * time.Millisecond})
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: env("HTTP_ADDR", "127.0.0.1:8080"), Handler: studio.Handler(store, env("FRONTEND_ORIGIN", "http://localhost:5173")), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 3)
	workerDone := make(chan struct{})
	scriptWorkerDone := make(chan struct{})
	go func() { defer close(workerDone); errs <- studio.RunWorker(ctx, store, pipeline) }()
	go func() { defer close(scriptWorkerDone); errs <- studio.RunScriptWorker(ctx, store, scriptPipeline) }()
	go func() {
		slog.Info("demo API listening", "address", srv.Addr, "provider", capabilities.Provider, "provider_ready", capabilities.Ready)
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
	<-workerDone
	<-scriptWorkerDone
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}
