package studio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

func syntheticPNG(index int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	colors := []color.RGBA{{38, 47, 90, 255}, {40, 87, 96, 255}, {104, 66, 86, 255}}
	base := colors[index%len(colors)]
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			c := base
			if x > 70+index*30 && x < 300+index*30 && y > 60 && y < 300 {
				c = color.RGBA{190, 145, 90, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
func syntheticWAV(seconds int) []byte {
	const rate = 48000
	n := rate * seconds
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+n*2))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(n*2))
	for i := 0; i < n; i++ {
		v := int16(math.Sin(float64(i)*2*math.Pi*220/rate) * 1800)
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	return b.Bytes()
}
func (m *MediaStore) Demo(project, space, key string, version int, shots []StoryboardShot) (MediaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"demo", version})
	if replay, err := m.check(d, k, key, digest); err != nil || replay {
		return p, err
	}
	if p.Version != version {
		return p, errMediaVersion
	}
	if len(p.Assets) > 110 || m.overQuota(2<<20) {
		return p, errMediaLimit
	}
	if len(shots) == 0 {
		for i := 0; i < 3; i++ {
			shots = append(shots, StoryboardShot{ID: fmt.Sprintf("shot-%d", i+1), DurationSeconds: 2, Dialogue: fmt.Sprintf("合成演示镜头 %d · 非 AI 生成", i+1)})
		}
	}
	if len(shots) > 6 {
		shots = shots[:6]
	}
	paths := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, path := range paths {
				os.Remove(path)
			}
		}
	}()
	add := func(data []byte, mime, kind, name, shot string) (MediaAsset, error) {
		id := mediaID()
		path := filepath.Join(m.dir, id)
		if err := os.WriteFile(path, data, 0600); err != nil {
			return MediaAsset{}, err
		}
		paths = append(paths, path)
		a := MediaAsset{ID: id, Kind: kind, MIMEType: mime, Name: name, Role: "demo", ShotID: shot, Source: "synthetic", FileURL: mediaURL(space, project, "assets/"+id+"/file")}
		p.Assets = append(p.Assets, a)
		return a, nil
	}
	audio, err := add(syntheticWAV(3), "audio/wav", "audio", "合成正弦测试音 · 非配音", "")
	if err != nil {
		return p, err
	}
	p.Assets[len(p.Assets)-1].Duration = 3
	p.Timeline = MediaTimeline{Clips: []MediaClip{}, MusicAssetID: audio.ID, MusicVolume: 0.15}
	for i, shot := range shots {
		a, err := add(syntheticPNG(i), "image/png", "image", fmt.Sprintf("合成色块画面 %d · 非 AI 生成", i+1), shot.ID)
		if err != nil {
			return p, err
		}
		text := shot.Dialogue
		if text == "" {
			text = "合成演示 · 非 AI 生成"
		}
		if len([]rune(text)) > 300 {
			text = string([]rune(text)[:300])
		}
		p.Timeline.Clips = append(p.Timeline.Clips, MediaClip{ID: shot.ID, AssetID: a.ID, Duration: 2, Subtitle: text, AudioVolume: 1})
	}
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest}
	err = m.commit(d)
	committed = err == nil
	return p, err
}
