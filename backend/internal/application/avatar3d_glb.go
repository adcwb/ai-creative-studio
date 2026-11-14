package application

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

type avatar3DProfile struct {
	Nodes  int `json:"nodes"`
	Morphs int `json:"morphs"`
}

// Bound each JSON lexeme and nesting, not the model/file size. GLB binary stays
// on disk. This prevents a single hostile metadata literal exhausting memory.
type avatarJSONReader struct {
	ctx             context.Context
	r               io.Reader
	quoted, escaped bool
	token, depth    int
}

func (r *avatarJSONReader) Read(p []byte) (int, error) {
	if r.ctx != nil && r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	n, e := r.r.Read(p)
	for i, b := range p[:n] {
		if r.quoted {
			r.token++
			if r.escaped {
				r.escaped = false
			} else if b == '\\' {
				r.escaped = true
			} else if b == '"' {
				r.quoted = false
				r.token = 0
			}
		} else {
			switch b {
			case '"':
				r.quoted = true
				r.token = 0
			case '{', '[':
				r.depth++
				r.token = 0
			case '}', ']':
				r.depth--
				r.token = 0
			case ' ', '\n', '\r', '\t', ',', ':':
				r.token = 0
			default:
				r.token++
			}
		}
		if r.token > 16*1024 || r.depth > 64 || r.depth < 0 {
			return i, ErrInvalidProjectInput
		}
	}
	return n, e
}
func validateAvatarGLB(ctx context.Context, path string) (avatar3DProfile, error) {
	if err := ctx.Err(); err != nil {
		return avatar3DProfile{}, err
	}
	bad := func() (avatar3DProfile, error) {
		return avatar3DProfile{}, projectInputError("GLB须为自包含的GLB2角色，包含兼容骨骼及jawOpen/眨眼形变；不接受外链或远端解码器扩展")
	}
	f, e := os.Open(path)
	if e != nil {
		return avatar3DProfile{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return avatar3DProfile{}, e
	}
	header := make([]byte, 20)
	if _, e = io.ReadFull(f, header); e != nil {
		return bad()
	}
	total := int64(binary.LittleEndian.Uint32(header[8:12]))
	length := int64(binary.LittleEndian.Uint32(header[12:16]))
	if string(header[:4]) != "glTF" || binary.LittleEndian.Uint32(header[4:8]) != 2 || total != info.Size() || length < 2 || length%4 != 0 || 20+length+8 > total || string(header[16:]) != "JSON" {
		return bad()
	}
	tail := make([]byte, 8)
	if _, e = f.ReadAt(tail, 20+length); e != nil {
		return bad()
	}
	bin := int64(binary.LittleEndian.Uint32(tail))
	if string(tail[4:]) != "BIN\x00" || bin < 1 || bin%4 != 0 || 28+length+bin != total {
		return bad()
	}
	dec := json.NewDecoder(&avatarJSONReader{ctx: ctx, r: io.NewSectionReader(f, 20, length)})
	dec.UseNumber()
	bones := map[string]bool{}
	morphs := map[string]bool{}
	profile := avatar3DProfile{}
	version := ""
	buffers := 0
	bufferSize := int64(0)
	var walk func(string, int) error
	walk = func(path string, depth int) error {
		if depth > 64 {
			return ErrInvalidProjectInput
		}
		token, e := dec.Token()
		if e != nil {
			return e
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				for dec.More() {
					k, e := dec.Token()
					if e != nil {
						return e
					}
					key, ok := k.(string)
					if !ok || key == "uri" {
						return ErrInvalidProjectInput
					}
					if e = walk(path+"/"+key, depth+1); e != nil {
						return e
					}
				}
				end, e := dec.Token()
				if e != nil || end != json.Delim('}') {
					return ErrInvalidProjectInput
				}
			case '[':
				for dec.More() {
					if path == "/nodes" {
						profile.Nodes++
					}
					if path == "/buffers" {
						buffers++
					}
					if e := walk(path+"/#", depth+1); e != nil {
						return e
					}
				}
				end, e := dec.Token()
				if e != nil || end != json.Delim(']') {
					return ErrInvalidProjectInput
				}
			default:
				return ErrInvalidProjectInput
			}
			return nil
		}
		if s, ok := token.(string); ok {
			if path == "/asset/version" {
				version = s
			}
			if path == "/nodes/#/name" {
				name := strings.TrimPrefix(strings.TrimPrefix(s, "mixamorig:"), "mixamorig")
				switch name {
				case "Hips", "Spine", "Neck", "Head", "LeftArm", "RightArm", "LeftHand", "RightHand", "LeftUpLeg", "RightUpLeg":
					bones[name] = true
				}
			}
			if path == "/meshes/#/extras/targetNames/#" {
				switch s {
				case "jawOpen", "eyeBlinkLeft", "eyeBlinkRight":
					morphs[s] = true
				}
			}
			if path == "/extensionsUsed/#" || path == "/extensionsRequired/#" {
				switch s {
				case "KHR_materials_unlit", "KHR_materials_ior", "KHR_materials_specular", "KHR_texture_transform", "KHR_mesh_quantization":
				default:
					return ErrInvalidProjectInput
				}
			}
		}
		if n, ok := token.(json.Number); ok {
			if path == "/buffers/#/byteLength" {
				bufferSize, e = n.Int64()
				if e != nil {
					return e
				}
			}
			if strings.HasPrefix(path, "/accessors/") && strings.HasSuffix(path, "/count") {
				v, e := n.Int64()
				if e != nil || v < 0 || v > bin {
					return ErrInvalidProjectInput
				}
			}
		}
		return nil
	}
	if e = walk("", 0); e != nil {
		if ctx.Err() != nil {
			return avatar3DProfile{}, ctx.Err()
		}
		return bad()
	}
	if _, e = dec.Token(); !errors.Is(e, io.EOF) {
		return bad()
	}
	if version != "2.0" || buffers != 1 || bufferSize <= 0 || bufferSize > bin || bin-bufferSize > 3 || len(bones) != 10 || len(morphs) != 3 {
		return bad()
	}
	profile.Morphs = len(morphs)
	return profile, nil
}
