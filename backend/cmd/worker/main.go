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

	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
	"github.com/adcwb/ai-creative-studio/backend/internal/studio"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped; inspect private database availability and configuration")
		os.Exit(1)
	}
}
func run() error {
	flushTraces, traceErr := studio.ConfigureTracingFromEnv("worker")
	if traceErr != nil {
		return traceErr
	}
	defer func() {
		if flushTraces() != nil {
			slog.Warn("trace export incomplete")
		}
	}()
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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
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
		if err = studio.InitPostgres(ctx, db); err != nil {
			return err
		}
	}
 if err = studio.CheckPostgres(ctx, db); err != nil {
 return errors.New("postgres schema unavailable; run explicit migration")
 }
	concurrency, err := workerConcurrency(os.Getenv("WORKER_CONCURRENCY"))
	if err != nil {
		return err
	}
	queue := jobqueue.Queue{DB: db, Slots: make(chan struct{}, concurrency), BeforeClaim: func(ctx context.Context) error { return studio.ReconcilePostgresJobs(ctx, db) }}

	store, err := studio.OpenPostgresStore(dir, db)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = studio.ConfigureBlobStorageFromEnv(store); err != nil {
		return err
	}
	if err = studio.ConfigureExecutionFromEnv(store); err != nil {
		return err
	}
	if err = studio.ConfigureWorkerRuntimeFromEnv(store, dir); err != nil {
		return err
	}
	provider, caps, err := studio.ConfigureWorkerProvidersFromEnv(store)
	if err != nil {
		return err
	}
	pipeline, err := studio.NewPipeline(ctx, provider, caps)
	if err != nil {
		return err
	}
	script, err := studio.NewScriptPipeline(ctx, studio.MockScriptProvider{})
	if err != nil {
		return err
	}
	storyboard, err := studio.NewStoryboardPipeline(ctx, studio.MockStoryboardProvider{})
	if err != nil {
		return err
	}
	slog.Info("durable worker started", "kinds", "image,script,storyboard,export,alignment,creative", "default_provider", caps.Provider)
	results := make(chan error, 8)
	go func() { results <- studio.RunQueuedImageWorker(ctx, queue, store, pipeline) }()
	go func() { results <- studio.RunQueuedScriptWorker(ctx, queue, store, script) }()
	go func() { results <- studio.RunQueuedStoryboardWorker(ctx, queue, store, storyboard) }()
	go func() { results <- studio.RunQueuedExportWorker(ctx, queue, store) }()
	go func() { results <- studio.RunQueuedAlignmentWorker(ctx, queue, store) }()
	go func() { results <- studio.RunQueuedCreativeWorker(ctx, queue, store) }()
	go func() { results <- studio.RunQueuedCreativeRecoveryWorker(ctx, queue, store) }()
	go func() { results <- studio.RunQueuedImageRecoveryWorker(ctx, queue, store) }()
	err = <-results
	cancel()
	for range 7 {
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
