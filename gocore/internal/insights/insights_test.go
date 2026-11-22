package insights_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/insights"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// All sessions here are synthetic: ids, paths and numbers are invented.

var now = time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)

type tree struct {
	t    *testing.T
	root string
}

func newTree(t *testing.T) *tree {
	t.Helper()
	return &tree{t: t, root: t.TempDir()}
}

// write puts lines in projects/<rel> and sets its modification time.
func (tr *tree) write(rel string, mtime time.Time, lines ...string) {
	tr.t.Helper()
	path := filepath.Join(tr.root, "projects", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr *tree) compute(probe string, loc *time.Location) (*insights.Result, error) {
	tr.t.Helper()
	return insights.Compute(context.Background(), insights.Options{
		Root: tr.root, WindowDays: 7, ProbeDir: probe, Now: now, Location: loc,
	})
}

func (tr *tree) must() *insights.Result {
	tr.t.Helper()
	res, err := tr.compute("", time.UTC)
	if err != nil {
		tr.t.Fatal(err)
	}
	if res == nil {
		tr.t.Fatal("no insights")
	}
	return res
}

func at(h int) string { return now.Add(time.Duration(h-12) * time.Hour).Format(time.RFC3339Nano) }

func assistant(ts, req string, out, in, cacheRead int, blocks ...string) string {
	content := append([]string{}, blocks...)
	if len(content) == 0 {
		content = append(content, `{"type":"text","text":"synthetic reply"}`)
	}
	reqField := ""
	if req != "" {
		reqField = fmt.Sprintf(`"requestId":%q,`, req)
	}
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,%s"message":{"role":"assistant","content":[%s],"usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":0,"output_tokens":%d}}}`,
		ts, reqField, strings.Join(content, ","), in, cacheRead, out)
}

func tool(id, name string) string {
	return fmt.Sprintf(`{"type":"tool_use","id":%q,"name":%q,"input":{"x":"secret"}}`, id, name)
}

func user(ts, cwd string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"cwd":%q,"message":{"role":"user","content":"synthetic prompt"}}`, ts, cwd)
}

func TestOneCountPerResponse(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	// One response written as three lines (one per content block), each repeating the usage.
	tr.write("-p/s1.jsonl", now,
		assistant(at(9), "req_1", 100, 10, 0),
		assistant(at(9), "req_1", 100, 10, 0, tool("t1", "Bash")),
		assistant(at(9), "req_1", 100, 10, 0, tool("t2", "Read")),
		assistant(at(10), "", 50, 10, 0),
		assistant(at(10), "", 50, 10, 0),
	)
	res := tr.must()
	if res.TotalOutputTokens != 200 {
		t.Errorf("total = %d; want 100 (one response) + 50 + 50 (no requestId, each its own)", res.TotalOutputTokens)
	}
	if len(res.TopTools) != 2 || res.TopTools[0].SharePercent != 50 {
		t.Errorf("tools = %+v", res.TopTools)
	}
}

func TestToolDedupByID(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/s1.jsonl", now,
		assistant(at(9), "req_1", 10, 1, 0, tool("t1", "Bash")),
		assistant(at(9), "req_2", 10, 1, 0, tool("t1", "Bash")),
		assistant(at(9), "req_3", 10, 1, 0, tool("t2", "Bash"), tool("", "Read"), tool("", "Read")),
	)
	res := tr.must()
	// Bash twice (t1, t2) and Read twice (no id: each counts).
	if len(res.TopTools) != 2 || res.TopTools[0].SharePercent != 50 || res.TopTools[1].SharePercent != 50 {
		t.Errorf("tools = %+v", res.TopTools)
	}
}

func TestCompactedSessionCountsEveryLine(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/s1.jsonl", now,
		assistant(at(1), "req_a", 1000, 10, 0),
		`{"type":"system","subtype":"compact_boundary","timestamp":"`+at(2)+`"}`,
		assistant(at(3), "req_b", 500, 10, 0),
	)
	if got := tr.must().TotalOutputTokens; got != 1500 {
		t.Errorf("total = %d; want the part before the compaction too (1500)", got)
	}
}

