// Package insights computes behavioural insights from the local Claude Code sessions
// (#298, part of #150): output tokens per day, the share spent on a large context or in
// long sessions, the most used tools and the first session start of each day.
//
// It reads Claude Code's session transcripts directly (`<config>/projects/*/*.jsonl` and
// the subagent files below them), which is the format of Claude Code and not a documented
// interface: only a few fields are read (see line), a change of the format that leaves no
// usage at all is reported as format drift. Message text, tool parameters and tool
// results are never read into the result nor logged.
package insights

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const (
	// HighContextThreshold is the estimated context (input + cache read + cache creation) above which a response counts as high-context.
	HighContextThreshold = 150_000
	// LongSession is the duration, first to last timestamp of the main session, from which a session counts as long.
	LongSession   = 8 * time.Hour
	topToolsLimit = 5
	// maxLineBytes bounds one transcript line (tool results can be large): a longer line is skipped.
	maxLineBytes = 64 << 20
	// Endpoint labels the drift report: where the lines were read, never a real path.
	Endpoint = "projects/*/*.jsonl"
)

// ToolShare is the share of the tool calls made through one tool, in percent (one decimal).
type ToolShare struct {
	Name         string  `json:"name"`
	SharePercent float64 `json:"sharePercent"`
}

// DailyTokens is the output of one local day.
type DailyTokens struct {
	Date                    string `json:"date"`
	OutputTokens            int64  `json:"outputTokens"`
	HighContextOutputTokens int64  `json:"highContextOutputTokens"`
}

// Result mirrors the app's ClaudeLocalInsights (types/index.ts).
type Result struct {
	ComputedAt              string            `json:"computedAt"`
	WindowDays              int               `json:"windowDays"`
	SessionsAnalyzed        int               `json:"sessionsAnalyzed"`
	TotalOutputTokens       int64             `json:"totalOutputTokens"`
	HighContextOutputTokens int64             `json:"highContextOutputTokens"`
	LongSessionOutputTokens int64             `json:"longSessionOutputTokens"`
	HighContextSharePercent float64           `json:"highContextSharePercent"`
	LongSessionSharePercent float64           `json:"longSessionSharePercent"`
	TopTools                []ToolShare       `json:"topTools"`
	Daily                   []DailyTokens     `json:"daily"`
	FirstSessionStartByDay  map[string]string `json:"firstSessionStartByDay"`
}

// Options of one computation.
type Options struct {
	// Root is Claude Code's config folder (see Root); its `projects` folder is read.
	Root       string
	WindowDays int
	// ProbeDir is the core's Claude probe folder: the sessions that ran there are skipped. Empty skips nothing.
	ProbeDir string
	Now      time.Time
	// Location decides the local day of a timestamp; nil means UTC.
	Location *time.Location
	// Logger receives counts only (debug); nil logs nothing.
	Logger *slog.Logger
}

// Root is Claude Code's config folder: CLAUDE_CONFIG_DIR, else ~/.claude (%USERPROFILE%\.claude on Windows).
func Root(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	if dir := getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", &usage.Error{Code: usage.CodeUnavailable, Message: "locate home directory: " + err.Error()}
	}
	return filepath.Join(home, ".claude"), nil
}

// line holds the only fields of a transcript line that are read.
type line struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	RequestID string          `json:"requestId"`
	CWD       string          `json:"cwd"`
	Message   json.RawMessage `json:"message"`
}

type message struct {
	Usage   json.RawMessage   `json:"usage"`
	Content []json.RawMessage `json:"content"`
}

type block struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// response is one API response as seen in one line.
type response struct {
	requestID string // empty: counts on its own
	output    int64
	context   int64
	at        time.Time
}

type toolCall struct {
	id, name string
	at       time.Time
}

// fileData is what one transcript contributes, before the merge across files.
type fileData struct {
	session   string // project folder + session id, shared by a session and its subagents
	subagent  bool
	cwd       string
	first     time.Time // first and last timestamp of the file (the whole file, not only the window)
	last      time.Time
	dayFirst  map[string]time.Time // earliest timestamp per local day, inside the window
	responses []response
	tools     []toolCall
	// Assistant lines inside the window, and how many carried a numeric output_tokens.
	assistantLines, withOutput int
	sampleLine                 []byte // the first assistant line inside the window, for the drift shape
}

type window struct {
	from, to time.Time
	loc      *time.Location
}

func (w window) contains(t time.Time) bool { return !t.Before(w.from) && !t.After(w.to) }

func (w window) day(t time.Time) string { return t.In(w.loc).Format(time.DateOnly) }

