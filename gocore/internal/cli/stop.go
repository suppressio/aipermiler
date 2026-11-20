package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/config"
	"github.com/suppressio/aipermiler/gocore/internal/service"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const maxStopTimeout = 5 * time.Minute

// runService dispatches `service <action>`; stop is the only action (#286).
func runService(env Env, args []string, jsonMode bool) int {
	if len(args) == 0 || args[0] != "stop" {
		return usageError(env, jsonMode, "service needs stop")
	}
	flags := newFlags("service stop")
	jsonFlag := flags.Bool("json", false, "Print the versioned JSON document")
	timeout := flags.Duration("timeout", service.DefaultStopTimeout, "How long to wait for the service to stop")
	if err := flags.Parse(args[1:]); err != nil {
		return flagError(env, jsonMode, err)
	}
	// Parsing stops at the first positional argument: the pre-parse guess of
	// jsonMode answers that usage error, as for the other commands.
	if flags.NArg() != 0 {
		return usageError(env, jsonMode, "Unexpected arguments")
	}
	jsonMode = *jsonFlag
	if *timeout <= 0 || *timeout > maxStopTimeout {
		return usageError(env, jsonMode, fmt.Sprintf("Timeout must be positive and at most %s", maxStopTimeout))
	}
	dir, err := config.Dir()
	if err != nil {
		return stopFailure(env, jsonMode, err)
	}
	ctx := env.Context
	if ctx == nil {
		ctx = context.Background()
	}
	status, err := service.Stop(ctx, service.StopOptions{Dir: dir, Timeout: *timeout})
	if err != nil {
		return stopFailure(env, jsonMode, err)
	}
	code, text := ExitOK, "The service stopped"
	switch status {
	case service.StopNotRunning:
		code, text = ExitNotRunning, "The service is not running"
	case service.StopTimeout:
		code, text = ExitStopTimeout, "The service did not stop in time"
	case service.StopStopped:
	}
	if jsonMode {
		return outputResult(env, json.NewEncoder(env.Stdout).Encode(struct {
			SchemaVersion int    `json:"schemaVersion"`
			Status        string `json:"status"`
		}{usage.SchemaVersion, string(status)}), code)
	}
	_, err = fmt.Fprintln(env.Stdout, text)
	return outputResult(env, err, code)
}

func stopFailure(env Env, jsonMode bool, err error) int {
	if jsonMode {
		return outputResult(env, usage.WriteError(env.Stdout, "", usage.ClassifyError(err)), ExitProviderError)
	}
	_, _ = fmt.Fprintln(env.Stderr, oneLine(err.Error()))
	return ExitProviderError
}
