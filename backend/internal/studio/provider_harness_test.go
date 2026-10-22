package studio

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// TestProviderHarness is compiled only by go test. Production binaries have no
// alternate endpoint or synthetic-key switch. scripts/provider_check.mjs owns it.
func TestProviderHarness(t *testing.T) {
	if os.Getenv("STUDIO_TEST_HARNESS") != "1" {
		t.Skip("local integration harness only")
	}
	provider, caps := ConfigureProvider("mock-svg", "")
	switch os.Getenv("STUDIO_TEST_MODE") {
	case "mock":
	case "unavailable":
		provider, caps = ConfigureProvider("openai-images", "")
	case "real":
		endpoint := os.Getenv("STUDIO_TEST_UPSTREAM")
		u, e := url.Parse(endpoint)
		if e != nil || u.Scheme != "http" || u.User != nil || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			t.Fatal("test upstream must be literal loopback HTTP")
		}
		caps = realCapabilities()
		provider = newOpenAIProvider(syntheticAPIKey, endpoint, &http.Client{Timeout: 2 * time.Second})
	default:
		t.Fatal("explicit test mode required")
	}
	address := os.Getenv("HTTP_ADDR")
	host, _, e := net.SplitHostPort(address)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("test API must bind loopback")
	}
	s, e := OpenStore(os.Getenv("DATA_DIR"), caps)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p, e := NewPipeline(ctx, provider, caps)
	if e != nil {
		t.Fatal(e)
	}
	srv := &http.Server{Addr: address, Handler: Handler(s, os.Getenv("FRONTEND_ORIGIN")), ReadHeaderTimeout: time.Second}
	done := make(chan error, 2)
	go func() { done <- RunWorker(ctx, s, p) }()
	go func() { done <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case e = <-done:
		if e != nil && !errors.Is(e, http.ErrServerClosed) {
			t.Error(e)
		}
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
}
