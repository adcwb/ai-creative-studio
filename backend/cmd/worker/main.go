// worker executes durable PostgreSQL jobs independently of the HTTP API.
// Managed remote requests require fresh authorization and execution governance.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/adcwb/ai-creative-studio/backend/internal/application"
	"github.com/adcwb/ai-creative-studio/backend/internal/bootstrap"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/deployconfig"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/diagnostics"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/servicehost"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	diagnostics.Configure("worker")
	if err := diagnostics.Invoke(context.Background(), "startup_error", func() error { return servicehost.Run("CreativeStudioWorker", run) }); err != nil {
		os.Exit(1)
	}
}
func run(parent context.Context) (resultErr error) {
	component := "deployment_config"
	defer func() {
		if resultErr != nil {
			diagnostics.StartupFailure(component)
		}
	}()
	if err := deployconfig.Load(); err != nil {
		return err
	}
	component = "tracing"
	flushTraces, traceErr := application.ConfigureTracingFromEnv("worker")
	if traceErr != nil {
		return traceErr
	}
	defer func() {
		if flushTraces() != nil {
			slog.Warn("trace export incomplete")
		}
	}()
	component = "worker_config"
	if os.Getenv("STORAGE_BACKEND") != "postgres" || os.Getenv("QUEUE_BACKEND") != "postgres" {
		return errors.New("worker requires postgres storage and queue")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL required")
	}
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		return errors.New("private DATA_DIR required")
	}
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	component = "storage"
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(8)
	if err = db.PingContext(ctx); err != nil {
		return err
	}
	if os.Getenv("STORAGE_AUTO_MIGRATE") == "true" {
		if err = application.InitPostgres(ctx, db); err != nil {
			return err
		}
	}
	if err = application.CheckPostgres(ctx, db); err != nil {
		return errors.New("postgres schema unavailable; run explicit migration")
	}
	concurrency, err := workerConcurrency(os.Getenv("WORKER_CONCURRENCY"))
	if err != nil {
		return err
	}
	queue := jobqueue.Queue{DB: db, Slots: make(chan struct{}, concurrency), BeforeClaim: func(ctx context.Context) error { return application.ReconcilePostgresJobs(ctx, db) }}

	store, err := application.OpenPostgresStore(dir, db)
	if err != nil {
		return err
	}
	defer store.Close()
	component = "blob_storage"
	if err = application.ConfigureBlobStorageFromEnv(store); err != nil {
		return err
	}
	component = "execution"
	if err = application.ConfigureExecutionFromEnv(store); err != nil {
		return err
	}
	component = "worker_runtime"
	if err = application.ConfigureWorkerRuntimeFromEnv(store, dir); err != nil {
		return err
	}
	provider, caps := application.ConfigureImageFromEnv()
	component = "providers"
	_, err = bootstrap.ConfigureProviders(store, provider, caps)
	if err != nil {
		return err
	}
	component = "pipeline"
	pipeline, err := application.NewPipeline(ctx, provider, caps)
	if err != nil {
		return err
	}
	script, err := application.NewScriptPipeline(ctx, application.MockScriptProvider{})
	if err != nil {
		return err
	}
	storyboard, err := application.NewStoryboardPipeline(ctx, application.MockStoryboardProvider{})
	if err != nil {
		return err
	}
	component = "serve"
	slog.Info("durable worker started", "kinds", "image,script,storyboard,export,alignment,creative,story_check,workflow", "default_provider", caps.Provider)
	results := make(chan error, 10)
	go func() { results <- application.RunQueuedImageWorker(ctx, queue, store, pipeline) }()
	go func() { results <- application.RunQueuedScriptWorker(ctx, queue, store, script) }()
	go func() { results <- application.RunQueuedStoryboardWorker(ctx, queue, store, storyboard) }()
	go func() { results <- application.RunQueuedExportWorker(ctx, queue, store) }()
	go func() { results <- application.RunQueuedAlignmentWorker(ctx, queue, store) }()
	go func() { results <- application.RunQueuedCreativeWorker(ctx, queue, store) }()
	go func() { results <- application.RunQueuedCreativeRecoveryWorker(ctx, queue, store) }()
	go func() { results <- application.RunQueuedImageRecoveryWorker(ctx, queue, store) }()
	go func() { results <- application.RunQueuedStoryCheckWorker(ctx, queue, store) }()
	go func() { results <- application.RunWorkflowWorker(ctx, store) }()
	err = <-results
	cancel()
	for range 9 {
		<-results
	}
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func workerConcurrency(raw string) (int, error) {
	if raw == "" {
		return 2, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 16 {
		return 0, errors.New("WORKER_CONCURRENCY must be an integer from 1 to 16")
	}
	return n, nil
}