func TestSubagentRepeatingMainResponse(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/s1.jsonl", now, user(at(8), "/work"), assistant(at(9), "req_1", 100, 10, 0, tool("t1", "Task")))
	tr.write("-p/s1/subagents/agent-1.jsonl", now,
		assistant(at(9), "req_1", 100, 10, 0, tool("t1", "Task")), // the same response, written here too
		assistant(at(9), "req_2", 40, 10, 0, tool("t9", "Grep")),
	)
	res := tr.must()
	if res.TotalOutputTokens != 140 {
		t.Errorf("total = %d; want 140", res.TotalOutputTokens)
	}
	if res.SessionsAnalyzed != 1 {
		t.Errorf("sessions = %d; a subagent file is not a session", res.SessionsAnalyzed)
	}
	if len(res.TopTools) != 2 {
		t.Errorf("tools = %+v; want Task and Grep once each", res.TopTools)
	}
}

func TestResponseCrossingMidnight(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	loc := time.FixedZone("UTC+2", 2*3600)
	// 21:59:58Z is 23:59:58 local on the 18th, 22:00:03Z is 00:00:03 on the 19th.
	first := time.Date(2026, time.November, 18, 21, 59, 58, 0, time.UTC).Format(time.RFC3339)
	second := time.Date(2026, time.November, 18, 22, 0, 3, 0, time.UTC).Format(time.RFC3339)
	tr.write("-p/s1.jsonl", now,
		assistant(first, "req_1", 100, 10, 0),
		assistant(second, "req_1", 100, 10, 0),
		assistant(second, "req_2", 30, 10, 0),
	)
	res, err := tr.compute("", loc)
	if err != nil || res == nil {
		t.Fatalf("%v %v", res, err)
	}
	want := []insights.DailyTokens{{Date: "2026-11-18", OutputTokens: 100}, {Date: "2026-11-19", OutputTokens: 30}}
	if len(res.Daily) != 2 || res.Daily[0] != want[0] || res.Daily[1] != want[1] {
		t.Errorf("daily = %+v; want %+v (the response counts once, on its first line's local day)", res.Daily, want)
	}
}

func TestTimestampOutsideWindowIsDropped(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	old := now.AddDate(0, 0, -10).Format(time.RFC3339)
	tr.write("-p/s1.jsonl", now, assistant(old, "req_old", 900, 1, 0), assistant(at(9), "req_new", 7, 1, 0))
	tr.write("-p/old.jsonl", now.AddDate(0, 0, -10), assistant(old, "req_older", 500, 1, 0))
	res := tr.must()
	if res.TotalOutputTokens != 7 || res.SessionsAnalyzed != 1 {
		t.Errorf("total = %d, sessions = %d; want 7 and 1", res.TotalOutputTokens, res.SessionsAnalyzed)
	}
}

func TestThresholdsAndShares(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	// Context = input + cache_read + cache_creation: 150_000 is not high, 150_001 is.
	tr.write("-p/short.jsonl", now,
		assistant(at(9), "r1", 100, 100_000, 50_000),
		assistant(at(9), "r2", 300, 100_000, 50_001),
	)
	// A session of exactly 8 h is long.
	tr.write("-p/long.jsonl", now,
		user(at(0), "/work"),
		assistant(at(8), "r3", 600, 10, 0),
	)
	res := tr.must()
	if res.TotalOutputTokens != 1000 || res.HighContextOutputTokens != 300 || res.LongSessionOutputTokens != 600 {
		t.Errorf("%+v", res)
	}
	if res.HighContextSharePercent != 30 || res.LongSessionSharePercent != 60 {
		t.Errorf("shares %v %v", res.HighContextSharePercent, res.LongSessionSharePercent)
	}
	if got := res.Daily[0].HighContextOutputTokens; got != 300 {
		t.Errorf("daily high context = %d", got)
	}
}

func TestShareRoundedToOneDecimal(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/s1.jsonl", now,
		assistant(at(9), "r1", 10, 1, 0, tool("a", "A")),
		assistant(at(9), "r2", 10, 1, 0, tool("b", "B")),
		assistant(at(9), "r3", 10, 1, 0, tool("c", "C")),
	)
	for _, share := range tr.must().TopTools {
		if share.SharePercent != 33.3 {
			t.Errorf("share = %v; want 33.3", share.SharePercent)
		}
	}
}

