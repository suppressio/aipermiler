package codex_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/codex"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

type tree struct {
	d          codex.Discovery
	root, home string
	env        map[string]string
	links      map[string]string
	noExec     map[string]bool
}

// simulatedInfo gives fixture files the executable bits of the simulated OS
// instead of the host's: on a Windows host os.Stat never reports them, so the
// Linux/macOS cases would fail there.
type simulatedInfo struct {
	fs.FileInfo
	noExec bool
}

func (i simulatedInfo) Mode() fs.FileMode {
	mode := i.FileInfo.Mode()
	if !mode.IsRegular() {
		return mode
	}
	if i.noExec {
		return mode&^0o777 | 0o600
	}
	return mode&^0o777 | 0o700
}

func newTree(t *testing.T, goos, arch string) *tree {
	t.Helper()
	root := t.TempDir()
	tree := &tree{root: root, home: filepath.Join(root, "home"), env: map[string]string{}, links: map[string]string{}, noExec: map[string]bool{}}
	tree.d = codex.Discovery{
		ReadHeader: func(path string) ([4]byte, error) {
			var header [4]byte
			data, err := os.ReadFile(tree.local(path))
			if err != nil {
				return header, err
			}
			copy(header[:], data)
			return header, nil
		},
		Getenv: func(key string) string { return tree.env[key] },
		Stat: func(path string) (fs.FileInfo, error) {
			info, err := os.Stat(tree.local(path))
			if err != nil {
				return nil, err
			}
			return simulatedInfo{info, tree.noExec[path]}, nil
		},
		ReadDir: func(path string) ([]os.DirEntry, error) { return os.ReadDir(tree.local(path)) },
		EvalSymlinks: func(path string) (string, error) {
			if target, ok := tree.links[path]; ok {
				return target, nil
			}
			return filepath.EvalSymlinks(tree.local(path))
		},
		UserHomeDir: func() (string, error) { return tree.home, nil }, GOOS: goos, GOARCH: arch,
	}
	return tree
}
func (tree *tree) local(path string) string {
	if path == tree.root || strings.HasPrefix(path, tree.root+string(filepath.Separator)) {
		return path
	}
	// Fixed system prefixes are mirrored inside the temporary tree, never read on the host.
	return filepath.Join(tree.root, "system", strings.TrimPrefix(strings.TrimPrefix(path, filepath.VolumeName(path)), string(filepath.Separator)))
}
func (tree *tree) file(t *testing.T, path string) string {
	t.Helper()
	local := tree.local(path)
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		t.Fatal(err)
	}
	header := []byte{0x7f, 'E', 'L', 'F'}
	if tree.d.GOOS == "windows" {
		header = []byte{'M', 'Z', 0, 0}
	}
	if tree.d.GOOS == "darwin" {
		header = []byte{0xcf, 0xfa, 0xed, 0xfe}
	}
	if err := os.WriteFile(local, append(header, []byte("synthetic native binary")...), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
func nativeFile(root, pkg, triple, name string) string {
	return filepath.Join(root, "@openai", "codex", "node_modules", "@openai", pkg, "vendor", triple, "bin", name)
}
func assertDiscover(t *testing.T, tree *tree, explicit, want string) {
	t.Helper()
	got, err := codex.DiscoverForTest(tree.d, explicit)
	if want == "" {
		var e *usage.Error
		if !errors.As(err, &e) || e.Code != usage.CodeUnavailable {
			t.Fatalf("discover = %q, %#v", got, err)
		}
	} else if err != nil || got != want {
		t.Fatalf("discover = %q, %v; want %q", got, err, want)
	}
}

func TestDiscoveryOrder(t *testing.T) {
	t.Parallel()
	goos, filename, pkg, triple, platform := runtime.GOOS, "codex", "codex-linux-x64", "x86_64-unknown-linux-musl", "linux-x86_64"
	if goos == "darwin" {
		pkg, triple, platform = "codex-darwin-x64", "x86_64-apple-darwin", "macos-x86_64"
	}
	if goos == "windows" {
		filename, pkg, triple, platform = "codex.exe", "codex-win32-x64", "x86_64-pc-windows-msvc", "windows-x86_64"
	}
	tree := newTree(t, goos, "amd64")
	explicit := tree.file(t, filepath.Join(tree.root, "explicit", filename))
	path1 := tree.file(t, filepath.Join(tree.root, "path1", filename))
	path2 := tree.file(t, filepath.Join(tree.root, "path2", filename))
	prefix := filepath.Join(tree.root, "npm")
	modules := filepath.Join(prefix, "lib", "node_modules")
	if goos == "windows" {
		modules = filepath.Join(prefix, "node_modules")
	}
	npm := tree.file(t, nativeFile(modules, pkg, triple, filename))
	extension := tree.file(t, filepath.Join(tree.home, ".vscode", "extensions", "openai.chatgpt-26.1021.22027", "bin", platform, filename))
	// A quoted entry is valid only on Windows, where it is common for paths with spaces.
	tree.env["PATH"] = filepath.Dir(path1) + ":" + filepath.Dir(path2)
	if goos == "windows" {
		tree.env["PATH"] = filepath.Dir(path1) + ";" + `"` + filepath.Dir(path2) + `"`
	}
	tree.env["NPM_CONFIG_PREFIX"] = prefix
	assertDiscover(t, tree, explicit, explicit)
	assertDiscover(t, tree, filepath.Join(tree.root, "missing"), "")
	assertDiscover(t, tree, "relative", "")
	for _, want := range []string{path1, path2, npm, extension} {
		assertDiscover(t, tree, "", want)
		if err := os.Remove(tree.local(want)); err != nil {
			t.Fatal(err)
		}
	}
	assertDiscover(t, tree, "", "")
}

func TestDiscoveryNpmLocations(t *testing.T) {
	t.Parallel()
	root := string(filepath.Separator)
	tests := []struct {
		name, goos, arch, pkg, triple, filename, key string
		prefix                                       func(*tree) string
		modules                                      func(string) string
	}{
		{"linux upper prefix", "linux", "amd64", "codex-linux-x64", "x86_64-unknown-linux-musl", "codex", "NPM_CONFIG_PREFIX", func(tr *tree) string { return filepath.Join(tr.root, "npm") }, func(p string) string { return filepath.Join(p, "lib", "node_modules") }},
		{"linux lower prefix arm64", "linux", "arm64", "codex-linux-arm64", "aarch64-unknown-linux-musl", "codex", "npm_config_prefix", func(tr *tree) string { return filepath.Join(tr.root, "npm") }, func(p string) string { return filepath.Join(p, "lib", "node_modules") }},
		{"linux usr", "linux", "amd64", "codex-linux-x64", "x86_64-unknown-linux-musl", "codex", "", func(*tree) string { return filepath.Join(root, "usr", "lib", "node_modules") }, func(p string) string { return p }},
		{"linux usr local", "linux", "amd64", "codex-linux-x64", "x86_64-unknown-linux-musl", "codex", "", func(*tree) string { return filepath.Join(root, "usr", "local", "lib", "node_modules") }, func(p string) string { return p }},
		{"linux home", "linux", "amd64", "codex-linux-x64", "x86_64-unknown-linux-musl", "codex", "", func(tr *tree) string { return filepath.Join(tr.home, ".npm-global", "lib", "node_modules") }, func(p string) string { return p }},
		{"windows prefix", "windows", "amd64", "codex-win32-x64", "x86_64-pc-windows-msvc", "codex.exe", "NPM_CONFIG_PREFIX", func(tr *tree) string { return filepath.Join(tr.root, "npm") }, func(p string) string { return filepath.Join(p, "node_modules") }},
		{"windows appdata arm64", "windows", "arm64", "codex-win32-arm64", "aarch64-pc-windows-msvc", "codex.exe", "APPDATA", func(tr *tree) string { return filepath.Join(tr.root, "AppData") }, func(p string) string { return filepath.Join(p, "npm", "node_modules") }},
		{"darwin local", "darwin", "amd64", "codex-darwin-x64", "x86_64-apple-darwin", "codex", "", func(*tree) string { return filepath.Join(root, "usr", "local", "lib", "node_modules") }, func(p string) string { return p }},
		{"darwin homebrew arm64", "darwin", "arm64", "codex-darwin-arm64", "aarch64-apple-darwin", "codex", "", func(*tree) string { return filepath.Join(root, "opt", "homebrew", "lib", "node_modules") }, func(p string) string { return p }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := newTree(t, tt.goos, tt.arch)
			prefix := tt.prefix(tr)
			if tt.key != "" {
				tr.env[tt.key] = prefix
			}
			want := tr.file(t, nativeFile(tt.modules(prefix), tt.pkg, tt.triple, tt.filename))
			assertDiscover(t, tr, "", want)
		})
	}
}

