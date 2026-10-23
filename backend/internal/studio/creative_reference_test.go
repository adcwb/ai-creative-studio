package studio

import (
	"context"
	"testing"
)

type recordingReferenceImage struct {
	calls int
	data  []byte
}

func (p *recordingReferenceImage) Generate(context.Context, Input) (GenerationResult, error) {
	panic("reference silently dropped")
}
func (p *recordingReferenceImage) GenerateWithReference(_ context.Context, _ Input, data []byte, mime string) (GenerationResult, error) {
	p.calls++
	p.data = append([]byte(nil), data...)
	if mime != "image/png" {
		panic("wrong reference MIME")
	}
	return GenerationResult{Data: syntheticCreativePNG(), MIMEType: "image/png", Width: 640, Height: 360}, nil
}
func TestCreativeReferenceOwnershipAndActualDelivery(t *testing.T) {
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := createProject(t, s)
	media, e := s.media.AddAsset(p.ID, p.SpaceID, "reference-upload", "reference.png", "reference", "", "", "uploaded", syntheticCreativePNG(), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	adapter := &recordingReferenceImage{}
	SetCreativeImage(s, adapter, Capabilities{Provider: "gemini-images", Mode: "real", Ready: true, Model: "synthetic-model"})
	input := CreativeJobInput{Kind: "image", ExpectedVersion: p.Version, ExpectedProvider: "gemini-images", ExpectedModel: "synthetic-model", Prompt: "同一角色下一个镜头", ReferenceAssetID: media.Assets[0].ID}
	job, created, e := s.createCreativeJob(p.SpaceID, p.ID, "reference-create", input)
	if e != nil || !created {
		t.Fatal(e)
	}
	if e = s.executeCreativeJob(context.Background(), &job); e != nil || adapter.calls != 1 || len(adapter.data) == 0 || job.AssetID == "" {
		t.Fatal("reference not delivered", e, job)
	}
	other, _, e := s.CreateProject(p.SpaceID, "reference-other", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	input.ExpectedVersion = other.Version
	if _, _, e = s.createCreativeJob(other.SpaceID, other.ID, "reference-cross-project", input); e == nil {
		t.Fatal("cross project reference accepted")
	}
}
