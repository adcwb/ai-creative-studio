package reviewfixture

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"
)

const Prefix = "studio-stream-review-"

func Command(name string, input io.Reader, output io.Writer) error {
	name = strings.TrimSuffix(filepath.Base(name), ".exe")
	if !strings.HasPrefix(name, Prefix) {
		return errors.New("fixture name missing")
	}
	parts := strings.Split(strings.TrimPrefix(name, Prefix), "-")
	if len(parts) != 2 || len(parts[0]) != 64 || (parts[1] != "allow" && parts[1] != "review") {
		return errors.New("invalid fixture name")
	}
	if _, e := hex.DecodeString(parts[0]); e != nil {
		return errors.New("invalid fixture digest")
	}
	return Run(parts[0], parts[1], input, output)
}

// This independent stdlib parser hashes the actual decoded Data stream, checks
// the metadata envelope and never stores the full base64 body in one allocation.
type fixtureBase64Reader struct {
	r        *bufio.Reader
	pending  []byte
	complete bool
}

func (f *fixtureBase64Reader) Read(p []byte) (int, error) {
	if len(f.pending) == 0 && !f.complete {
		chunk, e := f.r.ReadSlice('"')
		if e == nil {
			chunk = chunk[:len(chunk)-1]
			f.complete = true
		} else if !errors.Is(e, bufio.ErrBufferFull) {
			return 0, e
		}
		if bytes.IndexAny(chunk, "\r\n") >= 0 {
			return 0, errors.New("invalid fixture base64")
		}
		f.pending = chunk
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	if n == 0 && f.complete {
		return 0, io.EOF
	}
	return n, nil
}
func Run(expected, status string, input io.Reader, output io.Writer) error {
	marker := []byte(`"Media":[{"Data":"`)
	head := []byte{}
	buffer := make([]byte, 64<<10)
	index := -1
	for index < 0 {
		n, e := input.Read(buffer)
		head = append(head, buffer[:n]...)
		if len(head) > 128<<10 {
			return errors.New("fixture prefix too large")
		}
		index = bytes.Index(head, marker)
		if e != nil && index < 0 {
			return e
		}
	}
	prefix := append([]byte{}, head[:index]...)
	reader := bufio.NewReaderSize(io.MultiReader(bytes.NewReader(head[index+len(marker):]), input), 64<<10)
	hash := sha256.New()
	if _, e := io.Copy(hash, base64.NewDecoder(base64.StdEncoding.Strict(), &fixtureBase64Reader{r: reader})); e != nil {
		return e
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("fixture media digest mismatch")
	}
	tail, e := io.ReadAll(io.LimitReader(reader, 65537))
	if e != nil || len(tail) >= 65536 {
		return errors.New("fixture tail too large")
	}
	raw := append(append(prefix, []byte(`"Media":[{"Data":""`)...), tail...)
	var envelope struct {
		Stage string
		Media []map[string]json.RawMessage
	}
	if e = json.Unmarshal(raw, &envelope); e != nil {
		return e
	}
	if envelope.Stage != "output" || len(envelope.Media) != 1 {
		return errors.New("fixture envelope mismatch")
	}
	if _, ok := envelope.Media[0]["Stream"]; ok {
		return errors.New("fixture received metadata instead of bytes")
	}
	return json.NewEncoder(output).Encode(map[string]any{"provider": "local-fixture", "model": "synthetic", "status": status, "categories": []string{}, "checked_at": time.Now().UTC()})
}
