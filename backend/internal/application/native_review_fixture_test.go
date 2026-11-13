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
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const nativeReviewFixturePrefix = "studio-stream-review-"

// Test-only native executable fixture: copied test binaries dispatch here
// by an exact filename contract. No production binary has this entry point.
func TestMain(m *testing.M) {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if strings.HasPrefix(name, nativeReviewFixturePrefix) {
		parts := strings.Split(strings.TrimPrefix(name, nativeReviewFixturePrefix), "-")
		if len(parts) != 2 || len(parts[0]) != 64 || (parts[1] != "allow" && parts[1] != "review") {
			os.Exit(2)
		}
		if _, e := hex.DecodeString(parts[0]); e != nil {
			os.Exit(2)
		}
		if e := runNativeReviewFixture(parts[0], parts[1]); e != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
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
func runNativeReviewFixture(expected, status string) error {
	marker := []byte(`"Media":[{"Data":"`)
	head := []byte{}
	buffer := make([]byte, 64<<10)
	index := -1
	for index < 0 {
		n, e := os.Stdin.Read(buffer)
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
	reader := bufio.NewReaderSize(io.MultiReader(bytes.NewReader(head[index+len(marker):]), os.Stdin), 64<<10)
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
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"provider": "local-fixture", "model": "synthetic", "status": status, "categories": []string{}, "checked_at": time.Now().UTC()})
}
func nativeStreamReviewExecutable(t *testing.T, sha, status string) string {
	t.Helper()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	path := filepath.Join(t.TempDir(), nativeReviewFixturePrefix+sha+"-"+status+suffix)
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	// A separate file avoids sharing an image section with the still-running
	// parent test executable; Windows must be able to remove this fixture.
	source, e := os.Open(executable)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	target, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.Copy(target, source)
	closeErr := target.Close()
	if e != nil {
		t.Fatal(e)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return path
}
func TestNativePortableReviewCommandContract(t *testing.T) {
	data := []byte("synthetic reviewed bytes")
	sum := sha256.Sum256(data)
	command := nativeStreamReviewExecutable(t, hex.EncodeToString(sum[:]), "allow")
	policy := &ExecutionPolicy{ModerationCommand: command, ModerationContract: "local-or-free-v1", ModerationSource: "native synthetic test", ModerationExpiresAt: time.Now().Add(time.Hour)}
	g := ExecutionGovernor{Moderator: policy}
	r := ExecutionRequest{Stage: "output", HasMedia: true, Media: []ExecutionMedia{{Data: data, MIME: "audio/wav"}}}
	if e := g.ReviewResult(context.Background(), r); e != nil {
		t.Fatal("native review did not see actual bytes", e)
	}
	data[0] ^= 1
	if e := g.ReviewResult(context.Background(), r); e == nil {
		t.Fatal("changed bytes accepted")
	}
}
