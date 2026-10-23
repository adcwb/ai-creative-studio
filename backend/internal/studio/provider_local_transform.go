package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// LocalTransform is an operator-installed executable, never a browser-supplied
// command. It receives only bounded synthetic/user-authorized media and a model
// identifier via JSON stdin; the parent's credentials/environment are not inherited.
// Actual aligner/lip-sync models are deliberately not bundled or downloaded.
type LocalTransform struct{ command, model string }

func configureLocalTransform(prefix, provider string) (*LocalTransform, Capabilities) {
	command, model := os.Getenv(prefix+"_COMMAND"), os.Getenv(prefix+"_MODEL")
	c := Capabilities{Provider: "unavailable", Mode: "unavailable", Message: "未安装/配置本地模型适配；不模拟模型效果。"}
	if command == "" {
		return nil, c
	}
	c.Provider = provider
	c.Model = model
	c.Mode = "real"
	stat, err := os.Stat(command)
	if !filepath.IsAbs(command) || err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0111 == 0 || !configToken.MatchString(model) {
		c.ReasonCode = "invalid_local_transform_configuration"
		return nil, c
	}
	c.localExecution = true
	c.Ready = true
	c.Message = "本地可替换模型适配已配置，准确率/口型质量未验证；结果需人工复核。"
	return &LocalTransform{command: command, model: model}, c
}
func (t *LocalTransform) run(ctx context.Context, input any, maxOutput int) ([]byte, error) {
	if t == nil || !filepath.IsAbs(t.command) {
		return nil, providerFailure("provider_unavailable")
	}
	data, err := json.Marshal(input)
	if err != nil || len(data) > 48<<20 {
		return nil, providerFailure("provider_rejected")
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "studio-transform-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, t.command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + dir}
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, providerFailure("local_transform_failed")
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, int64(maxOutput)+1))
	if readErr != nil || len(out) > maxOutput {
		cancel()
		_ = stdout.Close()
		_ = cmd.Wait()
		return nil, providerFailure("invalid_provider_response")
	}
	if err = cmd.Wait(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, providerFailure("provider_timeout")
		}
		return nil, providerFailure("local_transform_failed")
	}
	return out, nil
}

type ForcedAlignmentProvider interface {
	Align(context.Context, []byte, string, float64, string) (AlignmentResult, error)
}

func ConfigureForcedAlignmentFromEnv() (*LocalTransform, Capabilities) {
	return configureLocalTransform("FORCED_ALIGNMENT", "local-forced-alignment")
}
func (t *LocalTransform) Transcribe(context.Context, []byte, string, float64) (AlignmentResult, error) {
	return AlignmentResult{}, providerFailure("unsupported_capability")
}
func (t *LocalTransform) Align(ctx context.Context, audio []byte, mime string, duration float64, text string) (AlignmentResult, error) {
	if len(audio) == 0 || len(audio) > 24<<20 || (mime != "audio/wav" && mime != "audio/mpeg") || len([]rune(text)) < 1 || len([]rune(text)) > 2000 {
		return AlignmentResult{}, providerFailure("provider_rejected")
	}
	b, err := t.run(ctx, map[string]any{"operation": "forced_alignment", "model": t.model, "audio": audio, "mime_type": mime, "duration": duration, "text": text}, 1<<20)
	if err != nil {
		return AlignmentResult{}, err
	}
	var out struct {
		Cues []SubtitleCue `json:"cues"`
	}
	if strictCreativeJSON(b, &out) != nil || len(out.Cues) == 0 || validateSubtitleCues(out.Cues, duration) != nil {
		return AlignmentResult{}, providerFailure("invalid_provider_response")
	}
	var got strings.Builder
	for _, cue := range out.Cues {
		got.WriteString(cue.Text)
	}
	normalize := func(s string) string { return strings.Join(strings.Fields(s), "") }
	if normalize(got.String()) != normalize(text) {
		return AlignmentResult{}, providerFailure("alignment_text_mismatch")
	}
	return AlignmentResult{Cues: out.Cues, TimingBasis: "forced-alignment"}, nil
}
func SetForcedAlignmentProvider(s *FileStore, p *LocalTransform, caps Capabilities) {
	a := s.media.alignment
	a.mu.Lock()
	defer a.mu.Unlock()
	if p != nil {
		a.forced = p
	}
	a.forcedCaps = caps
}

type LipSyncProvider interface {
	Sync(context.Context, []byte, []byte, string) ([]byte, error)
}

func ConfigureLipSyncFromEnv() (*LocalTransform, Capabilities) {
	return configureLocalTransform("LIP_SYNC", "local-lip-sync")
}
func (t *LocalTransform) Sync(ctx context.Context, video, audio []byte, audioMIME string) ([]byte, error) {
	if len(video) < 12 || len(video) > 10<<20 || string(video[4:8]) != "ftyp" || len(audio) == 0 || len(audio) > 10<<20 || (audioMIME != "audio/wav" && audioMIME != "audio/mpeg") {
		return nil, providerFailure("provider_rejected")
	}
	b, err := t.run(ctx, map[string]any{"operation": "lip_sync", "model": t.model, "video": video, "audio": audio, "audio_mime_type": audioMIME}, 45<<20)
	if err != nil {
		return nil, err
	}
	var out struct {
		Video []byte `json:"video"`
	}
	if strictCreativeJSON(b, &out) != nil || len(out.Video) < 12 || len(out.Video) > 32<<20 || string(out.Video[4:8]) != "ftyp" {
		return nil, providerFailure("invalid_provider_response")
	}
	return out.Video, nil
}
func SetCreativeLipSync(s *FileStore, p *LocalTransform, caps Capabilities) {
	if p != nil {
		s.creative.lipSync = p
	}
	s.creative.lipSyncCaps = caps
}

// SoundEffect uses an explicitly installed model runner. It does not use speech
// synthesis or synthetic tones as a substitute for a sound-effect model.
func ConfigureSoundEffectFromEnv() (*LocalTransform, Capabilities) {
	return configureLocalTransform("SOUND_EFFECT", "local-sound-effect")
}
func (t *LocalTransform) Music(ctx context.Context, prompt string) ([]byte, error) {
	prompt, err := normalizeProjectText(prompt, "音效描述", 3, 2000)
	if err != nil {
		return nil, err
	}
	b, err := t.run(ctx, map[string]any{"operation": "sound_effect", "model": t.model, "prompt": prompt}, 15<<20)
	if err != nil {
		return nil, err
	}
	var out struct {
		Audio []byte `json:"audio"`
	}
	if strictCreativeJSON(b, &out) != nil || len(out.Audio) < 44 || len(out.Audio) > 10<<20 || string(out.Audio[:4]) != "RIFF" || string(out.Audio[8:12]) != "WAVE" {
		return nil, providerFailure("invalid_provider_response")
	}
	return out.Audio, nil
}
func SetCreativeSoundEffect(s *FileStore, p *LocalTransform, caps Capabilities) {
	if p != nil {
		s.creative.soundEffect = p
	}
	s.creative.soundEffectCaps = caps
}
