package application

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMediaReplayVersionAndIsolation(t *testing.T) {
	m, err := openMediaStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	p, err := m.Demo("project", "personal-demo", "demo-key-001", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	same, err := m.Demo("project", "personal-demo", "demo-key-001", 1, nil)
	if err != nil || same.Version != p.Version || same.Assets[0].ID != p.Assets[0].ID {
		t.Fatalf("replay %v", err)
	}
	if _, err = m.Demo("project", "personal-demo", "demo-key-001", 2, nil); !errors.Is(err, errMediaKey) {
		t.Fatal(err)
	}
	if _, err = m.SaveTimeline("project", "personal-demo", "timeline-key", 1, p.Timeline); !errors.Is(err, errMediaVersion) {
		t.Fatal(err)
	}
	p.Timeline.Clips[0].Subtitle = "edited"
	saved, err := m.SaveTimeline("project", "personal-demo", "timeline-save", p.Version, p.Timeline)
	if err != nil {
		t.Fatal(err)
	}
	saved.Timeline.Clips[0].Subtitle = "caller mutation"
	if got := m.Get("project", "personal-demo").Timeline.Clips[0].Subtitle; got != "edited" {
		t.Fatal(got)
	}
	if len(m.Get("project", "team-demo").Assets) != 0 {
		t.Fatal("cross-space leak")
	}
}
func TestMediaUploadRejectsSpoofedAndReplay(t *testing.T) {
	m, err := openMediaStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	if _, err = m.AddAsset("p", "s", "upload-fake", "x", "", "", "", "uploaded", []byte("<svg/>"), "image/png"); !errors.Is(err, errMediaInput) {
		t.Fatal(err)
	}
	data := syntheticPNG(0)
	p, err := m.AddAsset("p", "s", "upload-real", "x", "reference", "A", "", "uploaded", data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.AddAsset("p", "s", "upload-real", "x", "reference", "A", "", "uploaded", data, "image/png")
	if err != nil || len(again.Assets) != 1 || again.Assets[0].ID != p.Assets[0].ID {
		t.Fatal(err)
	}
	if _, _, err = m.file("p", "other", p.Assets[0].ID, false, false); err == nil {
		t.Fatal("cross-space file")
	}
}
func TestMediaTimelineBounds(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	p, err := m.Demo("p", "s", "demo-bounds", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := p.Timeline.Clips[0]
	p.Timeline.Clips[0].CropX = .9
	p.Timeline.Clips[0].CropWidth = .5
	p.Timeline.Clips[0].CropHeight = 1
	if validateTimeline(p, p.Timeline) == nil {
		t.Fatal("invalid crop accepted")
	}
	p.Timeline.Clips[0] = c
	p.Timeline.Clips[0].AssetID = "unknown"
	if validateTimeline(p, p.Timeline) == nil {
		t.Fatal("unknown asset accepted")
	}
}
func TestMediaInterruptedRecoveryAndCorruptRefusal(t *testing.T) {
	dir := t.TempDir()
	m, _ := openMediaStore(dir)
	p, err := m.Demo("p", "s", "demo-recover", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := m.clone()
	p.Exports = append(p.Exports, MediaExport{ID: "abcdefabcdefabcdefabcdefabcdefab", Status: "running", Timeline: p.Timeline})
	d.Projects[mediaKey("s", "p")] = p
	if err = m.commit(d); err != nil {
		t.Fatal(err)
	}
	m.close()
	restored, err := openMediaStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Get("p", "s").Exports[0].Status != "failed" {
		t.Fatal("interrupted export replayed")
	}
	restored.close()
	if err = os.WriteFile(filepath.Join(dir, "media", "state.json"), []byte(`{"version":1,"projects":null,"receipts":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = openMediaStore(dir); err == nil {
		t.Fatal("corrupt reset")
	}
}
func TestMediaPersistenceFailureClosesWrites(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	p, err := m.Demo("p", "s", "demo-failure", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(m.dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(m.dir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = m.SaveTimeline("p", "s", "save-failure", p.Version, p.Timeline); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if m.Available() == nil {
		t.Fatal("did not fail closed")
	}
	if m.Get("p", "s").Version != p.Version {
		t.Fatal("failed write changed memory")
	}
}

func TestMediaAssetReceiptReturnsOriginalAfterLaterSameSourceAsset(t *testing.T) {
	dir := t.TempDir()
	m, err := openMediaStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.AddAsset("project", "personal-demo", "creative-first", "first", "shot", "", "shot-1", "ark-video", syntheticPNG(0), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	original := first.Assets[0].ID
	if _, err = m.AddAsset("project", "personal-demo", "creative-second", "second", "shot", "", "shot-1", "ark-video", syntheticPNG(1), "image/png"); err != nil {
		t.Fatal(err)
	}
	replay, err := m.AddAsset("project", "personal-demo", "creative-first", "first", "shot", "", "shot-1", "ark-video", syntheticPNG(0), "image/png")
	if err != nil || len(replay.Assets) != 2 {
		t.Fatal(err)
	}
	a, err := m.AssetForReceipt("project", "personal-demo", "creative-first")
	if err != nil || a.ID != original {
		t.Fatalf("wrong asset %s, expected %s, err %v", a.ID, original, err)
	}
	m.close()
	m, err = openMediaStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	a, err = m.AssetForReceipt("project", "personal-demo", "creative-first")
	if err != nil || a.ID != original {
		t.Fatal("receipt identity lost across restart", err)
	}
	if _, err = m.AssetForReceipt("another", "personal-demo", "creative-first"); err == nil {
		t.Fatal("cross-project receipt lookup")
	}
	if _, err = m.AssetForReceipt("project", "team-demo", "creative-first"); err == nil {
		t.Fatal("cross-space receipt lookup")
	}
	m.mu.Lock()
	d := m.clone()
	r := d.Receipts["personal-demo:project:creative-first"]
	r.AssetID = ""
	d.Receipts["personal-demo:project:creative-first"] = r
	err = m.commit(d)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.AssetForReceipt("project", "personal-demo", "creative-first"); err == nil {
		t.Fatal("legacy receipt guessed identity")
	}
}
