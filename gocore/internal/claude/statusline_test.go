package claude_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/claude"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// newSink creates an empty sink file, as the provider does before a probe.
func newSink(t *testing.T) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), claude.SinkPatternForTest)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}

// runStatusLine runs the sink as Claude would, once per document.
func runStatusLine(t *testing.T, sink string, docs ...[]byte) {
	t.Helper()
	for _, doc := range docs {
		if code := claude.Sink([]string{sink}, bytes.NewReader(doc)); code != 0 {
			t.Fatalf("sink exit = %d", code)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func reset(unix int64) *time.Time {
	r := time.Unix(unix, 0).UTC()
	return &r
}

func TestMapStatusLine(t *testing.T) {
	t.Parallel()
	five := usage.QuotaWindow{ID: "claude-5h", Label: "5-hour limit", PeriodType: usage.PeriodRollingHours, PeriodLength: ptr(5.0), Unit: usage.UnitPercentage, Used: 23.5, Total: ptr(100.0), ResetsAt: reset(1793570400)}
	seven := usage.QuotaWindow{ID: "claude-7d", Label: "7-day limit", PeriodType: usage.PeriodRollingDays, PeriodLength: ptr(7.0), Unit: usage.UnitPercentage, Used: 41, Total: ptr(100.0), ResetsAt: reset(1794070800)}
	tests := []struct {
		name  string
		docs  []string
		want  []usage.QuotaWindow
		drift string
	}{
		{"start then usage", []string{"statusline-2.1.316-start", "statusline-2.1.316"}, []usage.QuotaWindow{five, seven}, ""},
		{"a later run without rate_limits", []string{"statusline-2.1.316", "statusline-2.1.316-start"}, []usage.QuotaWindow{five, seven}, ""},
		{"five hours only", []string{"five-hour-only"}, []usage.QuotaWindow{five}, ""},
		{"no rate_limits", []string{"statusline-2.1.316-start"}, nil, "no rate_limits"},
		{"never ran", nil, nil, "never ran"},
		{"unknown windows", []string{"unknown-shape"}, nil, "no quota window"},
		{"malformed window", []string{"malformed-window"}, nil, "no quota window"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sink := newSink(t)
			for _, name := range tt.docs {
				runStatusLine(t, sink, testdata(t, name))
			}
			got, err := claude.MapSinkForTest(sink)
			if tt.drift != "" {
				assertCode(t, err, usage.CodeFormatDrift)
				var e *usage.Error
				if !errors.As(err, &e) || !strings.Contains(e.Message, tt.drift) {
					t.Fatalf("message = %q; want %q", e.Message, tt.drift)
				}
				if _, object := e.Shape.(map[string]any); object == (tt.name == "never ran") || e.Endpoint == "" {
					t.Fatalf("shape = %v, endpoint = %q", e.Shape, e.Endpoint)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.PlanTier != nil || !equalWindows(got.QuotaWindows, tt.want) {
				t.Fatalf("usage = %+v; want %+v", got, tt.want)
			}
		})
	}
}

func TestMapStatusLineSkipsBadWindows(t *testing.T) {
	t.Parallel()
	sink := newSink(t)
	runStatusLine(t, sink, []byte(`{"rate_limits":{"five_hour":{"used_percentage":-1,"resets_at":1793570400},"seven_day":{"resets_at":1794070800}}}`))
	_, err := claude.MapSinkForTest(sink)
	assertCode(t, err, usage.CodeFormatDrift)

	sink = newSink(t)
	runStatusLine(t, sink, []byte(`{"rate_limits":{"five_hour":{"used_percentage":0,"resets_at":-5},"seven_day":{"used_percentage":100,"resets_at":999999999999}}}`))
	got, err := claude.MapSinkForTest(sink)
	if err != nil || len(got.QuotaWindows) != 2 {
		t.Fatalf("usage = %+v, %v", got, err)
	}
	for _, w := range got.QuotaWindows {
		if w.ResetsAt != nil {
			t.Errorf("%s: reset %v kept", w.ID, w.ResetsAt)
		}
	}
}

func equalWindows(a, b []usage.QuotaWindow) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

func TestSink(t *testing.T) {
	t.Parallel()
	sink := newSink(t)
	runStatusLine(t, sink, []byte("{\n  \"cwd\": \"/synthetic\"\n}\n"), []byte(`{"cwd":"/other"}`))
	data, err := os.ReadFile(sink)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\"cwd\":\"/synthetic\"}\n{\"cwd\":\"/other\"}\n" {
		t.Fatalf("sink = %q", data)
	}

	dir := t.TempDir()
	missing := filepath.Join(dir, "claude-status-1.jsonl")
	link := filepath.Join(dir, "claude-status-2.jsonl")
	if err := os.Symlink(sink, link); err != nil {
		t.Logf("no symlink case: %v", err)
		link = ""
	}
	other := filepath.Join(dir, "notes.jsonl")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{"no argument", nil, "{}"},
		{"two arguments", []string{sink, sink}, "{}"},
		{"relative path", []string{filepath.Base(sink)}, "{}"},
		{"not a sink name", []string{other}, "{}"},
		{"missing file", []string{missing}, "{}"},
		{"symlink", []string{link}, "{}"},
		{"not an object", []string{sink}, "[1]"},
		{"not JSON", []string{sink}, "status"},
		{"too large", []string{sink}, `{"x":"` + strings.Repeat("a", 1<<20) + `"}`},
	} {
		if tt.name == "symlink" && link == "" {
			continue
		}
		if code := claude.Sink(tt.args, strings.NewReader(tt.stdin)); code == 0 {
			t.Errorf("%s: exit 0", tt.name)
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Error("the sink created a file")
	}
	if after, _ := os.ReadFile(sink); !bytes.Equal(after, data) {
		t.Errorf("sink changed to %q", after)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	if got := claude.ShellQuoteForTest(`/a b/it's`); got != `'/a b/it'\''s'` {
		t.Fatalf("quote = %s", got)
	}
}

// transcriptLayout builds Claude's projects folder with the probe's own transcript.
type transcriptLayout struct {
	probe, projects, folder, transcript string
}

func newLayout(t *testing.T) transcriptLayout {
	t.Helper()
	root := t.TempDir()
	l := transcriptLayout{probe: filepath.Join(root, "probe"), projects: filepath.Join(root, "claude", "projects")}
	l.folder = filepath.Join(l.projects, "-synthetic-probe")
	l.transcript = filepath.Join(l.folder, "00000000-0000-4000-8000-000000000000.jsonl")
	for _, d := range []string{l.probe, l.folder} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(l.transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return l
}

func reportDoc(t *testing.T, transcript, cwd string) []byte {
	t.Helper()
	doc, err := json.Marshal(map[string]string{"transcript_path": transcript, "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestRemoveTranscripts(t *testing.T) {
	t.Parallel()
	t.Run("own transcript and its empty folder", func(t *testing.T) {
		t.Parallel()
		l := newLayout(t)
		sink := newSink(t)
		runStatusLine(t, sink, reportDoc(t, l.transcript, l.probe), reportDoc(t, l.transcript, l.probe))
		claude.CleanupForTest(sink, l.probe, l.projects)
		if exists(l.transcript) || exists(l.folder) || !exists(l.projects) {
			t.Fatal("transcript or folder kept, or projects removed")
		}
	})
	t.Run("a malformed quota field does not hide the transcript", func(t *testing.T) {
		t.Parallel()
		l := newLayout(t)
		doc, err := json.Marshal(map[string]any{"transcript_path": l.transcript, "cwd": l.probe, "rate_limits": map[string]any{"five_hour": map[string]any{"used_percentage": "12"}}})
		if err != nil {
			t.Fatal(err)
		}
		sink := newSink(t)
		runStatusLine(t, sink, doc)
		claude.CleanupForTest(sink, l.probe, l.projects)
		if exists(l.transcript) {
			t.Fatal("transcript kept")
		}
	})
	t.Run("folder with other files is kept", func(t *testing.T) {
		t.Parallel()
		l := newLayout(t)
		other := filepath.Join(l.folder, "other.jsonl")
		if err := os.WriteFile(other, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		sink := newSink(t)
		runStatusLine(t, sink, reportDoc(t, l.transcript, l.probe))
		claude.CleanupForTest(sink, l.probe, l.projects)
		if exists(l.transcript) || !exists(other) {
			t.Fatal("wrong files removed")
		}
	})
	t.Run("kept", func(t *testing.T) {
		t.Parallel()
		for _, tt := range []struct {
			name string
			doc  func(l transcriptLayout) []byte
			keep func(l transcriptLayout) string
			proj func(l transcriptLayout) string
		}{
			{"another cwd", func(l transcriptLayout) []byte { return reportDoc(t, l.transcript, l.folder) }, nil, nil},
			{"no cwd", func(l transcriptLayout) []byte { return reportDoc(t, l.transcript, "") }, nil, nil},
			{"projects unknown", func(l transcriptLayout) []byte { return reportDoc(t, l.transcript, l.probe) }, nil, func(transcriptLayout) string { return "" }},
			{"projects elsewhere", func(l transcriptLayout) []byte { return reportDoc(t, l.transcript, l.probe) }, nil, func(l transcriptLayout) string { return l.probe }},
			{"not a jsonl", func(l transcriptLayout) []byte { return reportDoc(t, l.transcript+".bak", l.probe) }, func(l transcriptLayout) string { return l.transcript + ".bak" }, nil},
			{"two levels down", func(l transcriptLayout) []byte {
				return reportDoc(t, filepath.Join(l.folder, "sub", "x.jsonl"), l.probe)
			}, func(l transcriptLayout) string { return filepath.Join(l.folder, "sub", "x.jsonl") }, nil},
			{"directly in projects", func(l transcriptLayout) []byte { return reportDoc(t, filepath.Join(l.projects, "x.jsonl"), l.probe) }, func(l transcriptLayout) string { return filepath.Join(l.projects, "x.jsonl") }, nil},
			{"a folder named jsonl", func(l transcriptLayout) []byte { return reportDoc(t, filepath.Join(l.folder, "d.jsonl"), l.probe) }, func(l transcriptLayout) string { return filepath.Join(l.folder, "d.jsonl") }, nil},
		} {
			l := newLayout(t)
			target := l.transcript
			if tt.keep != nil {
				target = tt.keep(l)
				if tt.name == "a folder named jsonl" {
					if err := os.MkdirAll(target, 0o700); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(target, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			projects := l.projects
			if tt.proj != nil {
				projects = tt.proj(l)
			}
			sink := newSink(t)
			runStatusLine(t, sink, tt.doc(l))
			claude.CleanupForTest(sink, l.probe, projects)
			if !exists(target) || !exists(l.transcript) {
				t.Errorf("%s: a file was removed", tt.name)
			}
		}
	})
}

func TestContractFixture(t *testing.T) {
	t.Parallel()
	sink := newSink(t)
	runStatusLine(t, sink, testdata(t, "statusline-2.1.316"))
	got, err := claude.MapSinkForTest(sink)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := usage.WriteSuccess(&out, "claude", got, time.Date(2026, time.November, 1, 18, 30, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "contract", "v1", "usage-claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != string(want) {
		t.Fatalf("document = %s\nwant = %s", out.String(), want)
	}
}

func TestRemoveStaleSinks(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	now := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	create := func(name string, age time.Duration) string {
		path := filepath.Join(base, name)
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stale := create("claude-status-1.jsonl", 11*time.Minute)
	running := create("claude-status-2.jsonl", time.Minute)
	other := create("notes.jsonl", time.Hour)
	folder := filepath.Join(base, "claude-status-3.jsonl")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(folder, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(base, "claude-probe", "claude-status-4.jsonl")
	if err := os.MkdirAll(filepath.Dir(nested), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(nested, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	claude.RemoveStaleSinksForTest(base, now)
	if exists(stale) {
		t.Error("stale sink kept")
	}
	for _, kept := range []string{running, other, folder, nested} {
		if !exists(kept) {
			t.Errorf("%s removed", kept)
		}
	}
}
