// Package code loads the Go packages of a module and checks them against an
// architecture model.
package code

import (
	"cmp"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/types"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

// maxLoadErrors bounds the package errors that Load reports.
const maxLoadErrors = 10

// A Graph is the Go code of one module, as the checks see it.
type Graph struct {
	// Module is the path of the module.
	Module string
	// Packages are the packages of the module, each with its test files.
	Packages []Package
	// Unread are the paths that match the pattern ./..., but that the checks
	// cannot read: Go files that build constraints exclude or that use cgo,
	// and directories that cannot be listed.
	Unread []string
}

// A Package is one package of the module.
type Package struct {
	// Path is the import path. The files of an external test package belong
	// to the package that they test.
	Path string
	// Files are the Go files of the package, with its test files.
	Files []File
}

// A File is one Go file of a package.
type File struct {
	// Name is the path of the file, relative to the module root, with
	// forward slashes.
	Name string
	// Test reports whether the file is a test file.
	Test bool
	// Imports are the import declarations of the file.
	Imports []Import
	// Symbols are the uses of symbols from other packages.
	Symbols []Symbol
}

// An Import is one import declaration of a file.
type Import struct {
	// Path is the import path.
	Path string
	// Module is the path of the module of the imported package. It is empty
	// for a package of the standard library.
	Module string
	// Line is the line of the declaration.
	Line int
}

// A Symbol is one use of a package-level function or variable of another
// package, or of a builtin function.
type Symbol struct {
	// Name is "importpath.Name", or the name of a builtin function.
	Name string
	// Line is the line of the use.
	Line int
}

// Load loads every package of the Go module in dir, with its test files. A
// module with no package gives a graph with no packages. It first loads the
// packages for the environment of the process. When build constraints
// exclude a Go file, it loads the packages of the unread files again for each
// port that includes one of them.
func Load(dir string) (Graph, error) {
	modPath, err := modulePath(dir)
	if err != nil {
		return Graph{}, err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return Graph{}, fmt.Errorf("resolve %s: %w", dir, err)
	}
	b := graphBuilder{root: root, byPath: make(map[string]*Package), read: make(map[string]bool)}
	if err := b.add(nil, []string{"./..."}); err != nil {
		return Graph{}, err
	}
	fsys := os.DirFS(root)
	missed := unread(fsys, ".", b.read)
	tried := make(map[string]bool)
	for _, p := range ports() {
		pending := slices.DeleteFunc(slices.Clone(missed), func(name string) bool { return tried[name] })
		patterns, matched := p.match(root, pending)
		if len(patterns) == 0 {
			continue
		}
		if err := b.add(p.env(), patterns); err != nil {
			return Graph{}, fmt.Errorf("for %s/%s: %w", p.goos, p.goarch, err)
		}
		for _, name := range matched {
			tried[name] = true
		}
		missed = unread(fsys, ".", b.read)
	}
	return b.graph(modPath, missed), nil
}

// A port is a target operating system and architecture of the go command.
type port struct {
	goos, goarch string
}

// ports returns the ports of "go tool dist list" in Go 1.27: the first-class
// ports first, and then the other ports. A Go file that only an unlisted port
// builds stays unread.
func ports() []port {
	const list = "linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64 linux/386 linux/arm windows/386 " +
		"aix/ppc64 android/386 android/amd64 android/arm android/arm64 dragonfly/amd64 freebsd/386 freebsd/amd64 " +
		"freebsd/arm freebsd/arm64 illumos/amd64 ios/amd64 ios/arm64 js/wasm linux/loong64 linux/mips linux/mips64 " +
		"linux/mips64le linux/mipsle linux/ppc64 linux/ppc64le linux/riscv64 linux/s390x netbsd/386 netbsd/amd64 " +
		"netbsd/arm netbsd/arm64 openbsd/386 openbsd/amd64 openbsd/arm openbsd/arm64 openbsd/ppc64 openbsd/riscv64 " +
		"plan9/386 plan9/amd64 plan9/arm solaris/amd64 wasip1/wasm windows/arm64"
	var result []port
	for pair := range strings.FieldsSeq(list) {
		goos, goarch, _ := strings.Cut(pair, "/")
		result = append(result, port{goos: goos, goarch: goarch})
	}
	return result
}

// match returns the files that the port includes, by their build constraints
// and file names, and the package patterns of their directories. The names
// are relative to the root, with forward slashes. The cgo build tag is off,
// as for a load of another port, but match does not read the imports. Thus it
// can match a file that imports "C", which the load of the port then does
// not read.
func (p port) match(root string, names []string) (patterns, matched []string) {
	ctxt := build.Default
	ctxt.GOOS, ctxt.GOARCH, ctxt.CgoEnabled, ctxt.ToolTags = p.goos, p.goarch, false, nil
	for _, name := range names {
		dir, file := path.Split(name)
		if ok, err := ctxt.MatchFile(filepath.Join(root, filepath.FromSlash(dir)), file); err != nil || !ok {
			continue
		}
		matched = append(matched, name)
		pattern := "."
		if dir != "" {
			pattern = "./" + strings.TrimSuffix(dir, "/")
		}
		if !slices.Contains(patterns, pattern) {
			patterns = append(patterns, pattern)
		}
	}
	return patterns, matched
}

// env returns the environment of the go command for a load of the port.
func (p port) env() []string {
	return append(os.Environ(), "GOOS="+p.goos, "GOARCH="+p.goarch, "CGO_ENABLED=0")
}

// modulePath reads the module path from the go.mod file in dir. It reads the
// file through an os.Root, so the read stays inside dir.
func modulePath(dir string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("open the module directory: %w", err)
	}
	content, err := root.ReadFile("go.mod")
	if err = errors.Join(err, root.Close()); err != nil {
		return "", fmt.Errorf("read the go.mod file: %w", err)
	}
	mod := modfile.ModulePath(content)
	if mod == "" {
		return "", fmt.Errorf("the go.mod file in %s states no module path", dir)
	}
	return mod, nil
}

