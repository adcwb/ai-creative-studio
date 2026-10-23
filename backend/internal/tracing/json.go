package tracing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"syscall"
)

// JSONSink creates a new private file, never appends to an existing file or
// follows a symlink. Capacity exhaustion stops export rather than growing disk.
type JSONSink struct {
	file              *os.File
	written, maxBytes int64
}

func NewJSONSink(path string, maxBytes int64) (*JSONSink, error) {
	if maxBytes < 1024 || maxBytes > 64<<20 {
		return nil, errors.New("invalid trace capacity")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("trace file unavailable")
	}
	return &JSONSink{file: f, maxBytes: maxBytes}, nil
}
func (s *JSONSink) Export(ctx context.Context, records []Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var batch []byte
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			return errors.New("invalid trace record")
		}
		batch = append(batch, raw...)
		batch = append(batch, '\n')
	}
	if int64(len(batch)) > s.maxBytes-s.written {
		return errors.New("trace capacity reached")
	}
	n, err := s.file.Write(batch)
	s.written += int64(n)
	if err != nil {
		return errors.New("trace write failed")
	}
	return nil
}
func (s *JSONSink) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.file.Sync()
}
func (s *JSONSink) Close() error { return s.file.Close() }
