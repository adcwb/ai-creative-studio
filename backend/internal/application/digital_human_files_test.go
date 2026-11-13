package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const avatarFixturePrefix = "studio-avatar-native-fixture"

// A copied native test executable works on Windows without a shell/Python.
// Synthetic color frames test the protocol, not a model's avatar quality.
func avatarFileFixtureCommand() error {
	var in struct {
		Protocol, Operation, Model string
		ReferencePath              string `json:"reference_path"`
		AudioPath                  string `json:"audio_path"`
		OutputPath                 string `json:"output_path"`
	}
	if json.NewDecoder(os.Stdin).Decode(&in) != nil || in.Protocol != "studio-avatar-files-v1" || (in.Operation != "photo_avatar" && in.Operation != "lip_sync") || os.Getenv("STUDIO_TEST_SECRET") != "" {
		return errors.New("invalid fixture input")
	}
	if in.Model == "timeout" {
		time.Sleep(time.Minute)
		return nil
	}
	if in.Model == "escape" {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"protocol": in.Protocol, "video_path": "../unowned.mp4"})
	}
	if in.Model == "invalid" {
		_ = os.WriteFile(in.OutputPath, []byte("invalid bytes"), 0600)
	} else {
		if _, err := os.Stat(in.ReferencePath); err != nil {
			return err
		}
		_, duration, err := validateMedia(in.AudioPath, "audio/wav")
		if err != nil {
			return err
		}
		if err = ffmpeg(context.Background(), "-f", "lavfi", "-i", "color=c=blue:s=320x320:r=2:d="+seconds(duration), "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", in.OutputPath); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"protocol": in.Protocol, "video_path": "output.mp4"})
}
func fileAvatarExecutable(t *testing.T) string {
	t.Helper()
	name := avatarFixturePrefix
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	err = errors.Join(err, out.Close())
	if err != nil {
		t.Fatal(err)
	}
	return path
}
func TestDigitalHumanFilesLargeInputAndLongOutput(t *testing.T) {
	s := testStore(t)
	p, _, in, _ := avatarFixture(t, s, "personal-demo")
	audio := filepath.Join(t.TempDir(), "long.wav")
	if err := ffmpeg(context.Background(), "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=96000:duration=33", "-ac", "2", "-c:a", "pcm_s16le", audio); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(audio); err != nil || info.Size() <= 10<<20 {
		t.Fatal("fixture must cross old input bound", info, err)
	}
	media, err := s.media.addAssetFile(context.Background(), p.ID, p.SpaceID, "long-authorized-audio", "Long authorized voice", "voice", "", "", "synthetic", audio, "audio/wav", nil)
	if err != nil {
		t.Fatal(err)
	}
	in.AudioAssetID = media.Assets[len(media.Assets)-1].ID
	t.Setenv("STUDIO_TEST_SECRET", "synthetic-private-value")
	t.Setenv("DIGITAL_HUMAN_PHOTO_COMMAND", fileAvatarExecutable(t))
	t.Setenv("DIGITAL_HUMAN_PHOTO_MODEL", "synthetic")
	t.Setenv("DIGITAL_HUMAN_PHOTO_PROTOCOL", "files-v1")
	t.Setenv("DIGITAL_HUMAN_LICENSE_ACK", "true")
	ConfigureDigitalHumanFromEnv(s)
	in.ExpectedProvider = s.creative.avatarPhotoCaps.Provider
	in.ExpectedModel = s.creative.avatarPhotoCaps.Model
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "long-avatar-job", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	asset, err := s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+job.ID)
	if err != nil || asset.Duration < 32.8 || asset.Duration > 33.3 || asset.Kind != "video" {
		t.Fatal("long output not complete", asset, err)
	}
	if _, _, err = s.createCreativeJob(p.SpaceID, p.ID, "long-avatar-job", in); err != nil {
		t.Fatal("original request replay", err)
	}
}
func TestDigitalHumanFilesRejectEscapeInvalidAndTimeout(t *testing.T) {
	command := fileAvatarExecutable(t)
	dir := t.TempDir()
	ref, audio := filepath.Join(dir, "reference.png"), filepath.Join(dir, "audio.wav")
	if err := os.WriteFile(ref, pngFixture(t, 320, 320), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ffmpeg(context.Background(), "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "pcm_s16le", audio); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"escape", "invalid", "timeout"} {
		t.Run(model, func(t *testing.T) {
			deadline := 10 * time.Second
			if model == "timeout" {
				deadline = 250 * time.Millisecond
			}
			provider := &LocalTransform{command: command, model: model, protocol: "files-v1", timeout: deadline}
			out := filepath.Join(t.TempDir(), "result.mp4")
			err := provider.AvatarFiles(context.Background(), "photo_avatar", ref, "image/png", audio, "audio/wav", out)
			if err == nil {
				t.Fatal("unsafe output accepted")
			}
			if _, e := os.Stat(out); e == nil {
				t.Fatal("partial output retained")
			}
			if model != "timeout" {
				var failure *ProviderError
				if !errors.As(err, &failure) || failure.Code != "invalid_provider_response" {
					t.Fatal("fixture must reach output validation", err)
				}
			}
			if model == "timeout" {
				var failure *ProviderError
				if !errors.As(err, &failure) || failure.Code != "provider_timeout" {
					t.Fatal(err)
				}
			}
		})
	}
	t.Setenv("DIGITAL_HUMAN_PHOTO_COMMAND", command)
	t.Setenv("DIGITAL_HUMAN_PHOTO_MODEL", "synthetic")
	t.Setenv("DIGITAL_HUMAN_PHOTO_PROTOCOL", "shell")
	if p, c := configureLocalTransform("DIGITAL_HUMAN_PHOTO", "local-photo-avatar"); p != nil || c.Ready {
		t.Fatal("unknown protocol enabled")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatal("engine changed caller files")
	}
}

func TestDigitalHumanRevokedMemberNeverDispatches(t *testing.T) {
	s := testStore(t)
	base, _, a, actor := workflowFixture(t, s)
	p, _, in, video := avatarFixture(t, s, base.SpaceID)
	in.consentActor = actor
	provider := &avatarFixtureProvider{video: video}
	s.creative.avatarPhoto = provider
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "member-avatar", in)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	data := a.clone()
	org := data.Organizations[p.SpaceID]
	delete(org.Members, actor)
	org.Version++
	data.Organizations[p.SpaceID] = org
	err = a.commit(data)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); !errors.Is(err, errRuntimeDenied) || provider.calls != 0 {
		t.Fatal("revoked actor dispatched", provider.calls, err)
	}
	if code, _ := creativeFailure(err); code != "model_runtime_denied" {
		t.Fatal("revocation reported as upstream failure", code)
	}
}