// load loads the packages of the module in dir that match the patterns, with
// their test variants. A nil env is the environment of the process. It fails
// when a package has an error.
func load(dir string, env []string, patterns []string, mode packages.LoadMode) ([]*packages.Package, error) {
	roots, err := packages.Load(&packages.Config{Mode: mode, Dir: dir, Env: env, Tests: true}, patterns...)
	if err == nil {
		err = packageErrors(roots)
	}
	if err != nil {
		return nil, fmt.Errorf("load the packages in %s: %w", dir, err)
	}
	return roots, nil
}

// packageErrors joins the errors of the packages and of their imports. It
// reports maxLoadErrors of them, and then the number of the others. The go
// command reports a pattern that matches no package as an error of a
// package with no files, which is not an error here.
func packageErrors(roots []*packages.Package) error {
	var errs []error
	packages.Visit(roots, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			if len(p.GoFiles) == 0 && len(p.CompiledGoFiles) == 0 && strings.Contains(e.Msg, "matched no packages") {
				continue
			}
			errs = append(errs, e)
		}
	})
	if len(errs) > maxLoadErrors {
		errs = append(errs[:maxLoadErrors], fmt.Errorf("and %d more errors", len(errs)-maxLoadErrors))
	}
	return errors.Join(errs...)
}

// modulePaths maps the import path of each package in the import graph of the
// roots to the path of its module. A package of the standard library has no
// module, so it has no entry.
func modulePaths(roots []*packages.Package) map[string]string {
	modules := make(map[string]string)
	packages.Visit(roots, nil, func(p *packages.Package) {
		if p.Module != nil {
			modules[p.PkgPath] = p.Module.Path
		}
	})
	return modules
}

// A graphBuilder merges the packages of one or more loads of a module into one
// Package for each import path. The first load that reads a file gives it.
type graphBuilder struct {
	byPath map[string]*Package
	read   map[string]bool
	root   string
}

// add loads the packages of the module that match the patterns with the
// environment env, and adds the files that no earlier load read. A nil env is
// the environment of the process.
func (b *graphBuilder) add(env, patterns []string) error {
	deps, err := load(b.root, env, patterns, packages.NeedName|packages.NeedImports|packages.NeedDeps|packages.NeedModule)
	if err != nil {
		return err
	}
	roots, err := load(b.root, env, patterns, packages.NeedName|packages.NeedFiles|packages.NeedSyntax|packages.NeedTypes|
		packages.NeedTypesInfo|packages.NeedModule|packages.NeedForTest)
	if err != nil {
		return err
	}
	b.addRoots(roots, modulePaths(deps))
	return nil
}

