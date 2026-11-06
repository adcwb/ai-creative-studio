package application

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayFileReferenceAndLargeMaskPreserveExactBytes(t *testing.T) {
	dir := t.TempDir()
	ref, mask := filepath.Join(dir, "ref.png"), filepath.Join(dir, "mask.png")
	img := image.NewNRGBA(image.Rect(0, 0, 4096, 3072))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:i+4], []byte{31, 87, 149, 255})
	}
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	write := func(path string) {
		f, e := os.Create(path)
		if e != nil {
			t.Fatal(e)
		}
		if e = encoder.Encode(f, img); e != nil {
			t.Fatal(e)
		}
		f.Close()
	}
	write(ref)
	for y := 0; y < 3072; y++ {
		for x := 2048; x < 4096; x++ {
			img.Pix[y*img.Stride+x*4+3] = 0
		}
	}
	write(mask)
	img = nil
	hashes := map[string]string{}
	sizes := map[string]int64{}
	for name, path := range map[string]string{"image[]": ref, "mask": mask} {
		f, e := os.Open(path)
		if e != nil {
			t.Fatal(e)
		}
		h := sha256.New()
		n, e := io.Copy(h, f)
		f.Close()
		if e != nil || n <= 32<<20 {
			t.Fatal("fixture must exceed retired thresholds", name, n, e)
		}
		hashes[name] = hex.EncodeToString(h.Sum(nil))
		sizes[name] = n
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/images/edits" || r.Method != "POST" {
			t.Error("wrong request")
			w.WriteHeader(400)
			return
		}
		form, e := r.MultipartReader()
		if e != nil {
			t.Error(e)
			return
		}
		found := map[string]bool{}
		for {
			part, e := form.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Error(e)
				return
			}
			name := part.FormName()
			if expected, ok := hashes[name]; ok {
				h := sha256.New()
				n, e := io.CopyBuffer(h, part, make([]byte, 64<<10))
				if e != nil || n != sizes[name] || hex.EncodeToString(h.Sum(nil)) != expected {
					t.Error("reference bytes changed", name, e)
				}
				found[name] = true
			} else {
				_, _ = io.Copy(io.Discard, part)
			}
		}
		if !found["image[]"] || !found["mask"] {
			t.Error("missing inputs")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(syntheticPNG(0))}}})
	}))
	defer server.Close()
	p := gatewayImageProvider{api: newCreativeProvider("synthetic", server.URL, server.Client()), model: GatewayModel{Model: "synthetic-edit", Capabilities: []string{"image", "image_edit"}}}
	result, e := p.GenerateWithReferenceFiles(context.Background(), Input{Prompt: "Synthetic large-file image edit", Kind: "general", Style: "natural", Ratio: "1:1"}, ref, "image/png", mask)
	if e != nil || len(result.Data) == 0 || calls != 1 {
		t.Fatal("file edit", e, calls)
	}
	// Valid-sized PNGs still require both transparent and opaque regions.
	if e = validateEditMaskFiles(ref, "image/png", ref); e == nil {
		t.Fatal("opaque mask accepted")
	}
}

func TestGatewayFileJPEGReferenceKeepsItsMediaType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original.jpg")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = jpeg.Encode(f, image.NewGray(image.Rect(0, 0, 32, 32)), nil); e != nil {
		t.Fatal(e)
	}
	f.Close()
	sent := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts, e := r.MultipartReader()
		if e != nil {
			t.Error(e)
			return
		}
		for {
			part, e := parts.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Error(e)
				return
			}
			if part.FormName() == "image[]" {
				sent = true
				if part.Header.Get("Content-Type") != "image/jpeg" || !strings.HasSuffix(part.FileName(), ".jpg") {
					t.Error("JPEG transport metadata changed", part.Header)
				}
			}
			_, _ = io.Copy(io.Discard, part)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(syntheticPNG(0))}}})
	}))
	defer server.Close()
	p := gatewayImageProvider{api: newCreativeProvider("synthetic", server.URL, server.Client()), model: GatewayModel{Model: "synthetic-edit", Capabilities: []string{"image", "image_edit"}}}
	if _, e = p.GenerateWithReferenceFiles(context.Background(), Input{Prompt: "Synthetic JPEG edit", Kind: "general", Style: "natural", Ratio: "1:1"}, path, "image/jpeg", ""); e != nil || !sent {
		t.Fatal("JPEG reference failed", e)
	}
}
