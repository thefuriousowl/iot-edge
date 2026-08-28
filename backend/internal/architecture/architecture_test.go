package architecture

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestFeatureCoreImportBoundaries(t *testing.T) {
	t.Parallel()

	backendRoot := backendRoot(t)
	tests := []struct {
		name            string
		directory       string
		allowedInternal map[string]bool
	}{
		{
			name:      "auth core",
			directory: filepath.Join(backendRoot, "internal", "auth"),
		},
		{
			name:      "asset core",
			directory: filepath.Join(backendRoot, "internal", "asset"),
		},
		{
			name:      "vGateway core",
			directory: filepath.Join(backendRoot, "internal", "vgateway"),
			allowedInternal: map[string]bool{
				"github.com/thefuriousowl/iot-edge/internal/protocol": true,
			},
		},
		{
			name:      "device core",
			directory: filepath.Join(backendRoot, "internal", "device"),
			allowedInternal: map[string]bool{
				"github.com/thefuriousowl/iot-edge/internal/protocol": true,
			},
		},
		{
			name:      "tag core",
			directory: filepath.Join(backendRoot, "internal", "tag"),
			allowedInternal: map[string]bool{
				"github.com/thefuriousowl/iot-edge/internal/protocol": true,
			},
		},
		{
			name:      "data logger core",
			directory: filepath.Join(backendRoot, "internal", "datalogger"),
		},
		{
			name:      "plugin core",
			directory: filepath.Join(backendRoot, "internal", "plugin"),
		},
		{
			name:      "utility core",
			directory: filepath.Join(backendRoot, "internal", "utility"),
			allowedInternal: map[string]bool{
				"github.com/thefuriousowl/iot-edge/internal/asset":             true,
				"github.com/thefuriousowl/iot-edge/internal/utility/analytics": true,
			},
		},
		{
			name:      "Data Publisher core",
			directory: filepath.Join(backendRoot, "internal", "publisher"),
			allowedInternal: map[string]bool{
				"github.com/thefuriousowl/iot-edge/internal/plugin": true,
				"github.com/thefuriousowl/iot-edge/internal/tag":    true,
			},
		},
		{
			name:      "Energy Plugin",
			directory: filepath.Join(backendRoot, "internal", "plugin", "energy"),
			allowedInternal: map[string]bool{
				"github.com/thefuriousowl/iot-edge/internal/datalogger": true,
				"github.com/thefuriousowl/iot-edge/internal/plugin":     true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			imports := packageImports(t, test.directory)
			for _, importPath := range imports {
				if strings.HasPrefix(importPath, "github.com/gofiber/") ||
					strings.HasPrefix(importPath, "gorm.io/") {
					t.Errorf("core package imports adapter dependency %q", importPath)
				}
				if strings.HasPrefix(importPath, "github.com/thefuriousowl/iot-edge/internal/") &&
					!test.allowedInternal[importPath] {
					t.Errorf("core package imports internal implementation %q", importPath)
				}
			}
		})
	}
}

func backendRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() did not return the test filename")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func packageImports(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		t.Fatalf("finding Go files in %s: %v", directory, err)
	}

	imports := make([]string, 0)
	for _, filename := range entries {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}

		parsed, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing imports from %s: %v", filename, err)
		}
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquoting import in %s: %v", filename, err)
			}
			imports = append(imports, importPath)
		}
	}
	return imports
}
