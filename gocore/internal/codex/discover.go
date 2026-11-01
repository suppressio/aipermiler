package codex

import (
	"encoding/binary"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

type discovery struct {
	ReadHeader   func(string) ([4]byte, error)
	Getenv       func(string) string
	Stat         func(string) (fs.FileInfo, error)
	EvalSymlinks func(string) (string, error)
	ReadDir      func(string) ([]os.DirEntry, error)
	UserHomeDir  func() (string, error)
	GOOS, GOARCH string
}

func systemDiscovery() discovery {
	return discovery{readHeader, os.Getenv, os.Stat, filepath.EvalSymlinks, os.ReadDir, os.UserHomeDir, runtime.GOOS, runtime.GOARCH}
}

type nativeLayout struct{ pkg, triple string }

// Spikes verified linux/amd64, windows/amd64 and darwin/amd64; ARM64 entries are inferred.
var nativeLayouts = map[string]nativeLayout{
	"linux/amd64":   {"codex-linux-x64", "x86_64-unknown-linux-musl"},
	"linux/arm64":   {"codex-linux-arm64", "aarch64-unknown-linux-musl"},
	"darwin/amd64":  {"codex-darwin-x64", "x86_64-apple-darwin"},
	"darwin/arm64":  {"codex-darwin-arm64", "aarch64-apple-darwin"},
	"windows/amd64": {"codex-win32-x64", "x86_64-pc-windows-msvc"},
	"windows/arm64": {"codex-win32-arm64", "aarch64-pc-windows-msvc"},
}

func (d discovery) executable(path string) bool {
	info, err := d.Stat(path)
	return err == nil && info.Mode().IsRegular() && (d.GOOS == "windows" || info.Mode().Perm()&0o111 != 0)
}

func readHeader(path string) ([4]byte, error) {
	var header [4]byte
	file, err := os.Open(path)
	if err != nil {
		return header, err
	}
	defer func() { _ = file.Close() }()
	_, err = io.ReadFull(file, header[:])
	return header, err
}

func (d discovery) nativeExecutable(path string) bool {
	if !d.executable(path) {
		return false
	}
	header, err := d.ReadHeader(path)
	if err != nil {
		return false
	}
	switch d.GOOS {
	case "linux":
		return header == [4]byte{0x7f, 'E', 'L', 'F'}
	case "windows":
		return header[0] == 'M' && header[1] == 'Z'
	case "darwin":
		// Mach-O in either byte order, including universal/fat binaries.
		switch binary.BigEndian.Uint32(header[:]) {
		case 0xfeedface, 0xcefaedfe, 0xfeedfacf, 0xcffaedfe, 0xcafebabe, 0xbebafeca, 0xcafebabf, 0xbfbafeca:
			return true
		}
	}
	return false
}

func (d discovery) filename() string {
	if d.GOOS == "windows" {
		return "codex.exe"
	}
	return "codex"
}

func (d discovery) native(pkgroot string) string {
	layout, ok := nativeLayouts[d.GOOS+"/"+d.GOARCH]
	if !ok {
		return ""
	}
	path := filepath.Join(pkgroot, "node_modules", "@openai", layout.pkg, "vendor", layout.triple, "bin", d.filename())
	if d.nativeExecutable(path) {
		return path
	}
	return ""
}

func (d discovery) candidate(path string) string {
	if !d.executable(path) || (d.GOOS == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe")) {
		return ""
	}
	if d.GOOS != "windows" {
		resolved, err := d.EvalSymlinks(path)
		if err != nil {
			return ""
		}
		pkgroot := filepath.Dir(filepath.Dir(resolved))
		if resolved == filepath.Join(pkgroot, "bin", "codex.js") && filepath.Base(pkgroot) == "codex" && filepath.Base(filepath.Dir(pkgroot)) == "@openai" {
			return d.native(pkgroot)
		}
	}
	if d.nativeExecutable(path) {
		return path
	}
	return ""
}

func (d discovery) discover(explicit string) (string, error) {
	if explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", unavailable("codex binary path must be absolute")
		}
		if found := d.candidate(explicit); found != "" {
			return found, nil
		}
		return "", unavailable("Codex binary not found or not executable at --codex-bin")
	}
	for _, dir := range splitPathList(d.Getenv("PATH"), d.GOOS) {
		// Never execute from an implicit current directory or a relative PATH entry.
		if !filepath.IsAbs(dir) {
			continue
		}
		if found := d.candidate(filepath.Join(dir, d.filename())); found != "" {
			return found, nil
		}
	}
	if d.GOOS == "darwin" {
		for _, prefix := range []string{filepath.Join(string(filepath.Separator), "opt", "homebrew"), filepath.Join(string(filepath.Separator), "usr", "local")} {
			if found := d.candidate(filepath.Join(prefix, "bin", "codex")); found != "" {
				return found, nil
			}
		}
	}
	home, _ := d.UserHomeDir()
	for _, root := range d.npmRoots(home) {
		if found := d.native(filepath.Join(root, "@openai", "codex")); found != "" {
			return found, nil
		}
	}
	if home != "" {
		if found := d.extension(home); found != "" {
			return found, nil
		}
	}
	return "", unavailable("Codex CLI or VS Code extension not found; install it or set --codex-bin")
}

