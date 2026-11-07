package claude_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/claude"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// tree simulates one OS layout inside a temporary folder: fixed system prefixes are
// mirrored under root/system, never read on the host.
type tree struct {
	d          claude.Discovery
	root, home string
	env        map[string]string
	links      map[string]string
	noExec     map[string]bool
}

// simulatedInfo gives fixture files the executable bits of the simulated OS
// instead of the host's (os.Stat on Windows never reports them).
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
	tr := &tree{root: root, home: filepath.Join(root, "home"), env: map[string]string{}, links: map[string]string{}, noExec: map[string]bool{}}
	tr.d = claude.Discovery{
		Getenv: func(key string) string { return tr.env[key] },
		Stat: func(path string) (fs.FileInfo, error) {
			info, err := os.Stat(tr.local(path))
			if err != nil {
				return nil, err
			}
			return simulatedInfo{info, tr.noExec[path]}, nil
		},
		EvalSymlinks: func(path string) (string, error) {
			if target, ok := tr.links[path]; ok {
				return target, nil
			}
			if _, err := os.Stat(tr.local(path)); err != nil {
				return "", err
			}
			return path, nil
		},
		ReadDir:     func(path string) ([]os.DirEntry, error) { return os.ReadDir(tr.local(path)) },
		UserHomeDir: func() (string, error) { return tr.home, nil },
		GOOS:        goos, GOARCH: arch,
	}
	return tr
}

func (tr *tree) local(path string) string {
	if path == tr.root || strings.HasPrefix(path, tr.root+string(filepath.Separator)) {
		return path
	}
	return filepath.Join(tr.root, "system", strings.TrimPrefix(strings.TrimPrefix(path, filepath.VolumeName(path)), string(filepath.Separator)))
}

// file creates a file of the given size (sparse: no real data is written).
func (tr *tree) file(t *testing.T, path string, size int64) string {
	t.Helper()
	local := tr.local(path)
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(local)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// native creates a synthetic native binary (2 MB, over the stub threshold).
func (tr *tree) native(t *testing.T, path string) string {
	t.Helper()
	return tr.file(t, path, 2<<20)
}

func (tr *tree) remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(tr.local(path)); err != nil {
		t.Fatal(err)
	}
}

func assertDiscover(t *testing.T, tr *tree, explicit, want string) {
	t.Helper()
	got, err := claude.DiscoverForTest(tr.d, explicit)
	if want == "" {
		var e *usage.Error
		if !errors.As(err, &e) || e.Code != usage.CodeUnavailable || e.Reason != claude.ReasonNotInstalled {
			t.Fatalf("discover(%q) = %q, %#v; want unavailable, not_installed", explicit, got, err)
		}
		return
	}
	if err != nil || got != want {
		t.Fatalf("discover(%q) = %q, %v; want %q", explicit, got, err, want)
	}
}

// posixOnly skips a simulated Linux/macOS layout on a Windows host: a POSIX PATH
// (":"-separated) cannot hold host paths with a drive letter.
func posixOnly(t *testing.T, goos string) {
	t.Helper()
	if goos != "windows" && runtime.GOOS == "windows" {
		t.Skip("simulated POSIX PATH on a Windows host")
	}
}

func npmBinary(modules string) string {
	return filepath.Join(modules, "@anthropic-ai", "claude-code", "bin", "claude.exe")
}

