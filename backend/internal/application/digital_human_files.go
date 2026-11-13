package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativeproc"
)

// AvatarFiles consumes private copies, never paths from an HTTP client.
// Engines write output.mp4 and acknowledge the protocol on stdout. Binary
// payloads stay on disk, independently of JSON control message memory bounds.

func copyAvatarFile(ctx context.Context, source, target string) error {
	in, err := openLocalBlob(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := nativefs.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY|nativefs.NoFollow, 0600)
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(out, contextBlobReader{ctx, in}, make([]byte, 64<<10))
	err = errors.Join(err, out.Close())
	if err != nil {
		_ = os.Remove(target)
	}
	return err
}

func (t *LocalTransform) AvatarFiles(ctx context.Context, operation, reference, referenceMIME, audio, audioMIME, output string) error {
	if t == nil || t.protocol != "files-v1" || !filepath.IsAbs(t.command) || (operation != "photo_avatar" && operation != "lip_sync") {
		return providerFailure("provider_unavailable")
	}
	if (operation == "photo_avatar" && referenceMIME != "image/png" && referenceMIME != "image/jpeg") || (operation == "lip_sync" && referenceMIME != "video/mp4") || (audioMIME != "audio/wav" && audioMIME != "audio/mpeg") {
		return providerFailure("provider_rejected")
	}
	dir, err := nativefs.MkdirTemp("", "studio-avatar-engine-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ref, aud, out := filepath.Join(dir, "reference"), filepath.Join(dir, "audio"), filepath.Join(dir, "output.mp4")
	if err = copyAvatarFile(ctx, reference, ref); err != nil {
		return err
	}
	if err = copyAvatarFile(ctx, audio, aud); err != nil {
		return err
	}
	input, _ := json.Marshal(map[string]any{"protocol": "studio-avatar-files-v1", "operation": operation, "model": t.model, "reference_path": ref, "reference_mime_type": referenceMIME, "audio_path": aud, "audio_mime_type": audioMIME, "output_path": out})
	timeout := t.timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.command)
	cmd.Dir = dir
	cmd.Env = nativeproc.PrivateEnvironment(t.command, dir)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = checkLibraryDispatch(ctx); err != nil {
		return err
	}
	cleanup, err := nativeproc.Start(cmd)
	if err != nil {
		return providerFailure("local_transform_failed")
	}
	defer cleanup()
	ack, readErr := io.ReadAll(io.LimitReader(stdout, 8193))
	if readErr != nil || len(ack) > 8192 {
		cancel()
		_ = stdout.Close()
		_ = cmd.Wait()
		return providerFailure("invalid_provider_response")
	}
	if err = cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return providerFailure("provider_timeout")
			}
			return ctx.Err()
		}
		return providerFailure("local_transform_failed")
	}
	var result struct {
		Protocol string `json:"protocol"`
		Video    string `json:"video_path"`
	}
	// A manifest cannot redirect the server into reading arbitrary engine paths.
	if strictCreativeJSON(ack, &result) != nil || result.Protocol != "studio-avatar-files-v1" || result.Video != "output.mp4" {
		return providerFailure("invalid_provider_response")
	}
	// openLocalBlob rejects symlinks and non-regular files. Freeze an owned copy
	// before format validation and publication; discard every partial result.
	if err = copyAvatarFile(ctx, out, output); err != nil {
		return providerFailure("invalid_provider_response")
	}
	if err = validateProviderMediaFile(ctx, output, "video/mp4"); err != nil {
		_ = os.Remove(output)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return providerFailure("invalid_provider_response")
	}
	return nil
}
