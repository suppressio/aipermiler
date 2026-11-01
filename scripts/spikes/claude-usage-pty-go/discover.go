package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// discover lists the native claude binaries found, in the order the probe would try them.
// npm shims (.cmd/.ps1/.js) and the npm stub left when postinstall did not run are skipped.
func discover() []candidate {
	home, _ := os.UserHomeDir()
	exe := "claude"
	if runtime.GOOS == "windows" {
		exe = "claude.exe"
	}
	var out []candidate
	seen := map[string]bool{}
	add := func(source, p string) {
		if p == "" || !exists(p) {
			return
		}
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return
		}
		if strings.HasSuffix(real, ".js") || strings.HasSuffix(real, ".cjs") {
			return
		}
		if st, err := os.Stat(real); err != nil || st.Size() < 1<<20 { // npm stub / wrapper script
			return
		}
		if seen[real] {
			return
		}
		seen[real] = true
		if real != p {
			source += " via " + p
		}
		out = append(out, candidate{source, real})
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		add("PATH", filepath.Join(dir, exe))
	}
	add("native installer", filepath.Join(home, ".local", "bin", exe))
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		add("npm global", filepath.Join(appdata, "npm", "node_modules", "@anthropic-ai", "claude-code", "bin", exe))
		add("winget", newestGlob(filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WinGet", "Packages", "Anthropic.ClaudeCode*", exe)))
	case "darwin":
		add("homebrew (arm64)", "/opt/homebrew/bin/claude")
		add("homebrew (intel) / npm", "/usr/local/bin/claude")
		add("npm global", "/usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe")
		add("npm global (brew node)", "/opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe")
	default:
		add("npm global", "/usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe")
		add("npm global", "/usr/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe")
	}
	for _, d := range []string{".vscode", ".vscode-insiders", ".cursor", ".windsurf", ".vscode-server"} {
		add("vscode extension ("+d+")", newestGlob(filepath.Join(home, d, "extensions", "anthropic.claude-code-*", "resources", "native-binary", exe)))
	}
	return out
}
