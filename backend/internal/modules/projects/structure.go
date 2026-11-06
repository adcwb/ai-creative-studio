package projects

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
)

var structureIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Stable authored IDs are opaque. UUIDs from the editor and deterministic
// migrated IDs share this grammar; neither is a canonical series record ID.
func ValidStructureID(id string) bool { return structureIDPattern.MatchString(id) }

// StructureMapping pins the original legacy names and indexes to identities.
// It is stored with the project, never reconstructed using a reordered draft.
type StructureMapping struct {
	CompilerVersion     int                 `json:"compiler_version"`
	SourceScriptVersion int                 `json:"source_script_version"`
	SourceSHA256        string              `json:"source_sha256"`
	Characters          []CharacterIdentity `json:"characters"`
	Scenes              []SceneIdentity     `json:"scenes"`
}
type CharacterIdentity struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	ID    string `json:"id"`
}
type SceneIdentity struct {
	Index int    `json:"index"`
	ID    string `json:"id"`
}

func CloneStructureMappings(in []StructureMapping) []StructureMapping {
	out := append([]StructureMapping(nil), in...)
	for i := range out {
		out[i].Characters = append([]CharacterIdentity{}, in[i].Characters...)
		out[i].Scenes = append([]SceneIdentity{}, in[i].Scenes...)
	}
	return out
}

// CompileStructure is a pure, deterministic upgrade. Callers persist the mapping
// and upgraded script together after a version check; GET never assigns IDs.
func CompileStructure(script Script, namespace string, version int) (Script, StructureMapping, error) {
	script, err := NormalizeScript(script)
	if err != nil {
		return Script{}, StructureMapping{}, err
	}
	if namespace == "" || version < 1 {
		return Script{}, StructureMapping{}, InputError("缺少稳定关联的来源身份")
	}
	b, err := json.Marshal(script)
	if err != nil {
		return Script{}, StructureMapping{}, err
	}
	digest := sha256.Sum256(b)
	m := StructureMapping{CompilerVersion: 1, SourceScriptVersion: version, SourceSHA256: hex.EncodeToString(digest[:]), Characters: []CharacterIdentity{}, Scenes: []SceneIdentity{}}
	id := func(kind string, index int) string {
		b, _ := json.Marshal([]string{"studio-structure-v1", namespace, strconv.Itoa(version), m.SourceSHA256, kind, strconv.Itoa(index)})
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:16])
	}
	names := map[string]string{}
	for i := range script.Characters {
		c := &script.Characters[i]
		if script.StructureVersion == 0 {
			c.ID = id("character", i+1)
		}
		names[c.Name] = c.ID
		m.Characters = append(m.Characters, CharacterIdentity{i + 1, c.Name, c.ID})
	}
	for i := range script.Scenes {
		s := &script.Scenes[i]
		if script.StructureVersion == 0 {
			s.ID = id("scene", i+1)
			for j := range s.Dialogue {
				s.Dialogue[j].CharacterID = names[s.Dialogue[j].Character]
			}
		}
		m.Scenes = append(m.Scenes, SceneIdentity{i + 1, s.ID})
	}
	script.StructureVersion = 2
	return script, m, nil
}

func SceneIDs(script Script) []string {
	out := make([]string, 0, len(script.Scenes))
	for _, s := range script.Scenes {
		out = append(out, s.ID)
	}
	return out
}

// BindStoryboard keeps the scene list of the source revision. It must never be
// derived from the current script when the board references an earlier version.
func BindStoryboard(shots []StoryboardShot, sourceIDs []string) ([]StoryboardShot, error) {
	out, err := NormalizeStoryboardShots(shots)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, id := range sourceIDs {
		if !structureIDPattern.MatchString(id) || seen[id] {
			return nil, InputError("分镜来源场次标识缺失或重复")
		}
		seen[id] = true
	}
	for i := range out {
		s := &out[i]
		if s.SceneIndex == 0 {
			continue
		}
		if s.SceneIndex > len(sourceIDs) {
			return nil, InputError("镜头引用了不存在的来源场次")
		}
		id := sourceIDs[s.SceneIndex-1]
		if s.SceneID != "" && s.SceneID != id {
			return nil, InputError("镜头场次标识与来源索引不匹配，请保留草稿并重新读取")
		}
		s.SceneID = id
	}
	return out, nil
}
