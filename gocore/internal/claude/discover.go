// Package claude reads the Claude subscription quotas through the user's own,
// unmodified `claude` binary (#147). This file finds that binary, in the order
// decided on #183 (docs/SPIKE-CLAUDE-USAGE-PTY.md, Decision).
package claude

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/suppressio/aipermiler/gocore/internal/pathlist"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// minNativeSize tells the native binary (well over 100 MB) from the npm stub left
// when postinstall did not run (under 4 KB) and from wrapper scripts.
const minNativeSize = 1 << 20

// discovery holds the file system and environment seams, so every OS layout is
// tested on one host; systemDiscovery is the real one.
type discovery struct {
	Getenv       func(string) string
	Stat         func(string) (fs.FileInfo, error)
	EvalSymlinks func(string) (string, error)
	ReadDir      func(string) ([]os.DirEntry, error)
	UserHomeDir  func() (string, error)
	GOOS, GOARCH string
}

func systemDiscovery() discovery {
	return discovery{os.Getenv, os.Stat, filepath.EvalSymlinks, os.ReadDir, os.UserHomeDir, runtime.GOOS, runtime.GOARCH}
}

// Reasons of `unavailable` a client explains in the user's language (#180).
const (
	// Claude Code is not installed (or not where --claude-bin points).
	ReasonNotInstalled = "not_installed"
	// Signed in with an API key or a cloud provider: no subscription quota.
	ReasonNoSubscription = "no_subscription"
	// Claude Code waits for the user to answer about an API key in its environment.
	ReasonAPIKeyQuestion = "api_key_question"
)

func unavailableBecause(reason, message string) *usage.Error {
	return &usage.Error{Code: usage.CodeUnavailable, Reason: reason, Message: message}
}

func unavailable(message string) *usage.Error {
	return &usage.Error{Code: usage.CodeUnavailable, Message: message}
}

func (d discovery) filename() string {
	if d.GOOS == "windows" {
		return "claude.exe"
	}
	return "claude"
}

// native returns the resolved path when path leads to a native claude binary, or "".
// npm shims (.cmd, .ps1, sh scripts), .js/.cjs entry points and the npm stub are not.
func (d discovery) native(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	resolved, err := d.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	if ext == ".js" || ext == ".cjs" || (d.GOOS == "windows" && ext != ".exe") {
		return ""
	}
	info, err := d.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() < minNativeSize {
		return ""
	}
	if d.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return resolved
}

// discover returns the binary to run: the explicit path when given (an error when it
// is not a native claude binary, never a silent fallback), else the first candidate.
func (d discovery) discover(explicit string) (string, error) {
	if explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", unavailableBecause(ReasonNotInstalled, "claude binary path must be absolute")
		}
		if found := d.native(explicit); found != "" {
			return found, nil
		}
		return "", unavailableBecause(ReasonNotInstalled, "no native claude binary at --claude-bin (not found, not executable, or an npm shim)")
	}
	if found := d.candidates(); len(found) > 0 {
		return found[0], nil
	}
	return "", unavailableBecause(ReasonNotInstalled, "Claude Code not found; install it or set --claude-bin")
}

// candidates lists every native binary found, in discovery order, de-duplicated by
// resolved path (an npm symlink and its target are one candidate).
func (d discovery) candidates() []string {
	var out []string
	seen := map[string]bool{}
	add := func(path string) {
		if found := d.native(path); found != "" && !seen[found] {
			seen[found] = true
			out = append(out, found)
		}
	}
	for _, dir := range pathlist.Split(d.Getenv("PATH"), d.GOOS) {
		// Never execute from an implicit current directory or a relative PATH entry:
		// native rejects relative paths.
		add(filepath.Join(dir, d.filename()))
		if d.GOOS == "windows" {
			// An npm prefix in PATH holds only the claude.cmd shim; the native binary
			// it runs sits in the prefix's node_modules (the prefix is not always
			// %APPDATA%\npm: Node from winget uses its own folder, verified on #223).
			add(npmBinary(filepath.Join(dir, "node_modules")))
		}
	}
	home, err := d.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		home = ""
	}
	if home != "" {
		add(filepath.Join(home, ".local", "bin", d.filename()))
	}
	for _, path := range d.fixedPrefixes() {
		add(path)
	}
	if home != "" {
		for _, path := range d.extensions(home) {
			add(path)
		}
	}
	return out
}