func TestTopToolsLimit(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	var lines []string
	for i := range 7 {
		for j := 0; j <= i; j++ {
			lines = append(lines, assistant(at(9), fmt.Sprintf("r%d-%d", i, j), 1, 1, 0, tool(fmt.Sprintf("t%d-%d", i, j), fmt.Sprintf("Tool%d", i))))
		}
	}
	tr.write("-p/s1.jsonl", now, lines...)
	res := tr.must()
	if len(res.TopTools) != 5 || res.TopTools[0].Name != "Tool6" {
		t.Errorf("tools = %+v", res.TopTools)
	}
}

func TestProbeFolderSkipped(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	probe := filepath.Join(t.TempDir(), "claude-probe")
	if err := os.Mkdir(probe, 0o700); err != nil {
		t.Fatal(err)
	}
	tr.write("-probe/p1.jsonl", now, user(at(5), probe), assistant(at(5), "rp", 777, 1, 0))
	tr.write("-probe/p1/subagents/agent-1.jsonl", now, assistant(at(5), "rp2", 555, 1, 0))
	tr.write("-w/w1.jsonl", now, user(at(9), "/work"), assistant(at(9), "rw", 5, 1, 0))
	res, err := tr.compute(probe, time.UTC)
	if err != nil || res == nil {
		t.Fatalf("%v %v", res, err)
	}
	if res.TotalOutputTokens != 5 || res.SessionsAnalyzed != 1 {
		t.Errorf("total = %d, sessions = %d; the probe session and its subagent must be skipped", res.TotalOutputTokens, res.SessionsAnalyzed)
	}
	if got := res.FirstSessionStartByDay["2026-11-20"]; got != "2026-11-20T09:00:00.000Z" {
		t.Errorf("first start = %q; the probe session must not move it", got)
	}
}

func TestFirstSessionStartByDay(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/a.jsonl", now, user(at(10), "/w"), assistant(at(11), "r1", 5, 1, 0))
	tr.write("-p/b.jsonl", now, user(at(7), "/w"), assistant(at(8), "r2", 5, 1, 0))
	tr.write("-p/b/subagents/agent-1.jsonl", now, assistant(at(3), "r3", 5, 1, 0)) // not a main session
	got := tr.must().FirstSessionStartByDay
	if len(got) != 1 || got["2026-11-20"] != "2026-11-20T07:00:00.000Z" {
		t.Errorf("first starts = %v", got)
	}
}

// A session still writing after midnight did not start the next day: pacing would read
// the working day as starting at 00:xx (budget.todayActivitySpan).
func TestFirstSessionStartIgnoresLinesAfterMidnight(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/night.jsonl", now, user(at(-2), "/w"), assistant(at(0)[:11]+"00:05:00Z", "r1", 5, 1, 0))
	tr.write("-p/day.jsonl", now, user(at(9), "/w"), assistant(at(9), "r2", 5, 1, 0))
	got := tr.must().FirstSessionStartByDay
	if got["2026-11-20"] != "2026-11-20T09:00:00.000Z" || got["2026-11-19"] != "2026-11-19T22:00:00.000Z" || len(got) != 2 {
		t.Errorf("first starts = %v", got)
	}
}

// A field of another type inside `message` (here `content` as a string) must not drop
// the usage that is there.
func TestUsageKeptWhenContentHasAnotherType(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/s1.jsonl", now,
		`{"type":"assistant","timestamp":"`+at(9)+`","requestId":"r1","message":{"content":"plain text","usage":{"output_tokens":7}}}`)
	if got := tr.must().TotalOutputTokens; got != 7 {
		t.Errorf("total = %d; want 7", got)
	}
}

