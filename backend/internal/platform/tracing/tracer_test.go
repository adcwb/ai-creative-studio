package tracing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type collector struct {
	mu              sync.Mutex
	records         []Record
	flushed, closed bool
	block           bool
}

func (c *collector) Export(ctx context.Context, r []Record) error {
	if c.block {
		<-ctx.Done()
		return ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r...)
	return nil
}
func (c *collector) Flush(context.Context) error { c.flushed = true; return nil }
func (c *collector) Close() error                { c.closed = true; return nil }

const parent = "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01"

func TestParentValidation(t *testing.T) {
	if !ValidParent(parent) {
		t.Fatal("valid rejected")
	}
	for _, v := range []string{"", parent + " ", strings.ToUpper(parent), strings.Replace(parent, "00-", "01-", 1), strings.Replace(parent, "1234567890abcdef1234567890abcdef", strings.Repeat("0", 32), 1), strings.Replace(parent, "-1234567890abcdef-", "-0000000000000000-", 1), parent[:53] + "ff", "\n" + parent} {
		if ValidParent(v) {
			t.Fatalf("accepted %q", v)
		}
	}
}
func TestCollectorChainPrivacyFlush(t *testing.T) {
	c := &collector{}
	tr, err := New(c, "api", 64)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithTracer(WithParent(context.Background(), parent), tr)
	ctx, http := Start(ctx, "http.server", Attributes{Method: "POST"})
	ctx, worker := Start(ctx, "worker.execute", Attributes{Kind: "image"})
	ctx, gov := Start(ctx, "governance.execute", Attributes{Provider: "openai-images"})
	_, provider := Start(ctx, "provider.request", Attributes{Method: "secret prompt url?token=secret", Provider: "secret", Kind: "secret", Status: 900})
	provider.End("unknown")
	gov.End("unknown")
	worker.End("error")
	http.HTTPStatus(202)
	http.End("ok")
	if err = tr.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.flushed || !c.closed || len(c.records) != 4 {
		t.Fatalf("bad collector %#v", c)
	}
	for i, r := range c.records {
		if r.TraceID != parent[3:35] {
			t.Fatal("trace lost")
		}
		if i < 3 && r.ParentID != c.records[i+1].SpanID {
			t.Fatal("parent lost")
		}
	}
	raw, _ := json.Marshal(c.records)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("unapproved attribute exported")
	}
	if c.records[0].Outcome != "unknown" {
		t.Fatal("unknown lost")
	}
}
func TestBoundsDeadlineAndConcurrentClose(t *testing.T) {
	c := &collector{block: true}
	tr, _ := New(c, "worker", 1)
	ctx := WithTracer(context.Background(), tr)
	for i := 0; i < 200; i++ {
		_, s := Start(ctx, "worker.execute", Attributes{})
		s.End("ok")
	}
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := tr.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline %v", err)
	}
	if err := tr.Close(context.Background()); err == nil {
		t.Fatal("sink failure hidden")
	}
	if tr.Stats().Dropped == 0 {
		t.Fatal("unbounded")
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, s := Start(ctx, "http.server", Attributes{})
			s.End("ok")
			_ = tr.Close(context.Background())
		}()
	}
	wg.Wait()
}
func TestDisabledAndUnsampled(t *testing.T) {
	SetDefault(nil)
	ctx, s := Start(context.Background(), "http.server", Attributes{})
	s.End("ok")
	if !ValidParent(Parent(ctx)) {
		t.Fatal("correlation unavailable")
	}
	c := &collector{}
	tr, _ := New(c, "api", 1)
	_, s = Start(WithTracer(WithParent(ctx, parent[:53]+"00"), tr), "http.server", Attributes{})
	s.End("ok")
	if err := tr.Close(context.Background()); err != nil || len(c.records) != 0 {
		t.Fatal("unsampled export")
	}
}
func TestJSONBoundsAndNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	sink, err := NewJSONSink(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewJSONSink(path, 1024); err == nil {
		t.Fatal("overwrote existing")
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0600 {
		t.Fatal("not private")
	}
	if err = sink.Export(context.Background(), []Record{{Operation: strings.Repeat("x", 2048)}}); err == nil {
		t.Fatal("unbounded disk")
	}
	if err = sink.Export(context.Background(), []Record{{Operation: "internal"}}); err != nil {
		t.Fatal(err)
	}
	if err = sink.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	sink.Close()
	link := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = NewJSONSink(link, 1024); err == nil {
		t.Fatal("followed symlink")
	}
}
