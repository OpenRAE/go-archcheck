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

func TestLoadPortError(t *testing.T) {
	if testing.Short() {
		t.Skip("loads packages with the go command")
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":         "module example.com/broken\n\ngo 1.27.0\n",
		"p/p.go":         "package p\n",
		"p/p_plan9.go":   "package p\n\nvar x int = \"text\"\n",
		"p/p_windows.go": "package p\n",
	})
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "for plan9/386") || !strings.Contains(err.Error(), "p_plan9.go") {
		t.Errorf("Load() of a package with a type error for plan9 = %v, want an error for plan9/386 and p_plan9.go", err)
	}
}

func TestPorts(t *testing.T) {
	got := ports()
	if len(got) != 47 {
		t.Errorf("len(ports()) = %d, want 47", len(got))
	}
	if got[0] != (port{goos: "linux", goarch: "amd64"}) || got[len(got)-1] != (port{goos: "windows", goarch: "arm64"}) {
		t.Errorf("ports() = %v, want linux/amd64 first and windows/arm64 last", got)
	}
	seen := make(map[port]bool)
	for _, p := range got {
		if seen[p] || p.goos == "" || p.goarch == "" {
			t.Errorf("ports() has the empty or repeated port %v", p)
		}
		seen[p] = true
	}
}

func TestPortMatch(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a/a_windows.go":      "package a\n",
		"a/b_windows.go":      "package a\n",
		"a/ignored.go":        "//go:build ignore\n\npackage a\n",
		"b/b_windows_test.go": "package b\n",
		"c/c_linux.go":        "package c\n",
		"cgo/tag.go":          "//go:build cgo\n\npackage cgo\n",
		"cgo/import.go":       "package cgo\n\nimport \"C\"\n",
		"top_windows.go":      "package top\n",
	})
	windows := port{goos: "windows", goarch: "amd64"}
	tests := []struct {
		name         string
		names        []string
		wantPatterns []string
		wantMatched  []string
	}{
		{"none", nil, nil, nil},
		{
			"one pattern for each directory",
			[]string{"a/a_windows.go", "a/b_windows.go", "b/b_windows_test.go"},
			[]string{"./a", "./b"},
			[]string{"a/a_windows.go", "a/b_windows.go", "b/b_windows_test.go"},
		},
		{"module root", []string{"top_windows.go"}, []string{"."}, []string{"top_windows.go"}},
		{"other port", []string{"c/c_linux.go"}, nil, nil},
		{"ignore tag", []string{"a/ignored.go", "c/c_linux.go"}, nil, nil},
		{"cgo tag is off", []string{"cgo/tag.go"}, nil, nil},
		{"cgo import is not read", []string{"cgo/import.go"}, []string{"./cgo"}, []string{"cgo/import.go"}},
		{"missing file", []string{"d/d_windows.go"}, nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			patterns, matched := windows.match(dir, tc.names)
			if diff := cmp.Diff(tc.wantPatterns, patterns); diff != "" {
				t.Errorf("match(%q) patterns mismatch (-want +got):\n%s", tc.names, diff)
			}
			if diff := cmp.Diff(tc.wantMatched, matched); diff != "" {
				t.Errorf("match(%q) files mismatch (-want +got):\n%s", tc.names, diff)
			}
		})
	}
}

func TestLoadCgoFileOfOtherPort(t *testing.T) {
	if testing.Short() {
		t.Skip("loads packages with the go command")
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"go.mod":       "module example.com/cgo\n\ngo 1.27.0\n",
		"p/p.go":       "package p\n",
		"p/c_plan9.go": "package p\n\nimport \"C\"\n",
	})
	g, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if diff := cmp.Diff([]string{"p/c_plan9.go"}, g.Unread); diff != "" {
		t.Errorf("Unread mismatch (-want +got):\n%s", diff)
	}
}

func TestPortEnv(t *testing.T) {
	env := port{goos: "plan9", goarch: "arm"}.env()
	want := []string{"GOOS=plan9", "GOARCH=arm", "CGO_ENABLED=0"}
	if diff := cmp.Diff(want, env[len(env)-3:]); diff != "" {
		t.Errorf("env() tail mismatch (-want +got):\n%s", diff)
	}
}
