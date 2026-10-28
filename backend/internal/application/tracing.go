package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"
)

// ConfigureTracingFromEnv reads only explicit non-secret telemetry settings.
// No default exporter, remote endpoint, authentication or external service exists.
func ConfigureTracingFromEnv(service string) (func() error, error) {
	mode := os.Getenv("TRACE_MODE")
	if mode == "" || mode == "off" {
		return func() error { return nil }, nil
	}
	if mode != "json" || !filepath.IsAbs(os.Getenv("TRACE_JSON_PATH")) {
		return nil, errors.New("explicit absolute JSON trace path required")
	}
	sink, err := tracing.NewJSONSink(os.Getenv("TRACE_JSON_PATH"), 64<<20)
	if err != nil {
		return nil, err
	}
	tracer, err := tracing.New(sink, service, 256)
	if err != nil {
		_ = sink.Close()
		return nil, err
	}
	tracing.SetDefault(tracer)
	return func() error {
		tracing.SetDefault(nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return tracer.Close(ctx)
	}, nil
}