func TestTolerantReading(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/s1.jsonl", now,
		`{"type":"assistant","timestamp":"`+at(9)+`","requestId":"r1","extra":{"a":[1,2]},"message":{"usage":{"output_tokens":10,"input_tokens":1,"new_counter":3},"content":[{"type":"text","text":"x"},"stray",{"type":"tool_use","name":"Bash","future":true}]}}`,
		`{not json`,
		``,
		`{"type":7,"timestamp":["x"]}`,
		`{"type":"assistant","timestamp":"`+at(9)+`","requestId":"r2","message":"a string"}`,
		`{"type":"assistant","timestamp":"`+at(9)+`","requestId":"r3","message":{"usage":{"output_tokens":"many"}}}`,
		`{"type":"assistant","timestamp":"`+at(9)+`","requestId":"r4","message":{"usage":{"output_tokens":5}`, // cut while being written
	)
	tr.write("-p/unreadable.jsonl", now, `{}`)
	if err := os.Chmod(filepath.Join(tr.root, "projects", "-p", "unreadable.jsonl"), 0); err != nil {
		t.Fatal(err)
	}
	res := tr.must()
	if res.TotalOutputTokens != 10 {
		t.Errorf("total = %d; want 10", res.TotalOutputTokens)
	}
	if len(res.TopTools) != 1 || res.TopTools[0].Name != "Bash" {
		t.Errorf("tools = %+v", res.TopTools)
	}
}

func TestFileDisappearsOrIsNotAFile(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/s1.jsonl", now, assistant(at(9), "r1", 3, 1, 0))
	if err := os.MkdirAll(filepath.Join(tr.root, "projects", "-p", "dir.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tr.root, "projects", "stray.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := tr.must().TotalOutputTokens; got != 3 {
		t.Errorf("total = %d", got)
	}
}

func TestNoSessionsAndNoOutput(t *testing.T) {
	t.Parallel()
	empty := newTree(t)
	if res, err := empty.compute("", time.UTC); res != nil || err != nil {
		t.Errorf("no projects folder: %v %v", res, err)
	}
	zero := newTree(t)
	zero.write("-p/s1.jsonl", now, assistant(at(9), "r1", 0, 1, 0))
	if res, err := zero.compute("", time.UTC); res != nil || err != nil {
		t.Errorf("no output tokens: %v %v", res, err)
	}
}

func TestFormatDrift(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/s1.jsonl", now,
		user(at(8), "/work"),
		`{"type":"assistant","timestamp":"`+at(9)+`","requestId":"r1","message":{"role":"assistant","content":[{"type":"text","text":"PRIVATE TEXT"}],"tokens":{"out":42,"in":7}}}`,
	)
	_, err := tr.compute("", time.UTC)
	var e *usage.Error
	if !errors.As(err, &e) || e.Code != usage.CodeFormatDrift {
		t.Fatalf("err = %v; want format_drift", err)
	}
	data, _ := json.Marshal(e)
	text := string(data)
	for _, leak := range []string{"PRIVATE TEXT", "42", "r1"} {
		if strings.Contains(text, leak) {
			t.Errorf("shape leaks %q: %s", leak, text)
		}
	}
	for _, key := range []string{`"tokens":{"in":"number","out":"number"}`, `"requestId":"string"`, `"timestamp":"string"`} {
		if !strings.Contains(text, key) {
			t.Errorf("shape lacks %s: %s", key, text)
		}
	}
}

func TestNoDriftWhenAssistantLinesAreOutsideTheWindow(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	old := now.AddDate(0, 0, -20).Format(time.RFC3339)
	tr.write("-p/s1.jsonl", now, `{"type":"assistant","timestamp":"`+old+`","message":{}}`)
	if res, err := tr.compute("", time.UTC); res != nil || err != nil {
		t.Errorf("%v %v", res, err)
	}
}

func TestRoot(t *testing.T) {
	t.Parallel()
	home := func() (string, error) { return filepath.Join("home", "synthetic"), nil }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got, _ := insights.Root(env(map[string]string{"CLAUDE_CONFIG_DIR": "custom"}), home); got != "custom" {
		t.Errorf("config dir = %q", got)
	}
	if got, _ := insights.Root(env(nil), home); got != filepath.Join("home", "synthetic", ".claude") {
		t.Errorf("default = %q", got)
	}
	if _, err := insights.Root(env(nil), func() (string, error) { return "", errors.New("no home") }); err == nil {
		t.Error("want an error without a home folder")
	}
}

func TestCancelled(t *testing.T) {
	t.Parallel()
	tr := newTree(t)
	tr.write("-p/s1.jsonl", now, assistant(at(9), "r1", 3, 1, 0))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := insights.Compute(ctx, insights.Options{Root: tr.root, WindowDays: 7, Now: now, Location: time.UTC})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}
