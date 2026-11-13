package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestExecutionReviewWireAndAbandonedConsumer(t *testing.T) {
	data := bytes.Repeat([]byte("actual synthetic review bytes"), 4000)
	input := ExecutionRequest{Stage: "output", HasMedia: true, Model: "synthetic-model", Media: []ExecutionMedia{{Data: data, MIME: "audio/wav"}}}
	reader, done, e := executionJSONStream(context.Background(), input)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(reader)
	reader.Close()
	if e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	var decoded ExecutionRequest
	if e = json.Unmarshal(raw, &decoded); e != nil {
		t.Fatal(e)
	}
	if decoded.Stage != input.Stage || decoded.Model != input.Model || len(decoded.Media) != 1 || decoded.Media[0].MIME != "audio/wav" || !bytes.Equal(decoded.Media[0].Data, data) || decoded.Media[0].Stream != nil {
		t.Fatal("review wire content changed")
	}
	reader, done, e = executionJSONStream(context.Background(), input)
	if e != nil {
		t.Fatal(e)
	}
	reader.Close()
	select {
	case e = <-done:
		if e == nil {
			t.Fatal("abandoned send reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("abandoned consumer leaked producer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader, done, e = executionJSONStream(ctx, input)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.ReadAll(reader)
	reader.Close()
	if e == nil {
		t.Fatal("cancelled review emitted a complete body")
	}
	if e = <-done; e == nil {
		t.Fatal("cancelled producer reported success")
	}
}
