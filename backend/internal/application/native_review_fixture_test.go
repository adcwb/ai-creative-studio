package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/testsupport/reviewfixture"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const nativeReviewFixturePrefix = reviewfixture.Prefix

// Test-only native executable fixture: copied test binaries dispatch here
// by an exact filename contract. No production binary has this entry point.
func TestMain(m *testing.M) {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if name == avatarFixturePrefix {
		if avatarFileFixtureCommand() != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	if strings.HasPrefix(name, nativeReviewFixturePrefix) {
		if e := reviewfixture.Command(name, os.Stdin, os.Stdout); e != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func nativeStreamReviewExecutable(t *testing.T, sha, status string) string {
	t.Helper()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	path := filepath.Join(t.TempDir(), nativeReviewFixturePrefix+sha+"-"+status+suffix)
	executable := os.Getenv("STUDIO_NATIVE_REVIEW_FIXTURE")
	var e error
	if executable == "" {
		executable, e = os.Executable()
	}
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

func TestNativePortableReviewLargeStreamUsesRealBytes(t *testing.T) {
	path := largeWaveFile(t)
	sha, _ := nativeFileHash(t, path)
	command := nativeStreamReviewExecutable(t, sha, "allow")
	policy := &ExecutionPolicy{ModerationCommand: command, ModerationContract: "local-or-free-v1", ModerationSource: "native large synthetic test", ModerationExpiresAt: time.Now().Add(time.Hour)}
	g := ExecutionGovernor{Moderator: policy}
	media, e := executionFileMedia(context.Background(), path, "audio/wav")
	if e != nil {
		t.Fatal(e)
	}
	request := ExecutionRequest{Stage: "output", HasMedia: true, Media: []ExecutionMedia{media}}
	if e = g.ReviewResult(context.Background(), request); e != nil {
		t.Fatal("native large content review", e)
	}
	wrong := strings.Repeat("0", 64)
	policy.ModerationCommand = nativeStreamReviewExecutable(t, wrong, "allow")
	if e = g.ReviewResult(context.Background(), request); e == nil {
		t.Fatal("wrong expected media digest accepted")
	}
}