func (d discovery) npmRoots(home string) []string {
	var roots []string
	for _, key := range []string{"NPM_CONFIG_PREFIX", "npm_config_prefix"} {
		if prefix := d.Getenv(key); filepath.IsAbs(prefix) {
			if d.GOOS == "windows" {
				roots = append(roots, filepath.Join(prefix, "node_modules"))
			} else {
				roots = append(roots, filepath.Join(prefix, "lib", "node_modules"))
			}
		}
	}
	root := string(filepath.Separator)
	switch d.GOOS {
	case "windows":
		if appdata := d.Getenv("APPDATA"); filepath.IsAbs(appdata) {
			roots = append(roots, filepath.Join(appdata, "npm", "node_modules"))
		}
	case "linux":
		roots = append(roots, filepath.Join(root, "usr", "lib", "node_modules"), filepath.Join(root, "usr", "local", "lib", "node_modules"))
		if home != "" {
			roots = append(roots, filepath.Join(home, ".npm-global", "lib", "node_modules"))
		}
	case "darwin":
		roots = append(roots, filepath.Join(root, "usr", "local", "lib", "node_modules"), filepath.Join(root, "opt", "homebrew", "lib", "node_modules"))
	}
	return roots
}

type extensionCandidate struct {
	path    string
	version []uint64
}

func extensionVersion(name string) []uint64 {
	version, ok := strings.CutPrefix(name, "openai.chatgpt-")
	if !ok {
		return nil
	}
	version, _, _ = strings.Cut(version, "-")
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

func (d discovery) extension(home string) string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[d.GOARCH]
	if arch == "" {
		return ""
	}
	platforms := []string{d.GOOS + "-" + arch}
	if d.GOOS == "darwin" {
		platforms = []string{"macos-" + arch, "darwin-" + arch}
	}
	var candidates []extensionCandidate
	for _, editor := range []string{".vscode", ".vscode-server", ".vscode-insiders", ".cursor", ".windsurf", ".vscode-oss"} {
		dir := filepath.Join(home, editor, "extensions")
		entries, err := d.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if version := extensionVersion(entry.Name()); entry.IsDir() && version != nil {
				candidates = append(candidates, extensionCandidate{filepath.Join(dir, entry.Name()), version})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return newer(candidates[i].version, candidates[j].version) })
	for _, candidate := range candidates {
		for _, platform := range platforms {
			path := filepath.Join(candidate.path, "bin", platform, d.filename())
			if d.nativeExecutable(path) {
				return path
			}
		}
	}
	return ""
}

// splitPathList splits PATH for the given OS. filepath.SplitList would follow the
// host OS, while discovery is tested for every OS on one host. On Windows an entry
// may be quoted ("D:\Tools\Codex") and may then contain the separator; the quotes
// are removed, as cmd.exe and filepath.SplitList on Windows do.
func splitPathList(value, goos string) []string {
	if value == "" {
		return nil
	}
	if goos != "windows" {
		return strings.Split(value, ":")
	}
	var entries []string
	var current strings.Builder
	quoted := false
	for _, r := range value {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ';' && !quoted:
			entries = append(entries, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	return append(entries, current.String())
}
