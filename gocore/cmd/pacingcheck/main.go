// Command pacingcheck fails when docs/PACING.md has an id without a test or a
// fixture in internal/pacing, or a section 9 item neither fixed nor intended (#276).
// Run it from gocore/: `go run ./cmd/pacingcheck`.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/suppressio/aipermiler/gocore/internal/pacingcheck"
)

func main() {
	os.Exit(run())
}

func run() int {
	doc := flag.String("doc", "../docs/PACING.md", "the pacing model")
	tests := flag.String("tests", "internal/pacing", "folder searched for tests and fixtures")
	allowFile := flag.String("allow", "pacing-allowlist.txt", "ids deliberately without a test, one `ID reason` per line")
	flag.Parse()

	text, err := os.ReadFile(*doc)
	if err != nil {
		return fail(err)
	}
	f, err := os.Open(*allowFile)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = f.Close() }()
	allow, err := pacingcheck.ParseAllowlist(f)
	if err != nil {
		return fail(err)
	}
	covered, err := pacingcheck.ScanCovered(os.DirFS(*tests), ".")
	if err != nil {
		return fail(err)
	}
	report := pacingcheck.Check(string(text), covered, allow)
	report.Write(os.Stdout)
	if !report.OK() {
		return 1
	}
	return 0
}

func fail(err error) int {
	_, _ = fmt.Fprintln(os.Stderr, "pacing check:", err)
	return 2
}
