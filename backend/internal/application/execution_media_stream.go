package application

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/governance"
)

type mediaReadCloser struct {
	io.Reader
	io.Closer
}

func executionFileMedia(ctx context.Context, path, mime string) (ExecutionMedia, error) {
	f, e := openLocalBlob(path)
	if e != nil {
		return ExecutionMedia{}, e
	}
	defer f.Close()
	info, e := hashBlobStream(ctx, "", f)
	if e != nil {
		return ExecutionMedia{}, e
	}
	// Existing in-process review adapters retain their original small payloads.
	if info.Size <= 64<<10 {
		if _, e = f.Seek(0, 0); e != nil {
			return ExecutionMedia{}, e
		}
		b, e := io.ReadAll(f)
		return ExecutionMedia{Data: b, MIME: mime}, e
	}
	return ExecutionMedia{MIME: mime, Stream: &governance.MediaStream{SHA256: info.SHA256, Size: info.Size, Open: func() (io.ReadCloser, error) {
		f, e := openLocalBlob(path)
		if e != nil {
			return nil, e
		}
		return mediaReadCloser{contextBlobReader{ctx, f}, f}, nil
	}}}, nil
}

func hasStreamMedia(r ExecutionRequest) bool {
	for _, m := range r.Media {
		if m.Stream != nil {
			return true
		}
	}
	return false
}
func (p *ExecutionPolicy) SupportsStreamingMedia() bool { return true }
func (g *ExecutionGovernor) review(ctx context.Context, r ExecutionRequest) (ModerationDecision, error) {
	if hasStreamMedia(r) {
		v, ok := g.Moderator.(interface{ SupportsStreamingMedia() bool })
		if !ok || !v.SupportsStreamingMedia() || ExecutionRequestDigest(r) == "" {
			return ModerationDecision{}, ErrExecutionGovernance
		}
	}
	return g.Moderator.ReviewExecution(ctx, r)
}

// The command sees the existing Data(base64)/MIME wire contract, never a local
// path or a metadata-only substitute for inspecting the actual media bytes.
func executionJSONStream(ctx context.Context, r ExecutionRequest) (*io.PipeReader, <-chan error, error) {
	metadata := r
	metadata.Media = nil
	raw, e := json.Marshal(metadata)
	if e != nil || len(raw) > 64<<20 {
		return nil, nil, ErrExecutionGovernance
	}
	fields := map[string]json.RawMessage{}
	if e = json.Unmarshal(raw, &fields); e != nil {
		return nil, nil, e
	}
	delete(fields, "Media")
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	// Coalesce base64 encoder fragments before the process pipe. Windows pipe
	// writes are expensive; keep memory bounded without changing wire bytes,
	// deadlines, cancellation or the final content/hash validation.
	go func() {
		buffered := bufio.NewWriterSize(writer, 64<<10)
		e := writeExecutionJSON(ctx, buffered, fields, r.Media)
		if e == nil {
			e = buffered.Flush()
		}
		_ = writer.CloseWithError(e)
		done <- e
	}()
	return reader, done, nil
}
func writeExecutionJSON(ctx context.Context, w io.Writer, fields map[string]json.RawMessage, media []ExecutionMedia) error {
	put := func(v string) error { _, e := io.WriteString(w, v); return e }
	if e := put("{"); e != nil {
		return e
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		key, _ := json.Marshal(k)
		if _, e := w.Write(append(append(key, ':'), fields[k]...)); e != nil {
			return e
		}
		if e := put(","); e != nil {
			return e
		}
	}
	if e := put(`"Media":[`); e != nil {
		return e
	}
	for i, m := range media {
		if i > 0 {
			if e := put(","); e != nil {
				return e
			}
		}
		if m.Stream == nil {
			if m.Data == nil {
				raw, e := json.Marshal(m)
				if e != nil {
					return e
				}
				if _, e = w.Write(raw); e != nil {
					return e
				}
				continue
			}
			if e := put(`{"Data":"`); e != nil {
				return e
			}
			encoder := base64.NewEncoder(base64.StdEncoding, w)
			_, e := io.CopyBuffer(encoder, contextBlobReader{ctx, bytes.NewReader(m.Data)}, make([]byte, 64<<10))
			e = errors.Join(e, encoder.Close())
			if e != nil {
				return e
			}
			mime, _ := json.Marshal(m.MIME)
			if e = put(`","MIME":` + string(mime) + `}`); e != nil {
				return e
			}
			continue
		}

		if m.Data != nil || m.Stream.Open == nil {
			return ErrExecutionGovernance
		}
		f, e := m.Stream.Open()
		if e != nil {
			return e
		}
		if e = put(`{"Data":"`); e != nil {
			f.Close()
			return e
		}
		encoder := base64.NewEncoder(base64.StdEncoding, w)
		h := sha256.New()
		n, copyErr := io.CopyBuffer(io.MultiWriter(encoder, h), contextBlobReader{ctx, f}, make([]byte, 64<<10))
		e = errors.Join(copyErr, encoder.Close(), f.Close())
		if e != nil {
			return e
		}
		if n != m.Stream.Size || hex.EncodeToString(h.Sum(nil)) != m.Stream.SHA256 {
			return ErrBlobIntegrity
		}
		mime, _ := json.Marshal(m.MIME)
		if e = put(`","MIME":` + string(mime) + `}`); e != nil {
			return e
		}
	}
	return put("]}")
}

// Keep the temporary result private until validation, full review and the
// existing fenced/idempotent asset transaction have all succeeded.
func newProviderMediaFile(dir string) (*os.File, error) {
	return os.CreateTemp(dir, ".provider-media-")
}
