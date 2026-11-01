// THROW-AWAY spike for #183: read the `/usage` panel of an unmodified `claude` in a PTY
// (creack/pty on Linux/macOS, ConPTY through golang.org/x/sys/windows on Windows).
// Not part of the build, lint or tests; its own go.mod keeps the dependency out of the core.
//
//	go run . [-bin PATH] [-raw] [-out FILE] [-timeout 25s] [-launchd-path] [-discover]
//
// It never reads Claude's credential files: it only starts the binary and reads its screen.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const rows, cols = 50, 140

// Flags passed to claude: no hooks, no MCP servers, no user/project settings (plugins included).
var claudeArgs = []string{
	"--settings", `{"disableAllHooks":true}`,
	"--strict-mcp-config",
	"--setting-sources", "local",
}

type window struct {
	Label       string  `json:"label"`
	UsedPercent float64 `json:"usedPercent"`
	Resets      string  `json:"resets"`
}

type result struct {
	OS         string   `json:"os"`
	Bin        string   `json:"bin"`
	BinSource  string   `json:"binSource"`
	Version    string   `json:"version,omitempty"`
	State      string   `json:"state"` // ok | stale | not_signed_in | not_ready | error
	Reason     string   `json:"reason,omitempty"`
	Windows    []window `json:"windows,omitempty"`
	WallMs     int64    `json:"wallMs"`
	ReadyMs    int64    `json:"readyMs"`
	CPUMs      int64    `json:"cpuMs"`
	ExitNote   string   `json:"exitNote,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Screen     string   `json:"screen,omitempty"`
}

// session is the platform PTY: Write sends keys, Read returns output, Close kills the whole tree
// and returns the CPU time used by it.
type session interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() (cpu time.Duration, note string)
}

var (
	reUsed       = regexp.MustCompile(`(\d{1,3}(?:\.\d+)?)\s*%\s*used`)
	reResets     = regexp.MustCompile(`Resets?\s*(.+)$`)
	reNotSigned  = regexp.MustCompile(`(?i)select\s*login\s*method|not\s*logged\s*in|please\s*run\s*/login|run\s*/login`)
	reStale      = regexp.MustCompile(`(?i)could\s*not\s*refresh`)
	reTrust      = regexp.MustCompile(`(?i)do\s*you\s*trust|trust\s*the\s*files|is\s*this\s*a\s*project`)
	reTheme      = regexp.MustCompile(`(?i)choose\s*the\s*text\s*style|dark\s*mode.*light\s*mode`)
	reUpdate     = regexp.MustCompile(`(?i)update\s*available|new\s*version|press\s*enter\s*to\s*continue|what's\s*new`)
	reUsageTitle = regexp.MustCompile(`(?i)current\s*session|current\s*week`)
)

func main() {
	bin := flag.String("bin", "", "claude binary (default: discovery)")
	raw := flag.Bool("raw", false, "include the rendered screen in the result")
	out := flag.String("out", "", "write the JSON result to FILE (for GUI builds with no stdout)")
	timeout := flag.Duration("timeout", 25*time.Second, "whole-probe timeout")
	launchdPath := flag.Bool("launchd-path", false, "child PATH=/usr/bin:/bin:/usr/sbin:/sbin, as a launchd GUI job")
	discoverOnly := flag.Bool("discover", false, "only list discovery candidates")
	flag.StringVar(&workdir, "workdir", "", "fixed working directory (default: a new temp folder, removed after)")
	flag.BoolVar(&acceptTrust, "accept-trust", false, "answer \"Yes, I trust this folder\" once (only for the probe's own empty folder)")
	flag.Parse()

	res := result{OS: runtime.GOOS + "/" + runtime.GOARCH}
	if *launchdPath { // discovery sees the same PATH as a launchd GUI job
		_ = os.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	}
	cands := discover()
	for _, c := range cands {
		res.Candidates = append(res.Candidates, c.source+": "+c.path)
	}
	if *bin != "" {
		res.Bin, res.BinSource = *bin, "flag"
	} else if len(cands) > 0 {
		res.Bin, res.BinSource = cands[0].path, cands[0].source
	}
	if *discoverOnly || res.Bin == "" {
		if res.Bin == "" {
			res.State, res.Reason = "error", "claude not found"
		}
		emit(res, *out)
		return
	}
	probe(&res, *timeout, *launchdPath, *raw)
	emit(res, *out)
}

func emit(res result, out string) {
	b, _ := json.MarshalIndent(res, "", "  ")
	if out != "" {
		_ = os.WriteFile(out, b, 0o600)
		return
	}
	fmt.Println(string(b))
}

func childEnv(launchdPath bool) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		// Markers inherited from a parent Claude Code session change its behaviour.
		if strings.HasPrefix(k, "CLAUDE_CODE_") || k == "CLAUDECODE" || k == "TERM" {
			continue
		}
		if launchdPath && strings.EqualFold(k, "PATH") {
			continue
		}
		env = append(env, kv)
	}
	if launchdPath {
		env = append(env, "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
	}
	return append(env, "TERM=xterm-256color")
}

