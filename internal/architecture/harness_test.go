package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Enforce the actual Go dependency boundary, not a naming convention. A new
// concrete adapter gets exactly the same restriction without changing this test.
func TestSharedLayersCannotImportConcreteHarness(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, layer := range []string{"domain", "protocol", "app", "ui", "worker", "harness"} {
		base := filepath.Join(root, "internal", layer)
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if layer == "harness" && entry.IsDir() && path != base {
				return filepath.SkipDir
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if layer == "worker" && filepath.Base(path) == "defaults.go" {
				return nil
			} // Explicit composition root.
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				spec, ok := node.(*ast.ImportSpec)
				if !ok {
					return true
				}
				name, _ := strconv.Unquote(spec.Path.Value)
				if strings.HasPrefix(name, "shioji/internal/harness/") {
					t.Errorf("shared layer %s imports concrete implementation %s", path, name)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
