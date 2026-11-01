package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// statusLineArgs replaces the default flags: our own statusLine command (this binary in -sl-sink
// mode) instead of disableAllHooks, which would switch the status line off too.
func statusLineArgs() ([]string, string, error) {
	dir, err := os.MkdirTemp("", "hyp-claude-sl-")
	if err != nil {
		return nil, "", err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	file := filepath.Join(dir, "statusline.jsonl")
	cmd := fmt.Sprintf("%q -sl-sink %q", self, file)
	settings, _ := json.Marshal(map[string]any{"statusLine": map[string]string{"type": "command", "command": cmd}})
	return []string{"--settings", string(settings), "--strict-mcp-config", "--setting-sources", "local"}, file, nil
}

// statusLineSink is the statusLine command: one JSON document per run, appended as one line.
func statusLineSink(file string) {
	b, _ := io.ReadAll(os.Stdin)
	var v any
	if json.Unmarshal(b, &v) == nil {
		b, _ = json.Marshal(v)
		if f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.Write(append(b, '\n'))
			_ = f.Close()
		}
	}
	fmt.Println("hypermiler")
}

type rateWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"`
}

// probeTranscript returns the transcript path the status line reported for the probe's own
// session, only when it lies in Claude's project folder named after workdir.
func probeTranscript(file, workdir string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	path := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var d struct {
			TranscriptPath string `json:"transcript_path"`
			Cwd            string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &d) == nil && d.TranscriptPath != "" && sameDir(d.Cwd, workdir) {
			path = d.TranscriptPath
		}
	}
	if filepath.Ext(path) != ".jsonl" || filepath.Base(filepath.Dir(filepath.Dir(path))) != "projects" {
		return ""
	}
	return path
}

func sameDir(a, b string) bool {
	resolve := func(p string) (string, error) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(abs)
	}
	ra, err1 := resolve(a)
	rb, err2 := resolve(b)
	return err1 == nil && err2 == nil && ra == rb
}

// readStatusLine returns the windows of the last status line run that carried rate_limits.
func readStatusLine(file string) []window {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []window
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var d struct {
			RateLimits map[string]rateWindow `json:"rate_limits"`
		}
		if json.Unmarshal(sc.Bytes(), &d) != nil || len(d.RateLimits) == 0 {
			continue
		}
		out = out[:0]
		for _, k := range []string{"five_hour", "seven_day"} {
			w, ok := d.RateLimits[k]
			if !ok || w.UsedPercentage == nil {
				continue
			}
			r := ""
			if w.ResetsAt != nil {
				r = time.Unix(*w.ResetsAt, 0).Format(time.RFC3339)
			}
			out = append(out, window{Label: k, UsedPercent: *w.UsedPercentage, Resets: r})
		}
	}
	return out
}
