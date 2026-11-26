// Command pacingparity prints the Go pacing readings of parity scenarios as a
// JSON array, for scripts/pacing-parity.js (#325). Run from gocore/:
// `go run ./cmd/pacingparity scenario.json...`; a folder argument reads every
// *.json file in it, in name order.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	_ "time/tzdata" // the scenarios name IANA zones; do not depend on the host

	"github.com/suppressio/aipermiler/gocore/internal/pacingparity"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: pacingparity <scenario.json | folder>...")
		return 2
	}
	var files []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return fail(err)
		}
		if !info.IsDir() {
			files = append(files, arg)
			continue
		}
		found, err := filepath.Glob(filepath.Join(arg, "*.json"))
		if err != nil {
			return fail(err)
		}
		files = append(files, found...)
	}
	outputs := make([]pacingparity.Output, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return fail(err)
		}
		scenario, err := pacingparity.Parse(data)
		if err != nil {
			return fail(fmt.Errorf("%s: %w", file, err))
		}
		out, err := pacingparity.Run(scenario)
		if err != nil {
			return fail(fmt.Errorf("%s: %w", file, err))
		}
		outputs = append(outputs, out)
	}
	if err := json.NewEncoder(os.Stdout).Encode(outputs); err != nil {
		return fail(err)
	}
	return 0
}

func fail(err error) int {
	_, _ = fmt.Fprintln(os.Stderr, "pacingparity:", err)
	return 1
}
