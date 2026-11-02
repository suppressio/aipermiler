package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// SinkCommand is the hidden core subcommand Claude runs as the probe's status line:
// it appends the JSON Claude passes on stdin to the file named by its argument.
const SinkCommand = "claude-statusline"

const (
	sinkPattern  = "claude-status-*.jsonl"
	maxStatusDoc = 1 << 20
	endpoint     = "claude status line rate_limits"
)

// Sink is the status line command: one JSON document per run, appended as one line
// to an existing sink file the provider created. It prints nothing, so the probe's
// status line stays empty.
func Sink(args []string, stdin io.Reader) int {
	if len(args) != 1 || !validSink(args[0]) {
		return 2
	}
	data, err := io.ReadAll(io.LimitReader(stdin, maxStatusDoc+1))
	if err != nil || len(data) > maxStatusDoc {
		return 1
	}
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil {
		return 1
	}
	line := new(bytes.Buffer)
	if json.Compact(line, data) != nil {
		return 1
	}
	line.WriteByte('\n')
	// No O_CREATE: only a file the provider made for this probe is written.
	file, err := os.OpenFile(args[0], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return 1
	}
	_, err = file.Write(line.Bytes())
	if closeErr := file.Close(); err != nil || closeErr != nil {
		return 1
	}
	return 0
}

// validSink accepts only an absolute path to a regular file named like a sink.
func validSink(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	if ok, _ := filepath.Match(sinkPattern, filepath.Base(path)); !ok {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// shellQuote quotes for the POSIX shell Claude runs the status line command with.
// Windows runs it through another shell: #223.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type statusWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"`
}

type statusDoc struct {
	Cwd            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
	RateLimits     *struct {
		FiveHour *statusWindow `json:"five_hour"`
		SevenDay *statusWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

// statusRead is what the sink file holds after a probe.
type statusRead struct {
	runs        int
	latest      *statusDoc // last run that carried rate_limits
	shape       any        // that run, or the last run when none did
	transcripts []transcript
}

type transcript struct{ path, cwd string }

func readStatus(path string) statusRead {
	var read statusRead
	file, err := os.Open(path)
	if err != nil {
		return read
	}
	defer func() { _ = file.Close() }()
	lines := bufio.NewScanner(file)
	lines.Buffer(make([]byte, 64*1024), maxStatusDoc+1)
	for lines.Scan() {
		var doc statusDoc
		var decoded any
		if json.Unmarshal(lines.Bytes(), &doc) != nil || json.Unmarshal(lines.Bytes(), &decoded) != nil {
			continue
		}
		read.runs++
		if doc.TranscriptPath != "" {
			read.transcripts = append(read.transcripts, transcript{doc.TranscriptPath, doc.Cwd})
		}
		if doc.RateLimits != nil {
			read.latest, read.shape = &doc, decoded
		} else if read.latest == nil {
			read.shape = decoded
		}
	}
	return read
}

// waitForRateLimits polls the sink until a run with rate_limits arrived or wait ran out.
func waitForRateLimits(done <-chan struct{}, path string, wait time.Duration) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for readStatus(path).latest == nil {
		select {
		case <-done:
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func drift(message string, shape any) *usage.Error {
	return &usage.Error{Code: usage.CodeFormatDrift, Message: message, Endpoint: endpoint, Shape: usage.ExtractShape(shape)}
}

// mapUsage turns the last status line run with rate_limits into the usage model.
// There is no plan tier: the status line does not carry one.
func mapUsage(read statusRead) (usage.Usage, error) {
	if read.runs == 0 {
		return usage.Usage{}, drift("Claude: the status line never ran", nil)
	}
	if read.latest == nil {
		return usage.Usage{}, drift("Claude: the status line carried no rate_limits", read.shape)
	}
	var result usage.Usage
	for _, w := range []struct {
		window *statusWindow
		hours  int64
	}{{read.latest.RateLimits.FiveHour, 5}, {read.latest.RateLimits.SevenDay, 7 * 24}} {
		if window, ok := mapWindow(w.window, w.hours); ok {
			result.QuotaWindows = append(result.QuotaWindows, window)
		}
	}
	if len(result.QuotaWindows) == 0 {
		return usage.Usage{}, drift("Claude: no quota window recognized", read.shape)
	}
	return result, nil
}

func mapWindow(w *statusWindow, hours int64) (usage.QuotaWindow, bool) {
	if w == nil || w.UsedPercentage == nil || *w.UsedPercentage < 0 {
		return usage.QuotaWindow{}, false
	}
	length, period, suffix, label := hours, usage.PeriodRollingHours, "h", "hour"
	if hours%24 == 0 {
		length, period, suffix, label = hours/24, usage.PeriodRollingDays, "d", "day"
	}
	n, total := float64(length), 100.0
	result := usage.QuotaWindow{ID: fmt.Sprintf("claude-%d%s", length, suffix), Label: fmt.Sprintf("%d-%s limit", length, label), PeriodType: period, PeriodLength: &n, Unit: usage.UnitPercentage, Used: *w.UsedPercentage, Total: &total}
	if w.ResetsAt != nil && *w.ResetsAt > 0 {
		reset := time.Unix(*w.ResetsAt, 0).UTC()
		// The JSON contract requires an RFC 3339 year.
		if reset.Year() <= 9999 {
			result.ResetsAt = &reset
		}
	}
	return result, true
}

// removeTranscripts deletes the probe's own transcripts, so neither Claude's session
// list nor our local insights see the probes. Only a .jsonl one level under Claude's
// projects folder, reported by a run whose cwd is the probe folder; then that
// project folder if it is left empty. Nothing else is touched; failures only leave
// the file behind.
func removeTranscripts(transcripts []transcript, dir, projects string) {
	if projects == "" {
		return
	}
	for _, t := range transcripts {
		if filepath.Ext(t.path) != ".jsonl" || !filepath.IsAbs(t.path) || !sameFile(t.cwd, dir) {
			continue
		}
		folder := filepath.Dir(t.path)
		if info, err := os.Lstat(folder); err != nil || !info.IsDir() || !sameFile(filepath.Dir(folder), projects) {
			continue
		}
		if info, err := os.Lstat(t.path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if os.Remove(t.path) == nil {
			// Fails while the folder holds anything else, which is then kept.
			_ = os.Remove(folder)
		}
	}
}

// sameFile compares the files themselves (inode / file id), not their spellings.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}