// fixedPrefixes are searched whatever PATH says: on macOS the launchd PATH of a GUI
// app has neither /opt/homebrew/bin nor /usr/local/bin.
// npmBinary is the native binary npm's postinstall links into the package.
func npmBinary(modules string) string {
	return filepath.Join(modules, "@anthropic-ai", "claude-code", "bin", "claude.exe")
}

func (d discovery) fixedPrefixes() []string {
	root := string(filepath.Separator)
	switch d.GOOS {
	case "windows":
		var out []string
		if appdata := d.Getenv("APPDATA"); filepath.IsAbs(appdata) {
			out = append(out, npmBinary(filepath.Join(appdata, "npm", "node_modules")))
		}
		if local := d.Getenv("LOCALAPPDATA"); filepath.IsAbs(local) {
			out = append(out, d.winget(filepath.Join(local, "Microsoft", "WinGet", "Packages"))...)
		}
		return out
	case "darwin":
		return []string{
			filepath.Join(root, "opt", "homebrew", "bin", "claude"),
			filepath.Join(root, "usr", "local", "bin", "claude"),
			npmBinary(filepath.Join(root, "usr", "local", "lib", "node_modules")),
			npmBinary(filepath.Join(root, "opt", "homebrew", "lib", "node_modules")),
		}
	case "linux":
		return []string{
			npmBinary(filepath.Join(root, "usr", "local", "lib", "node_modules")),
			npmBinary(filepath.Join(root, "usr", "lib", "node_modules")),
		}
	}
	return nil
}

// winget lists claude.exe in the winget package folders (Anthropic.ClaudeCode_<source>).
func (d discovery) winget(packages string) []string {
	entries, err := d.ReadDir(packages)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "Anthropic.ClaudeCode_") {
			out = append(out, filepath.Join(packages, entry.Name(), "claude.exe"))
		}
	}
	return out
}

type extension struct {
	path    string
	version []uint64
}

// extensionPlatform is the suffix of the extension folder for the current OS and
// architecture (anthropic.claude-code-<version>-<platform>), "" when unknown.
func (d discovery) extensionPlatform() string {
	goos := map[string]string{"linux": "linux", "darwin": "darwin", "windows": "win32"}[d.GOOS]
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[d.GOARCH]
	if goos == "" || arch == "" {
		return ""
	}
	return goos + "-" + arch
}

// extensionVersion parses anthropic.claude-code-<version>-<platform>; nil when the
// name is another extension, another platform or not a plain dotted version.
func extensionVersion(name, platform string) []uint64 {
	rest, ok := strings.CutPrefix(name, "anthropic.claude-code-")
	if !ok {
		return nil
	}
	version, ok := strings.CutSuffix(rest, "-"+platform)
	if !ok {
		return nil
	}
	var numbers []uint64
	for _, part := range strings.Split(version, ".") {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return nil
		}
		numbers = append(numbers, n)
	}
	return numbers
}

func newer(a, b []uint64) bool {
	for i := 0; i < len(a) || i < len(b); i++ {
		var av, bv uint64
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

// extensions lists the bundled binaries of the VS Code-family extensions, newest
// version first by number (2.1.100 is newer than 2.1.99), current platform only.
func (d discovery) extensions(home string) []string {
	platform := d.extensionPlatform()
	if platform == "" {
		return nil
	}
	var found []extension
	for _, editor := range []string{".vscode", ".vscode-insiders", ".cursor", ".windsurf", ".vscode-server"} {
		dir := filepath.Join(home, editor, "extensions")
		entries, err := d.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if version := extensionVersion(entry.Name(), platform); entry.IsDir() && version != nil {
				found = append(found, extension{filepath.Join(dir, entry.Name()), version})
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return newer(found[i].version, found[j].version) })
	out := make([]string, 0, len(found))
	for _, ext := range found {
		out = append(out, filepath.Join(ext.path, "resources", "native-binary", d.filename()))
	}
	return out
}
