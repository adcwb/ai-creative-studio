package application

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Pipe-produced WAV uses 0xffffffff for lengths that were unknown during
// generation. Normalize a private validation copy only; the reviewed/accepted
// bytes keep their exact original SHA and the source is never rewritten.
func wavValidationPath(ctx context.Context, path string) (string, func(), error) {
	noop := func() {}
	f, e := openLocalBlob(path)
	if e != nil {
		return "", noop, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return "", noop, e
	}
	var header [12]byte
	if _, e = io.ReadFull(f, header[:]); e != nil {
		return "", noop, e
	}
	declared := binary.LittleEndian.Uint32(header[4:8])
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" || (declared != ^uint32(0) && int64(declared)+8 != info.Size()) {
		return "", noop, providerFailure("invalid_provider_response")
	}
	var dataLengthOffset int64 = -1
	for offset := int64(12); offset+8 <= info.Size(); {
		if e = ctx.Err(); e != nil {
			return "", noop, e
		}
		var chunk [8]byte
		if _, e = f.ReadAt(chunk[:], offset); e != nil {
			return "", noop, e
		}
		n := binary.LittleEndian.Uint32(chunk[4:])
		if string(chunk[:4]) == "data" {
			if n == ^uint32(0) {
				dataLengthOffset = offset + 4
			}
			break
		}
		if n == ^uint32(0) || int64(n) > info.Size()-offset-8 {
			return "", noop, providerFailure("invalid_provider_response")
		}
		offset += 8 + int64(n) + int64(n%2)
	}
	if declared != ^uint32(0) && dataLengthOffset < 0 {
		return path, noop, nil
	}
	if info.Size()-8 > int64(^uint32(0)) {
		return "", noop, providerFailure("invalid_provider_response")
	}
	copyFile, e := os.CreateTemp(filepath.Dir(path), ".decode-wav-")
	if e != nil {
		return "", noop, e
	}
	remove := func() { _ = copyFile.Close(); _ = os.Remove(copyFile.Name()) }
	if _, e = f.Seek(0, 0); e == nil {
		_, e = io.CopyBuffer(copyFile, contextBlobReader{ctx, f}, make([]byte, 64<<10))
	}
	size := make([]byte, 4)
	binary.LittleEndian.PutUint32(size, uint32(info.Size()-8))
	if e == nil {
		_, e = copyFile.WriteAt(size, 4)
	}
	if e == nil && dataLengthOffset >= 0 {
		binary.LittleEndian.PutUint32(size, uint32(info.Size()-dataLengthOffset-4))
		_, e = copyFile.WriteAt(size, dataLengthOffset)
	}
	e = errors.Join(e, copyFile.Close())
	if e != nil {
		remove()
		return "", noop, e
	}
	return copyFile.Name(), remove, nil
}

func validateProviderMediaFile(ctx context.Context, path, mime string) error {
	if _, _, e := validateMedia(path, mime); e != nil {
		return e
	}
	if mime == "audio/wav" {
		checked, cleanup, e := wavValidationPath(ctx, path)
		if e != nil {
			return e
		}
		defer cleanup()
		path = checked
	}
	if e := ffmpeg(ctx, "-protocol_whitelist", "file,pipe", "-xerror", "-err_detect", "explode", "-i", path, "-f", "null", "-"); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return providerFailure("invalid_provider_response")
	}
	return nil
}

type streamingSpeechProvider interface {
	SpeechTo(context.Context, string, io.Writer) error
}
type streamingVideoProvider interface {
	DownloadVideoTo(context.Context, string, io.Writer) error
}