// addRoots merges the package variants that go/packages returns for tests.
// A test variant belongs to the package that it tests. A file outside the
// module root, such as the generated main file of a test binary, is not part
// of the graph.
func (b *graphBuilder) addRoots(roots []*packages.Package, modules map[string]string) {
	for _, p := range roots {
		owner := cmp.Or(p.ForTest, p.PkgPath)
		for _, syntax := range p.Syntax {
			abs := p.Fset.File(syntax.FileStart).Name()
			name, ok := strings.CutPrefix(abs, b.root+string(filepath.Separator))
			if !ok {
				continue
			}
			name = filepath.ToSlash(name)
			if b.read[name] {
				continue
			}
			b.read[name] = true
			gp := b.byPath[owner]
			if gp == nil {
				gp = &Package{Path: owner}
				b.byPath[owner] = gp
			}
			gp.Files = append(gp.Files, fileOf(p, syntax, name, modules))
		}
	}
}

// graph returns the graph of the added packages, with the unread paths.
func (b *graphBuilder) graph(modPath string, unread []string) Graph {
	g := Graph{Module: modPath, Unread: unread}
	for _, owner := range slices.Sorted(maps.Keys(b.byPath)) {
		gp := b.byPath[owner]
		slices.SortFunc(gp.Files, func(x, y File) int { return cmp.Compare(x.Name, y.Name) })
		g.Packages = append(g.Packages, *gp)
	}
	return g
}

// unread returns the Go files under dir that match the pattern ./..., but
// that are not in read, and each directory that fsys cannot list. Like the
// go command, it skips the directories named vendor or testdata, the files
// and directories whose names begin with "." or "_", and the directories of
// other modules.
func unread(fsys fs.FS, dir string, read map[string]bool) []string {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return []string{dir}
	}
	var paths []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		name := path.Join(dir, e.Name())
		switch {
		case e.IsDir():
			if e.Name() != "vendor" && e.Name() != "testdata" && !isModule(fsys, name) {
				paths = append(paths, unread(fsys, name, read)...)
			}
		case strings.HasSuffix(e.Name(), ".go") && !read[name]:
			paths = append(paths, name)
		}
	}
	return paths
}

// isModule reports whether directory dir of fsys holds a go.mod file.
func isModule(fsys fs.FS, dir string) bool {
	_, err := fs.Stat(fsys, path.Join(dir, "go.mod"))
	return err == nil
}

func fileOf(p *packages.Package, syntax *ast.File, name string, modules map[string]string) File {
	f := File{Name: name, Test: strings.HasSuffix(name, "_test.go")}
	for _, spec := range syntax.Imports {
		importPath := p.TypesInfo.PkgNameOf(spec).Imported().Path()
		f.Imports = append(f.Imports, Import{
			Path:   importPath,
			Module: modules[importPath],
			Line:   p.Fset.Position(spec.Pos()).Line,
		})
	}
	ast.Inspect(syntax, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			if symbol := symbolName(p.TypesInfo.Uses[ident], p.Types); symbol != "" {
				f.Symbols = append(f.Symbols, Symbol{Name: symbol, Line: p.Fset.Position(ident.Pos()).Line})
			}
		}
		return true
	})
	return f
}

// symbolName returns the name of a builtin function, or the "importpath.Name"
// of a package-level function or variable of another package. It returns the
// empty string for every other object. A method of a predeclared type, such
// as error.Error, has no package. For a nil package, Scope returns the
// universe scope, which does not hold the method.
func symbolName(obj types.Object, self *types.Package) string {
	switch o := obj.(type) {
	case *types.Builtin:
		return o.Name()
	case *types.Func, *types.Var:
		if pkg := o.Pkg(); pkg != self && pkg.Scope().Lookup(o.Name()) == o {
			return pkg.Path() + "." + o.Name()
		}
	}
	return ""
}
