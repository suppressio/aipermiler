package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/claude"
	"github.com/suppressio/aipermiler/gocore/internal/insights"
	"github.com/suppressio/aipermiler/gocore/internal/logging"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const (
	defaultInsightsDays = 30
	maxInsightsDays     = 366
)

// runInsights: `insights claude [--json] [--days N] [--timeout d]`, the local insights read
// from Claude Code's session files (#298).
func runInsights(env Env, args []string, jsonMode bool) int {
	if len(args) == 0 {
		return usageError(env, jsonMode, "A provider is required")
	}
	if args[0] == "--help" || args[0] == "-h" {
		return flagError(env, jsonMode, flag.ErrHelp)
	}
	if args[0] != "claude" {
		return usageError(env, jsonMode, "Unknown provider for insights: "+args[0])
	}
	flags := newFlags("insights claude")
	jsonFlag := flags.Bool("json", false, "Print the versioned JSON document")
	days := flags.Int("days", defaultInsightsDays, "Window in days")
	timeout := flags.Duration("timeout", 2*time.Minute, "Deadline")
	if err := flags.Parse(args[1:]); err != nil {
		return flagError(env, jsonMode, err)
	}
	if flags.NArg() != 0 {
		return usageError(env, jsonMode, "Unexpected arguments")
	}
	jsonMode = *jsonFlag
	if *days < 1 || *days > maxInsightsDays {
		return usageError(env, jsonMode, fmt.Sprintf("Days must be between 1 and %d", maxInsightsDays))
	}
	if *timeout <= 0 || *timeout > 2*time.Minute {
		return usageError(env, jsonMode, "Timeout must be greater than 0 and at most 2m0s")
	}

	root, err := claudeRoot(env)
	if err != nil {
		return insightsFailure(env, jsonMode, err)
	}
	logger := logging.New(env.Stderr)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := insights.Compute(ctx, insights.Options{
		Root: root, WindowDays: *days, ProbeDir: probeDir(env, logger), Now: env.Now(), Location: env.Location, Logger: logger,
	})
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return insightsFailure(env, jsonMode, err)
	}
	if jsonMode {
		return outputResult(env, usage.WriteInsights(env.Stdout, "claude", result), ExitOK)
	}
	return outputResult(env, writeInsightsText(env.Stdout, result, *days), ExitOK)
}

func claudeRoot(env Env) (string, error) {
	if env.ClaudeConfigDir != "" {
		return env.ClaudeConfigDir, nil
	}
	return insights.Root(os.Getenv, os.UserHomeDir)
}

// probeDir is the folder whose sessions are skipped; when it cannot be located the probes
// cannot run either, so nothing needs skipping.
func probeDir(env Env, logger *slog.Logger) string {
	if env.ClaudeProbeDir != "" {
		return env.ClaudeProbeDir
	}
	dir, err := claude.ProbeDir()
	if err != nil {
		logger.Debug("Claude probe folder not located; no session skipped")
		return ""
	}
	return dir
}

func insightsFailure(env Env, jsonMode bool, err error) int {
	classified := usage.ClassifyError(err)
	if jsonMode {
		return outputResult(env, usage.WriteError(env.Stdout, "claude", classified), ExitProviderError)
	}
	_, _ = fmt.Fprintf(env.Stderr, "claude: %s\n", oneLine(classified.Message))
	return ExitProviderError
}

func writeInsightsText(w io.Writer, r *insights.Result, days int) error {
	if r == nil {
		_, err := fmt.Fprintf(w, "Claude local insights · no output tokens in the last %d days\n", days)
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Claude local insights · last %d days · %d sessions · %s output tokens\n", r.WindowDays, r.SessionsAnalyzed, thousands(r.TotalOutputTokens))
	fmt.Fprintf(&b, "High context %s%% · long sessions %s%%\n", trim(r.HighContextSharePercent), trim(r.LongSessionSharePercent))
	if len(r.TopTools) > 0 {
		tools := make([]string, 0, len(r.TopTools))
		for _, t := range r.TopTools {
			tools = append(tools, fmt.Sprintf("%s %s%%", t.Name, trim(t.SharePercent)))
		}
		fmt.Fprintf(&b, "Top tools: %s\n", strings.Join(tools, ", "))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func trim(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func thousands(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