func TestDiscoveryShims(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			tr := newTree(t, goos, "amd64")
			dir := filepath.Join(tr.root, "path")
			tr.env["PATH"] = dir
			if goos == "windows" {
				for _, name := range []string{"codex", "codex.cmd", "codex.ps1"} {
					shim := tr.file(t, filepath.Join(dir, name))
					assertDiscover(t, tr, shim, "")
				}
				assertDiscover(t, tr, "", "")
				exe := tr.file(t, filepath.Join(dir, "codex.exe"))
				assertDiscover(t, tr, "", exe)
				return
			}
			shim := tr.file(t, filepath.Join(dir, "codex"))
			modules := filepath.Join(tr.root, "npm", "lib", "node_modules")
			js := tr.file(t, filepath.Join(modules, "@openai", "codex", "bin", "codex.js"))
			tr.links[shim] = js
			pkg, triple := "codex-linux-x64", "x86_64-unknown-linux-musl"
			if goos == "darwin" {
				pkg, triple = "codex-darwin-x64", "x86_64-apple-darwin"
			}
			// A broken native package skips the shim, including for an explicit path.
			assertDiscover(t, tr, "", "")
			assertDiscover(t, tr, shim, "")
			native := tr.file(t, nativeFile(modules, pkg, triple, "codex"))
			if runtime.GOOS != "windows" {
				assertDiscover(t, tr, "", native)
			}
			assertDiscover(t, tr, shim, native)
		})
	}
}

