package code

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/go/packages"
)

func TestLoadFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("loads packages with the go command")
	}
	g, err := Load("testdata/fixture")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := g.Module, "example.com/fixture"; got != want {
		t.Errorf("Module = %q, want %q", got, want)
	}
	var paths []string
	for _, p := range g.Packages {
		paths = append(paths, p.Path)
	}
	wantPaths := []string{"example.com/fixture/cmd/app", "example.com/fixture/internal/core", "example.com/fixture/internal/core/coretest"}
	if diff := cmp.Diff(wantPaths, paths); diff != "" {
		t.Errorf("package paths mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"internal/core/ignored.go"}, g.Unread); diff != "" {
		t.Errorf("Unread mismatch (-want +got):\n%s", diff)
	}
	core := g.Packages[1]
	if got, want := len(core.Files), 2; got != want {
		t.Fatalf("len(core.Files) = %d, want %d", got, want)
	}
	lib, test := core.Files[0], core.Files[1]
	if lib.Test || !test.Test {
		t.Errorf("Test flags = %t, %t, want false, true", lib.Test, test.Test)
	}
	if !slices.Contains(lib.Symbols, Symbol{Name: "os.Getenv", Line: 8}) {
		t.Errorf("core.go symbols = %v, want os.Getenv at line 8", lib.Symbols)
	}
	if !slices.Contains(test.Imports, Import{Path: "example.com/fixture/internal/core/coretest", Module: "example.com/fixture", Line: 7}) {
		t.Errorf("core_test.go imports = %v, want the coretest import", test.Imports)
	}
	if !slices.Contains(lib.Imports, Import{Path: "os", Line: 4}) {
		t.Errorf("core.go imports = %v, want os with no module", lib.Imports)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load("testdata/missing"); err == nil {
		t.Errorf("Load() of a directory with no go.mod error = nil, want an error")
	}
}

func TestLoadPackageError(t *testing.T) {
	if testing.Short() {
		t.Skip("loads packages with the go command")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/broken\n\ngo 1.27.0\n",
		"broken.go": "package broken\n\nfunc f() {\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "broken.go") {
		t.Errorf("Load() of a package with a syntax error = %v, want an error for broken.go", err)
	}
}

func TestPackageErrors(t *testing.T) {
	var many []packages.Error
	var manyMessages []string
	for i := range maxLoadErrors + 2 {
		many = append(many, packages.Error{Msg: fmt.Sprintf("error %d", i)})
		manyMessages = append(manyMessages, fmt.Sprintf("-: error %d", i))
	}
	noMatch := []packages.Error{{Msg: "matched no packages"}}
	tests := []struct {
		name string
		pkgs []*packages.Package
		want []string
	}{
		{"no package for the pattern", []*packages.Package{{Errors: noMatch}}, nil},
		{"go files", []*packages.Package{{GoFiles: []string{"a.go"}, Errors: noMatch}}, []string{"-: matched no packages"}},
		{"compiled go files", []*packages.Package{{CompiledGoFiles: []string{"a.go"}, Errors: noMatch}}, []string{"-: matched no packages"}},
		{"other error", []*packages.Package{{Errors: []packages.Error{{Msg: "bad import"}}}}, []string{"-: bad import"}},
		{"at the bound", []*packages.Package{{Errors: many[:maxLoadErrors]}}, manyMessages[:maxLoadErrors]},
		{"over the bound", []*packages.Package{{Errors: many}}, append(slices.Clone(manyMessages[:maxLoadErrors]), "and 2 more errors")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			if err := packageErrors(tc.pkgs); err != nil {
				got = strings.Split(err.Error(), "\n")
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("packageErrors() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