func copyProviderMedia(ctx context.Context, in io.Reader, out io.Writer, mime string) error {
	reader := bufio.NewReaderSize(contextBlobReader{ctx, in}, 64<<10)
	n := 12
	if mime == "audio/wav" {
		n = 44
	}
	prefix, e := reader.Peek(n)
	if e != nil || (mime == "audio/wav" && (string(prefix[:4]) != "RIFF" || string(prefix[8:12]) != "WAVE")) || (mime == "video/mp4" && string(prefix[4:8]) != "ftyp") {
		return providerFailure("invalid_provider_response")
	}
	if _, e = io.CopyBuffer(out, reader, make([]byte, 64<<10)); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return providerFailure("invalid_provider_response")
	}
	return nil
}
func (p *OpenAICreativeProvider) requestMediaTo(ctx context.Context, method, path string, body any, out io.Writer, mime string) error {
	var raw []byte
	var e error
	if body != nil {
		raw, e = json.Marshal(body)
		if e != nil {
			return providerFailure("provider_rejected")
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, p.endpoint+path, bytes.NewReader(raw))
	if e != nil {
		return providerFailure("provider_error")
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Content-Type", "application/json")
	req.GetBody = nil
	response, e := libraryGuardedRequest(p.client, req)
	if e != nil {
		if errors.Is(e, context.DeadlineExceeded) {
			return providerFailure("provider_timeout")
		}
		return providerFailure("provider_error")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := "provider_error"
		switch response.StatusCode {
		case 401, 403:
			code = "provider_auth_failed"
		case 429:
			code = "provider_rate_limited"
		case 400, 404, 413, 422:
			code = "provider_rejected"
		}
		return providerFailure(code)
	}
	return copyProviderMedia(ctx, response.Body, out, mime)
}
func (p *OpenAICreativeProvider) SpeechTo(ctx context.Context, text string, out io.Writer) error {
	if p.speechModel == "" {
		return providerFailure("provider_unavailable")
	}
	text, e := normalizeProjectText(text, "speech", 1, 2000)
	if e != nil {
		return e
	}
	return p.requestMediaTo(ctx, "POST", "/audio/speech", map[string]string{"model": p.speechModel, "input": text, "voice": "alloy", "response_format": "wav"}, out, "audio/wav")
}
func (p *OpenAICreativeProvider) DownloadVideoTo(ctx context.Context, id string, out io.Writer) error {
	if p.videoRetired {
		return providerFailure("provider_retired")
	}
	if !videoIDPattern.MatchString(id) {
		return providerFailure("provider_rejected")
	}
	return p.requestMediaTo(ctx, "GET", "/videos/"+id+"/content", nil, out, "video/mp4")
}
func (p *gatewayCreativeProvider) SpeechTo(ctx context.Context, text string, out io.Writer) error {
	if !configToken.MatchString(p.voice) {
		return providerFailure("provider_rejected")
	}
	text, e := normalizeProjectText(text, "speech", 1, 2000)
	if e != nil {
		return e
	}
	return p.requestMediaTo(ctx, "POST", "/audio/speech", map[string]string{"model": p.model.Model, "input": text, "voice": p.voice, "response_format": "wav"}, out, "audio/wav")
}
func (p *gatewayCreativeProvider) DownloadVideoTo(ctx context.Context, id string, out io.Writer) error {
	if !gatewayVideoID.MatchString(id) {
		return providerFailure("provider_rejected")
	}
	return p.requestMediaTo(ctx, "GET", "/videos/"+id+"/content", nil, out, "video/mp4")
}
func (p runtimeCreative) SpeechTo(ctx context.Context, text string, out io.Writer) error {
	if p.capability != "speech" {
		return errRuntimeDenied
	}
	if stream, ok := p.p.(streamingSpeechProvider); ok {
		return stream.SpeechTo(ctx, text, out)
	}
	b, e := p.p.Speech(ctx, text)
	if e != nil {
		return e
	}
	_, e = out.Write(b)
	return e
}
func (p runtimeCreative) DownloadVideoTo(ctx context.Context, id string, out io.Writer) error {
	if p.capability != "video" || (executionReadOnly(ctx) && (id == "" || id != executionRecoveryUpstream(ctx))) {
		return errRuntimeDenied
	}
	if stream, ok := p.p.(streamingVideoProvider); ok {
		return stream.DownloadVideoTo(ctx, id, out)
	}
	b, e := p.p.DownloadVideo(ctx, id)
	if e != nil {
		return e
	}
	_, e = out.Write(b)
	return e
}
