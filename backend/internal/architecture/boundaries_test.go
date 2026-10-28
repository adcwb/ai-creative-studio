package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Business modules may use standard-library contracts and the explicitly listed
// cross-module value types. They must never depend on execution or persistence.
func TestBusinessModuleDependencies(t *testing.T) {
	root := filepath.Join("..", "modules")
	allowed := map[string]map[string]bool{"projects": {"governance": true}, "media": {"publishing": true}}
	const prefix = "github.com/adcwb/ai-creative-studio/backend/internal/modules/"
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		owner := strings.Split(filepath.ToSlash(rel), "/")[0]
		for _, spec := range f.Imports {
			dependency, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			for _, forbidden := range []string{"database", "net/http", "os", "syscall"} {
				if dependency == forbidden || strings.HasPrefix(dependency, forbidden+"/") {
					t.Errorf("%s imports infrastructure dependency %s", rel, dependency)
				}
			}
			if !strings.Contains(strings.Split(dependency, "/")[0], ".") {
				continue
			}
			target := strings.TrimPrefix(dependency, prefix)
			if !strings.HasPrefix(dependency, prefix) || !allowed[owner][target] {
				t.Errorf("%s imports forbidden dependency %s", rel, dependency)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPlatformDoesNotImportBusinessOrBootstrap(t *testing.T) {
	root := filepath.Join("..", "platform")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range f.Imports {
			dependency, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if strings.Contains(dependency, "/backend/internal/") && !strings.Contains(dependency, "/backend/internal/platform/") {
				t.Errorf("%s imports business dependency %s", path, dependency)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