func TestDiscoveryOrder(t *testing.T) {
	t.Parallel()
	sys := string(filepath.Separator)
	tests := []struct {
		goos, filename, platform string
		env                      func(*tree)
		fixed                    func(*tree) []string
	}{
		{"linux", "claude", "linux-x64", func(*tree) {}, func(*tree) []string {
			return []string{
				npmBinary(filepath.Join(sys, "usr", "local", "lib", "node_modules")),
				npmBinary(filepath.Join(sys, "usr", "lib", "node_modules")),
			}
		}},
		{"darwin", "claude", "darwin-x64", func(*tree) {}, func(*tree) []string {
			return []string{
				filepath.Join(sys, "opt", "homebrew", "bin", "claude"),
				filepath.Join(sys, "usr", "local", "bin", "claude"),
				npmBinary(filepath.Join(sys, "usr", "local", "lib", "node_modules")),
				npmBinary(filepath.Join(sys, "opt", "homebrew", "lib", "node_modules")),
			}
		}},
		{"windows", "claude.exe", "win32-x64", func(tr *tree) {
			tr.env["APPDATA"] = filepath.Join(tr.home, "AppData", "Roaming")
			tr.env["LOCALAPPDATA"] = filepath.Join(tr.home, "AppData", "Local")
		}, func(tr *tree) []string {
			return []string{
				npmBinary(filepath.Join(tr.env["APPDATA"], "npm", "node_modules")),
				filepath.Join(tr.env["LOCALAPPDATA"], "Microsoft", "WinGet", "Packages", "Anthropic.ClaudeCode_Microsoft.Winget.Source_synthetic", "claude.exe"),
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			t.Parallel()
			posixOnly(t, tt.goos)
			tr := newTree(t, tt.goos, "amd64")
			tt.env(tr)
			explicit := tr.native(t, filepath.Join(tr.root, "explicit", tt.filename))
			path1 := tr.native(t, filepath.Join(tr.root, "path1", tt.filename))
			path2 := tr.native(t, filepath.Join(tr.root, "path2", tt.filename))
			sep := ":"
			if tt.goos == "windows" {
				sep = ";"
			}
			tr.env["PATH"] = "relative" + sep + filepath.Dir(path1) + sep + filepath.Dir(path2)
			local := tr.native(t, filepath.Join(tr.home, ".local", "bin", tt.filename))
			want := []string{path1, path2, local}
			for _, path := range tt.fixed(tr) {
				want = append(want, tr.native(t, path))
			}
			want = append(want, tr.native(t, filepath.Join(tr.home, ".vscode", "extensions", "anthropic.claude-code-2.1.0-"+tt.platform, "resources", "native-binary", tt.filename)))
			// A binary in a relative PATH entry is never run.
			tr.native(t, filepath.Join("relative", tt.filename))

			if got := claude.CandidatesForTest(tr.d); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("candidates =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			assertDiscover(t, tr, explicit, explicit)
			for _, path := range want {
				assertDiscover(t, tr, "", path)
				tr.remove(t, path)
			}
			assertDiscover(t, tr, "", "")
		})
	}
}

func TestDiscoveryExplicit(t *testing.T) {
	t.Parallel()
	tr := newTree(t, "linux", "amd64")
	tr.native(t, filepath.Join(tr.home, ".local", "bin", "claude"))
	stub := tr.file(t, filepath.Join(tr.root, "stub", "claude"), 4<<10)
	// An explicit path that is not a native binary is an error, never a fallback to
	// the binary discovery would find.
	assertDiscover(t, tr, filepath.Join(tr.root, "missing", "claude"), "")
	assertDiscover(t, tr, stub, "")
	assertDiscover(t, tr, "relative/claude", "")
}

func TestDiscoverySkipsShimsAndStubs(t *testing.T) {
	t.Parallel()
	posixOnly(t, "linux")
	tr := newTree(t, "linux", "amd64")
	dir := filepath.Join(tr.root, "bin")
	tr.env["PATH"] = dir
	modules := filepath.Join(tr.root, "npm", "lib", "node_modules")
	// npm without postinstall: bin/claude.exe is a small stub, the shim runs a .cjs.
	stub := tr.file(t, npmBinary(modules), 4<<10)
	wrapper := tr.native(t, filepath.Join(modules, "@anthropic-ai", "claude-code", "cli-wrapper.cjs"))
	shim := tr.file(t, filepath.Join(dir, "claude"), 2<<20)
	tr.links[shim] = wrapper
	if got := claude.CandidatesForTest(tr.d); len(got) != 0 {
		t.Fatalf("candidates = %q; want none", got)
	}
	// A native binary without the executable bit is not run either.
	tr.links[shim] = stub
	notExec := tr.native(t, filepath.Join(tr.home, ".local", "bin", "claude"))
	tr.noExec[notExec] = true
	assertDiscover(t, tr, "", "")
}

func TestDiscoveryWindowsShims(t *testing.T) {
	t.Parallel()
	tr := newTree(t, "windows", "amd64")
	dir := filepath.Join(tr.root, "npm")
	tr.env["PATH"] = dir
	for _, name := range []string{"claude", "claude.cmd", "claude.ps1"} {
		assertDiscover(t, tr, tr.native(t, filepath.Join(dir, name)), "")
	}
	assertDiscover(t, tr, "", "")
	// The winget link in PATH resolves to the package's claude.exe.
	link := tr.native(t, filepath.Join(tr.root, "Links", "claude.exe"))
	target := tr.native(t, filepath.Join(tr.root, "Packages", "Anthropic.ClaudeCode_synthetic", "claude.exe"))
	tr.links[link] = target
	tr.env["PATH"] = dir + ";" + filepath.Dir(link)
	assertDiscover(t, tr, "", target)

	// npm with its prefix in PATH (Node from winget): the shim's own package.
	prefix := filepath.Join(tr.root, "node")
	tr.native(t, filepath.Join(prefix, "claude.cmd"))
	tr.env["PATH"] = prefix
	stub := tr.file(t, npmBinary(filepath.Join(prefix, "node_modules")), 4<<10)
	assertDiscover(t, tr, "", "")
	tr.remove(t, stub)
	assertDiscover(t, tr, "", tr.native(t, npmBinary(filepath.Join(prefix, "node_modules"))))
}

func TestDiscoveryDeduplicates(t *testing.T) {
	t.Parallel()
	posixOnly(t, "linux")
	tr := newTree(t, "linux", "amd64")
	sys := string(filepath.Separator)
	npm := tr.native(t, npmBinary(filepath.Join(sys, "usr", "local", "lib", "node_modules")))
	link := tr.native(t, filepath.Join(sys, "usr", "local", "bin", "claude"))
	tr.links[link] = npm
	tr.env["PATH"] = filepath.Dir(link)
	if got := claude.CandidatesForTest(tr.d); len(got) != 1 || got[0] != npm {
		t.Fatalf("candidates = %q; want only %q", got, npm)
	}
}

func TestDiscoveryExtensions(t *testing.T) {
	t.Parallel()
	tr := newTree(t, "darwin", "arm64")
	ext := func(editor, name string) string {
		return filepath.Join(tr.home, editor, "extensions", name, "resources", "native-binary", "claude")
	}
	older := tr.native(t, ext(".vscode", "anthropic.claude-code-2.1.99-darwin-arm64"))
	newest := tr.native(t, ext(".cursor", "anthropic.claude-code-2.1.100-darwin-arm64"))
	// Other platforms, other extensions and unparsable versions are never candidates.
	tr.native(t, ext(".vscode", "anthropic.claude-code-2.2.0-darwin-x64"))
	tr.native(t, ext(".vscode", "anthropic.claude-code-2.2.0-linux-arm64"))
	tr.native(t, ext(".vscode", "anthropic.claude-code-next-darwin-arm64"))
	tr.native(t, ext(".vscode", "someone.claude-code-9.0.0-darwin-arm64"))
	got := claude.CandidatesForTest(tr.d)
	if strings.Join(got, "\n") != newest+"\n"+older {
		t.Fatalf("candidates = %q; want %q then %q", got, newest, older)
	}
}

func TestDiscoveryUnknownPlatform(t *testing.T) {
	t.Parallel()
	tr := newTree(t, "linux", "riscv64")
	tr.native(t, filepath.Join(tr.home, ".vscode", "extensions", "anthropic.claude-code-2.1.0-linux-riscv64", "resources", "native-binary", "claude"))
	assertDiscover(t, tr, "", "")
}
