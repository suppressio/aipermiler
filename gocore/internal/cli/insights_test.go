package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/cli"
	"github.com/suppressio/aipermiler/gocore/internal/provider"
)

func insightsSession(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "projects", "-synthetic")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","timestamp":"2026-10-31T08:00:00.000Z","cwd":"/work","message":{"content":"synthetic"}}`,
		`{"type":"assistant","timestamp":"2026-10-31T09:00:00.000Z","requestId":"req_1","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}],"usage":{"input_tokens":200000,"output_tokens":1000}}}`,
		`{"type":"assistant","timestamp":"2026-10-31T09:00:00.000Z","requestId":"req_1","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{}}],"usage":{"input_tokens":200000,"output_tokens":1000}}}`,
		`{"type":"assistant","timestamp":"2026-10-31T10:00:00.000Z","requestId":"req_2","message":{"content":[],"usage":{"input_tokens":10,"output_tokens":3000}}}`,
	}
	path := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fixedNow(), fixedNow()); err != nil {
		t.Fatal(err)
	}
}

func runInsights(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := cli.Env{Providers: provider.Registry{"fake": standardFake}, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Now: fixedNow, Location: time.UTC, ClaudeConfigDir: root, ClaudeProbeDir: filepath.Join(root, "no-probe")}
	code := cli.RunWith(env, args)
	return code, stdout.String(), stderr.String()
}

func TestInsightsContractFixtures(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	insightsSession(t, root)
	code, out, _ := runInsights(t, root, "insights", "claude", "--json", "--days", "7")
	if want := fixture(t, "insights-claude"); code != cli.ExitOK || out != string(want) {
		t.Errorf("exit = %d\noutput = %s\nwant = %s", code, out, want)
	}
	code, out, _ = runInsights(t, t.TempDir(), "insights", "claude", "--json")
	if want := fixture(t, "insights-empty"); code != cli.ExitOK || out != string(want) {
		t.Errorf("exit = %d\noutput = %s\nwant = %s", code, out, want)
	}
	drift := t.TempDir()
	dir := filepath.Join(drift, "projects", "-synthetic")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","timestamp":"2026-10-31T09:00:00.000Z","requestId":"req_1","message":{"content":[{"type":"text","text":"PRIVATE"}],"tokens":{"out":1}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runInsights(t, drift, "insights", "claude", "--json")
	if want := fixture(t, "error-insights-drift"); code != cli.ExitProviderError || out != string(want) {
		t.Errorf("exit = %d\noutput = %s\nwant = %s", code, out, want)
	}
}

func TestInsightsText(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	insightsSession(t, root)
	code, out, _ := runInsights(t, root, "insights", "claude", "--days", "7")
	if code != cli.ExitOK || !strings.Contains(out, "4,000 output tokens") || !strings.Contains(out, "Bash 50%") || strings.Contains(out, "{") {
		t.Errorf("exit = %d, output = %q", code, out)
	}
	_, out, _ = runInsights(t, t.TempDir(), "insights", "claude")
	if !strings.Contains(out, "no output tokens") {
		t.Errorf("output = %q", out)
	}
}

func TestInsightsUsageErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"insights"}, {"insights", "nope"}, {"insights", "claude", "extra"}, {"insights", "claude", "--days", "0"},
		{"insights", "claude", "--days", "-1"}, {"insights", "claude", "--days", "367"}, {"insights", "claude", "--days", "x"},
		{"insights", "claude", "--timeout", "0"}, {"insights", "claude", "--unknown"},
	} {
		for _, jsonMode := range []bool{false, true} {
			full := append([]string{}, args...)
			if jsonMode {
				full = append(full, "--json")
			}
			t.Run(fmt.Sprint(full), func(t *testing.T) {
				t.Parallel()
				code, out, errOut := runInsights(t, t.TempDir(), full...)
				if code != cli.ExitUsage {
					t.Fatalf("exit = %d; want 2", code)
				}
				if jsonMode && (!strings.Contains(out, `"code":"usage"`) || errOut != "") {
					t.Errorf("json: out %q err %q", out, errOut)
				}
				var doc map[string]any
				if jsonMode && json.Unmarshal([]byte(out), &doc) != nil {
					t.Errorf("not json: %q", out)
				}
				if !jsonMode && (out != "" || errOut == "") {
					t.Errorf("text: out %q err %q", out, errOut)
				}
			})
		}
	}
}
