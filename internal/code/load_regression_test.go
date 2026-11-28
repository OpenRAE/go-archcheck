package code

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestRegressionLoadReadsFilesOfOtherPorts checks that Load reads the Go files
// that build constraints exclude for the host, on every host.
func TestRegressionLoadReadsFilesOfOtherPorts(t *testing.T) {
	if testing.Short() {
		t.Skip("loads packages with the go command")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":               "module example.com/ports\n\ngo 1.27.0\n",
		"p/p.go":               "package p\n",
		"p/p_linux.go":         "package p\n\nimport \"os\"\n\nvar _ = os.Getpid\n",
		"p/p_darwin.go":        "package p\n\nimport \"os\"\n\nvar _ = os.Getppid\n",
		"p/p_windows.go":       "package p\n\nimport \"os\"\n\nvar _ = os.Getuid\n",
		"p/p_windows_test.go":  "package p\n\nimport \"testing\"\n\nfunc TestP(t *testing.T) {}\n",
		"p/ignored.go":         "//go:build ignore\n\npackage p\n",
		"win/win_windows.go":   "package win\n",
		"plan9/plan9_plan9.go": "package plan9\n",
	}
	writeFiles(t, dir, files)
	g, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := make(map[string][]string)
	for _, p := range g.Packages {
		for _, f := range p.Files {
			got[p.Path] = append(got[p.Path], f.Name)
		}
	}
	want := map[string][]string{
		"example.com/ports/p":     {"p/p.go", "p/p_darwin.go", "p/p_linux.go", "p/p_windows.go", "p/p_windows_test.go"},
		"example.com/ports/plan9": {"plan9/plan9_plan9.go"},
		"example.com/ports/win":   {"win/win_windows.go"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("package files mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"p/ignored.go"}, g.Unread); diff != "" {
		t.Errorf("Unread mismatch (-want +got):\n%s", diff)
	}
}

// writeFiles writes each file, by its slash-separated name relative to dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