// Compute reads the sessions and returns the insights, or nil when the window has no
// output tokens (nothing to show, not an error). Format drift is a *usage.Error.
func Compute(ctx context.Context, opts Options) (*Result, error) {
	loc := opts.Location
	if loc == nil {
		loc = time.UTC
	}
	win := window{from: opts.Now.Add(-time.Duration(opts.WindowDays) * 24 * time.Hour), to: opts.Now.Add(24 * time.Hour), loc: loc}
	files, err := list(filepath.Join(opts.Root, "projects"), win.from)
	if err != nil {
		return nil, err
	}
	var data []*fileData
	unreadable, corrupt := 0, 0
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read sessions: %w", err)
		}
		d, bad, err := readFile(f, win)
		corrupt += bad
		if err != nil {
			unreadable++
			continue
		}
		data = append(data, d)
	}
	if opts.Logger != nil {
		opts.Logger.Debug("Read Claude Code sessions", "files", len(files), "unreadable", unreadable, "corruptLines", corrupt)
	}
	return aggregate(data, opts, win)
}

type sessionFile struct {
	path     string
	session  string
	subagent bool
}

// list finds `projects/*/<session>.jsonl` and `projects/*/<session>/subagents/*.jsonl`
// modified since `from`. A folder that cannot be read is skipped.
func list(projects string, from time.Time) ([]sessionFile, error) {
	projectDirs, err := os.ReadDir(projects)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, &usage.Error{Code: usage.CodeUnavailable, Message: "read Claude Code sessions folder: " + err.Error()}
	}
	var files []sessionFile
	recent := func(e fs.DirEntry) bool {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".jsonl") {
			return false
		}
		info, err := e.Info()
		return err == nil && !info.ModTime().Before(from)
	}
	for _, project := range projectDirs {
		if !project.IsDir() {
			continue
		}
		dir := filepath.Join(projects, project.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			id := strings.TrimSuffix(e.Name(), ".jsonl")
			key := project.Name() + "/" + id
			switch {
			case e.IsDir():
				subs, err := os.ReadDir(filepath.Join(dir, e.Name(), "subagents"))
				if err != nil {
					continue
				}
				for _, s := range subs {
					if recent(s) {
						files = append(files, sessionFile{filepath.Join(dir, e.Name(), "subagents", s.Name()), project.Name() + "/" + e.Name(), true})
					}
				}
			case recent(e):
				files = append(files, sessionFile{filepath.Join(dir, e.Name()), key, false})
			}
		}
	}
	return files, nil
}

// readFile reads one transcript; the second result counts the corrupt lines skipped.
func readFile(f sessionFile, win window) (*fileData, int, error) {
	file, err := os.Open(f.path)
	if err != nil {
		return nil, 0, fmt.Errorf("open session: %w", err)
	}
	defer func() { _ = file.Close() }()
	d := &fileData{session: f.session, subagent: f.subagent, dayFirst: map[string]time.Time{}}
	reader := bufio.NewReaderSize(file, 1<<20)
	corrupt := 0
	for {
		raw, tooLong, err := readLine(reader)
		if len(raw) > 0 || tooLong {
			if tooLong || !d.add(raw, win) {
				corrupt++
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return d, corrupt, nil
			}
			return nil, corrupt, fmt.Errorf("read session: %w", err)
		}
	}
}

// readLine returns the next line without its terminator. A line longer than maxLineBytes
// is consumed and reported as tooLong, with no content.
func readLine(r *bufio.Reader) (raw []byte, tooLong bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(raw)+len(chunk) > maxLineBytes {
				tooLong, raw = true, nil
			} else {
				raw = append(raw, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(raw, "\r\n"), tooLong, err
	}
}

// add reads one line; false when it is not valid JSON.
func (d *fileData) add(raw []byte, win window) bool {
	var l line
	if err := json.Unmarshal(raw, &l); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return false
		}
		// A field of another type is ignored; the rest of the line is still read.
	}
	if d.cwd == "" {
		d.cwd = l.CWD
	}
	at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		return true // a line without a usable timestamp has no day: nothing to count
	}
	if d.first.IsZero() || at.Before(d.first) {
		d.first = at
	}
	if at.After(d.last) {
		d.last = at
	}
	if !win.contains(at) {
		return true
	}
	if day := win.day(at); !d.subagent {
		if known, ok := d.dayFirst[day]; !ok || at.Before(known) {
			d.dayFirst[day] = at
		}
	}
	if l.Type != "assistant" {
		return true
	}
	d.assistantLines++
	if d.sampleLine == nil {
		d.sampleLine = slices.Clone(raw)
	}
	var m message
	if json.Unmarshal(l.Message, &m) != nil {
		return true
	}
	var u map[string]any
	if json.Unmarshal(m.Usage, &u) == nil {
		if output, ok := number(u, "output_tokens"); ok {
			d.withOutput++
			d.responses = append(d.responses, response{
				requestID: l.RequestID, output: output, at: at,
				context: orZero(number(u, "input_tokens")) + orZero(number(u, "cache_read_input_tokens")) + orZero(number(u, "cache_creation_input_tokens")),
			})
		}
	}
	for _, rawBlock := range m.Content {
		var b block
		if json.Unmarshal(rawBlock, &b) == nil && b.Type == "tool_use" && b.Name != "" {
			d.tools = append(d.tools, toolCall{id: b.ID, name: b.Name, at: at})
		}
	}
	return true
}

func number(u map[string]any, key string) (int64, bool) {
	v, ok := u[key].(float64)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return int64(v), true
}

func orZero(v int64, _ bool) int64 { return v }