func probe(res *result, timeout time.Duration, launchdPath, raw bool) {
	cwd := workdir
	if cwd == "" {
		tmp, err := os.MkdirTemp("", "hyp-claude-probe-")
		if err != nil {
			res.State, res.Reason = "error", err.Error()
			return
		}
		defer os.RemoveAll(tmp)
		cwd = tmp
	} else if err := os.MkdirAll(cwd, 0o700); err != nil {
		res.State, res.Reason = "error", err.Error()
		return
	}

	t0 := time.Now()
	s, err := start(res.Bin, claudeArgs, cwd, childEnv(launchdPath))
	if err != nil {
		res.State, res.Reason = "error", "start: "+err.Error()
		return
	}
	scr := newScreen(rows, cols)
	chunks := make(chan []byte, 64)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := s.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(chunks)
				return
			}
		}
	}()

	deadline := time.After(timeout)
	var lastData time.Time
	sentAt := time.Time{}
	trustSent := false
	phase := "start"
	finish := func(state, reason string) {
		res.State, res.Reason = state, reason
		phase = "done"
	}
loop:
	for phase != "done" {
		select {
		case b, ok := <-chunks:
			if !ok {
				finish("error", "claude exited")
				break loop
			}
			scr.write(b)
			lastData = time.Now()
		case <-deadline:
			finish("not_ready", "timeout in phase "+phase)
		case <-time.After(150 * time.Millisecond):
		}
		text := scr.text()
		quiet := !lastData.IsZero() && time.Since(lastData) > 1200*time.Millisecond
		switch phase {
		case "start":
			switch {
			case reNotSigned.MatchString(text):
				finish("not_signed_in", "")
			case reTheme.MatchString(text):
				finish("not_ready", "first-run theme prompt")
			case reTrust.MatchString(text) && quiet && acceptTrust && !trustSent:
				_, _ = s.Write([]byte("\x1b[B"))
				time.Sleep(300 * time.Millisecond)
				_, _ = s.Write([]byte("\r"))
				trustSent, lastData = true, time.Now()
				res.Reason = "trust accepted"
			case reTrust.MatchString(text) && quiet && !trustSent:
				finish("not_ready", "folder trust prompt")
			case reUpdate.MatchString(text) && quiet:
				finish("not_ready", "update/notice prompt")
			case quiet:
				res.ReadyMs = time.Since(t0).Milliseconds()
				_, _ = s.Write([]byte("/usage"))
				time.Sleep(300 * time.Millisecond)
				_, _ = s.Write([]byte("\r"))
				sentAt, phase = time.Now(), "usage"
			}
		case "usage":
			if reNotSigned.MatchString(text) {
				finish("not_signed_in", "")
				break
			}
			ws := parse(text)
			settled := lastData.After(sentAt) && time.Since(lastData) > 700*time.Millisecond
			if len(ws) > 0 && settled {
				res.Windows = ws
				if reStale.MatchString(text) {
					finish("stale", "could not refresh")
				} else {
					finish("ok", res.Reason)
				}
			} else if time.Since(sentAt) > 12*time.Second {
				finish("not_ready", "no usage windows on screen")
			}
		}
	}
	// Keep reading while closing: ClosePseudoConsole can block while its output pipe is full.
	go func() {
		for range chunks {
		}
	}()
	cpu, note := s.Close()
	res.WallMs = time.Since(t0).Milliseconds()
	res.CPUMs = cpu.Milliseconds()
	res.ExitNote = note
	if v := reVersion.FindString(scr.text()); v != "" {
		res.Version = v
	}
	if raw || res.State == "not_ready" || res.State == "error" {
		res.Screen = scr.text()
	}
}

var (
	workdir     string
	acceptTrust bool
)

var reVersion = regexp.MustCompile(`Claude\s*Code\s*v?\d+\.\d+\.\d+`)

// parse reads "<label>" / "<n>% used" / "Resets <when>" groups from the rendered screen.
func parse(text string) []window {
	var out []window
	label := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.Trim(line, " │|╭╮╰╯─█▌░▒▓ ")
		if line == "" {
			continue
		}
		if m := reUsed.FindStringSubmatch(line); m != nil {
			var p float64
			_, _ = fmt.Sscan(m[1], &p)
			out = append(out, window{Label: label, UsedPercent: p})
			continue
		}
		if m := reResets.FindStringSubmatch(line); m != nil && len(out) > 0 && out[len(out)-1].Resets == "" {
			out[len(out)-1].Resets = strings.TrimSpace(m[1])
			continue
		}
		label = line
	}
	return out
}

type candidate struct{ source, path string }

func exists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// newestGlob returns the lexically last match (extension folders carry the version).
func newestGlob(pattern string) string {
	m, _ := filepath.Glob(pattern)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1]
}