func TestDigitalHumanFileVideoRoute(t *testing.T) {
	s := testStore(t)
	p, _, in, video := avatarFixture(t, s, "personal-demo")
	state, err := s.media.AddAsset(p.ID, p.SpaceID, "file-reference-video", "Authorized video", "reference", "", "", "synthetic", video, "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	person, _, err := s.createDigitalHuman(p.SpaceID, p.ID, "file-video-person", "synthetic-actor", DigitalHumanInput{Name: "Video reference", ReferenceAssetID: state.Assets[len(state.Assets)-1].ID, ConsentReference: "synthetic-video-permission", LikenessConfirmed: true, SinglePersonConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIP_SYNC_COMMAND", fileAvatarExecutable(t))
	t.Setenv("LIP_SYNC_MODEL", "synthetic")
	t.Setenv("LIP_SYNC_PROTOCOL", "files-v1")
	runner, caps := ConfigureLipSyncFromEnv()
	if !caps.Ready {
		t.Fatal(caps)
	}
	SetCreativeLipSync(s, runner, caps)
	in.AvatarID = person.ID
	in.ExpectedProvider = caps.Provider
	in.ExpectedModel = caps.Model
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "file-lip-sync-job", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	asset, err := s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+job.ID)
	if err != nil || asset.Kind != "video" || asset.ID == person.ReferenceAssetID || asset.Duration < 0.9 || job.AssetID != asset.ID {
		t.Fatal("file video route did not compose authorized media", asset, err)
	}
}