// aggregate merges the files into the result.
func aggregate(data []*fileData, opts Options, win window) (*Result, error) {
	assistantLines, withOutput := 0, 0
	var sample []byte
	for _, d := range data {
		assistantLines += d.assistantLines
		withOutput += d.withOutput
		if sample == nil {
			sample = d.sampleLine
		}
	}
	if assistantLines > 0 && withOutput == 0 {
		return nil, driftError(sample)
	}

	skipped := map[string]bool{}
	for _, d := range data {
		if opts.ProbeDir != "" && sameFolder(d.cwd, opts.ProbeDir) {
			skipped[d.session] = true
		}
	}
	duration := map[string]time.Duration{}
	sessions := 0
	firstStart := map[string]time.Time{}
	for _, d := range data {
		if skipped[d.session] || d.subagent {
			continue
		}
		sessions++
		duration[d.session] = d.last.Sub(d.first)
		for day, at := range d.dayFirst {
			if known, ok := firstStart[day]; !ok || at.Before(known) {
				firstStart[day] = at
			}
		}
	}

	// One count per API response, across every file: the lines of a response repeat its
	// usage, and a subagent file can repeat a response of the main file. Of the copies the
	// largest output wins (a streamed response may be written before it is complete) and
	// the earliest timestamp gives its day.
	type counted struct {
		response
		session string
	}
	byRequest := map[string]*counted{}
	var responses []*counted
	seenTools := map[string]bool{}
	toolCounts := map[string]int{}
	totalTools := 0
	for _, d := range data {
		if skipped[d.session] {
			continue
		}
		for _, r := range d.responses {
			if r.requestID != "" {
				if known, ok := byRequest[r.requestID]; ok {
					if r.output > known.output {
						known.output, known.context = r.output, r.context
					}
					if r.at.Before(known.at) {
						known.at = r.at
					}
					continue
				}
			}
			c := &counted{r, d.session}
			if r.requestID != "" {
				byRequest[r.requestID] = c
			}
			responses = append(responses, c)
		}
		for _, t := range d.tools {
			if t.id != "" {
				if seenTools[t.id] {
					continue
				}
				seenTools[t.id] = true
			}
			toolCounts[t.name]++
			totalTools++
		}
	}

	res := &Result{WindowDays: opts.WindowDays, SessionsAnalyzed: sessions, FirstSessionStartByDay: map[string]string{}}
	daily := map[string]*DailyTokens{}
	for _, r := range responses {
		if r.output <= 0 || !win.contains(r.at) {
			continue
		}
		day := win.day(r.at)
		bucket := daily[day]
		if bucket == nil {
			bucket = &DailyTokens{Date: day}
			daily[day] = bucket
		}
		res.TotalOutputTokens += r.output
		bucket.OutputTokens += r.output
		if r.context > HighContextThreshold {
			res.HighContextOutputTokens += r.output
			bucket.HighContextOutputTokens += r.output
		}
		if duration[r.session] >= LongSession {
			res.LongSessionOutputTokens += r.output
		}
	}
	if res.TotalOutputTokens == 0 {
		return nil, nil //nolint:nilnil // no output tokens in the window is "nothing to show", not an error
	}
	res.ComputedAt = opts.Now.UTC().Format(time.RFC3339)
	res.HighContextSharePercent = percent(res.HighContextOutputTokens, res.TotalOutputTokens)
	res.LongSessionSharePercent = percent(res.LongSessionOutputTokens, res.TotalOutputTokens)
	res.Daily = make([]DailyTokens, 0, len(daily))
	for _, b := range daily {
		res.Daily = append(res.Daily, *b)
	}
	sort.Slice(res.Daily, func(i, j int) bool { return res.Daily[i].Date < res.Daily[j].Date })
	for day, at := range firstStart {
		res.FirstSessionStartByDay[day] = at.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	res.TopTools = topTools(toolCounts, totalTools)
	return res, nil
}

func percent(part, total int64) float64 {
	return math.Round(float64(part)/float64(total)*1000) / 10
}

func topTools(counts map[string]int, total int) []ToolShare {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > topToolsLimit {
		names = names[:topToolsLimit]
	}
	shares := make([]ToolShare, 0, len(names))
	for _, name := range names {
		shares = append(shares, ToolShare{Name: name, SharePercent: math.Round(float64(counts[name])/float64(total)*1000) / 10})
	}
	return shares
}

// driftError is the format drift of a window with assistant lines and no usage: the shape
// (keys and types) of one of them, never a value.
func driftError(sample []byte) *usage.Error {
	var decoded any
	_ = json.Unmarshal(sample, &decoded)
	return &usage.Error{
		Code: usage.CodeFormatDrift, Message: "Unrecognized Claude Code session format",
		Endpoint: Endpoint, Shape: usage.ExtractShape(decoded),
	}
}

// sameFolder compares two folders as real paths, case-insensitively on Windows and macOS
// (their default file systems), like Claude Code's own recorded cwd.
func sameFolder(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	resolve := func(p string) string {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		return filepath.Clean(p)
	}
	a, b = resolve(a), resolve(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