func TestDiscoveryMacFixedPrefixes(t *testing.T) {
	t.Parallel()
	tr := newTree(t, "darwin", "amd64")
	root := string(filepath.Separator)
	brew := tr.file(t, filepath.Join(root, "opt", "homebrew", "bin", "codex"))
	local := tr.file(t, filepath.Join(root, "usr", "local", "bin", "codex"))
	// Return the logical fixed path for ordinary binaries; shim targets remain physical paths.
	tr.d.EvalSymlinks = func(p string) (string, error) { return p, nil }
	assertDiscover(t, tr, "", brew)
	if err := os.Remove(tr.local(brew)); err != nil {
		t.Fatal(err)
	}
	assertDiscover(t, tr, "", local)
}

func TestDiscoveryExtensions(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"linux", "windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			tr := newTree(t, goos, "amd64")
			filename, platform := "codex", goos+"-x86_64"
			if goos == "windows" {
				filename = "codex.exe"
			}
			if goos == "darwin" {
				platform = "macos-x86_64"
			}
			versions := []string{"3.0.0", "26.999.1", "26.1021.22027"}
			var latest string
			for i, version := range versions {
				editor := []string{".vscode-server", ".cursor", ".vscode"}[i]
				latest = tr.file(t, filepath.Join(tr.home, editor, "extensions", "openai.chatgpt-"+version+"-synthetic-platform", "bin", platform, filename))
			}
			// Newer extension has only a foreign-OS binary; never select it.
			foreign := "windows-x86_64"
			if goos == "windows" {
				foreign = "linux-x86_64"
			}
			tr.file(t, filepath.Join(tr.home, ".vscode", "extensions", "openai.chatgpt-99.0.0", "bin", foreign, "codex"))
			tr.file(t, filepath.Join(tr.home, ".vscode", "extensions", "openai.chatgpt-invalid", "bin", platform, filename))
			assertDiscover(t, tr, "", latest)
			if goos == "darwin" {
				fallback := tr.file(t, filepath.Join(filepath.Dir(filepath.Dir(latest)), "darwin-x86_64", filename))
				if err := os.Remove(tr.local(latest)); err != nil {
					t.Fatal(err)
				}
				assertDiscover(t, tr, "", fallback)
			}
		})
	}
}

func TestDiscoveryFailure(t *testing.T) {
	t.Parallel()
	tr := newTree(t, "linux", "amd64")
	tr.d.UserHomeDir = func() (string, error) { return "", errors.New("no home") }
	_, err := codex.DiscoverForTest(tr.d, "")
	if err == nil || err.Error() != "Codex CLI or VS Code extension not found; install it or set --codex-bin" {
		t.Fatalf("error = %v", err)
	}
	path := tr.file(t, filepath.Join(tr.root, "codex"))
	tr.noExec[path] = true
	assertDiscover(t, tr, path, "")
}

func TestDiscoverySkipsScripts(t *testing.T) {
	t.Parallel()
	tr := newTree(t, "linux", "amd64")
	shim := tr.file(t, filepath.Join(tr.root, "path", "codex"))
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexec node synthetic-codex.js\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	tr.env["PATH"] = filepath.Dir(shim)
	native := tr.file(t, filepath.Join(tr.home, ".vscode", "extensions", "openai.chatgpt-26.1.0", "bin", "linux-x86_64", "codex"))
	assertDiscover(t, tr, shim, "")
	assertDiscover(t, tr, "", native)
}

func TestSplitPathList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		goos, value string
		want        []string
	}{
		{"linux", "/usr/bin:/opt/codex", []string{"/usr/bin", "/opt/codex"}},
		{"linux", "", nil},
		{"windows", `C:\Windows;"D:\Tools\Codex";E:\bin`, []string{`C:\Windows`, `D:\Tools\Codex`, `E:\bin`}},
		{"windows", `"D:\a;b\Codex";C:\x`, []string{`D:\a;b\Codex`, `C:\x`}},
	}
	for _, tt := range tests {
		t.Run(tt.goos+" "+tt.value, func(t *testing.T) {
			t.Parallel()
			got := codex.SplitPathListForTest(tt.value, tt.goos)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") || len(got) != len(tt.want) {
				t.Errorf("splitPathList(%q) = %q; want %q", tt.value, got, tt.want)
			}
		})
	}
}
