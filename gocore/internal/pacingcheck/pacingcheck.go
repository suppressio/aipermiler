// Package pacingcheck verifies that the Go pacing packages cover the whole
// model of docs/PACING.md (#276): every id of the document has a test or a
// fixture, and every known deviation of section 9 is fixed or intended.
//
// An id is a table row's first cell (A-1, G-12, TP-2, E3b, ...) or a bold
// definition opening a line (D-3.20, I-4). A mention in prose is not an id.
// Coverage is a whole-id mention in a *_test.go file or a testdata file under
// the pacing packages; the same id in production code does not count.
package pacingcheck

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
)

const idPattern = `[A-Z]{1,2}-?[0-9]+(?:\.[0-9]+)?[a-z]?`

var (
	tableIDRe  = regexp.MustCompile(`^\|\s*(?:\*\*)?(` + idPattern + `)(?:\*\*)?\s*\|`)
	boldIDRe   = regexp.MustCompile(`^(?:-\s+)?\*\*([A-Z]{1,2}-[0-9]+(?:\.[0-9]+)?[a-z]?)`)
	mentionRe  = regexp.MustCompile(idPattern)
	itemRe     = regexp.MustCompile(`^([0-9]+)\.\s`)
	resolvedRe = regexp.MustCompile(`(?i)\b(fixed|intended)\b`)
)

// Deviation is one numbered item of section 9.
type Deviation struct {
	N        int
	Resolved bool // marked fixed or intended
}

// ExtractIDs returns the ids defined by the document, in document order, once each.
func ExtractIDs(doc string) []string {
	var ids []string
	seen := map[string]bool{}
	for line := range strings.SplitSeq(doc, "\n") {
		m := tableIDRe.FindStringSubmatch(line)
		if m == nil {
			m = boldIDRe.FindStringSubmatch(line)
		}
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		ids = append(ids, m[1])
	}
	return ids
}

// ExtractDeviations returns the numbered items of the "## 9." section. An item
// is resolved when its text, continuation lines included, says fixed or intended.
func ExtractDeviations(doc string) []Deviation {
	var items []Deviation
	inSection := false
	for line := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(line, "## ") {
			inSection = strings.HasPrefix(line, "## 9.")
			continue
		}
		if !inSection {
			continue
		}
		if m := itemRe.FindStringSubmatch(line); m != nil {
			var n int
			_, _ = fmt.Sscanf(m[1], "%d", &n)
			items = append(items, Deviation{N: n})
		}
		if len(items) > 0 && resolvedRe.MatchString(line) {
			items[len(items)-1].Resolved = true
		}
	}
	return items
}

func isIDByte(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}

// ScanCovered returns the ids mentioned, as whole tokens, by the *_test.go
// files and the testdata files under root.
func ScanCovered(fsys fs.FS, root string) (map[string]bool, error) {
	covered := map[string]bool{}
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isEvidence(p) {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		text := string(data)
		for _, loc := range mentionRe.FindAllStringIndex(text, -1) {
			if loc[0] > 0 && isIDByte(text[loc[0]-1]) {
				continue
			}
			if loc[1] < len(text) && (isIDByte(text[loc[1]]) || text[loc[1]] == '.' && loc[1]+1 < len(text) && text[loc[1]+1] >= '0' && text[loc[1]+1] <= '9') {
				continue
			}
			covered[text[loc[0]:loc[1]]] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", root, err)
	}
	return covered, nil
}

func isEvidence(p string) bool {
	if strings.HasSuffix(p, "_test.go") {
		return true
	}
	for _, part := range strings.Split(path.Dir(p), "/") {
		if part == "testdata" {
			return true
		}
	}
	return false
}

// ParseAllowlist reads lines "ID reason"; blank lines and # comments are skipped.
func ParseAllowlist(r io.Reader) (map[string]string, error) {
	allow := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, reason, _ := strings.Cut(line, " ")
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return nil, fmt.Errorf("allowlist line %d: %s has no reason", n, id)
		}
		if _, dup := allow[id]; dup {
			return nil, fmt.Errorf("allowlist line %d: %s listed twice", n, id)
		}
		allow[id] = reason
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}
	return allow, nil
}

// Report is the outcome of Check.
type Report struct {
	Total      int      // ids in the document
	Missing    []string // no test or fixture, not allowlisted
	Stale      []string // allowlisted but covered
	Unknown    []string // allowlisted but not in the document
	Deviations int      // items in section 9
	Unresolved []int    // section 9 items neither fixed nor intended
}

// Check compares the document with the coverage and the allowlist.
func Check(doc string, covered map[string]bool, allow map[string]string) Report {
	ids := ExtractIDs(doc)
	r := Report{Total: len(ids)}
	inDoc := map[string]bool{}
	for _, id := range ids {
		inDoc[id] = true
		_, allowed := allow[id]
		switch {
		case allowed && covered[id]:
			r.Stale = append(r.Stale, id)
		case !allowed && !covered[id]:
			r.Missing = append(r.Missing, id)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(allow)) {
		if !inDoc[id] {
			r.Unknown = append(r.Unknown, id)
		}
	}
	devs := ExtractDeviations(doc)
	r.Deviations = len(devs)
	for _, d := range devs {
		if !d.Resolved {
			r.Unresolved = append(r.Unresolved, d.N)
		}
	}
	return r
}

// OK reports whether nothing needs attention.
func (r Report) OK() bool {
	return len(r.Missing)+len(r.Stale)+len(r.Unknown)+len(r.Unresolved) == 0
}

// Write prints the report: one line per kind of problem, ids in document order.
func (r Report) Write(w io.Writer) {
	if r.OK() {
		_, _ = fmt.Fprintf(w, "pacing check: ok, %d ids covered, %d deviations resolved\n", r.Total, r.Deviations)
		return
	}
	_, _ = fmt.Fprintln(w, "pacing check: FAILED")
	if len(r.Missing) > 0 {
		_, _ = fmt.Fprintf(w, "missing a test (%d of %d): %s\n", len(r.Missing), r.Total, strings.Join(r.Missing, " "))
	}
	if len(r.Stale) > 0 {
		_, _ = fmt.Fprintf(w, "allowlisted but covered, remove from the allowlist (%d): %s\n", len(r.Stale), strings.Join(r.Stale, " "))
	}
	if len(r.Unknown) > 0 {
		_, _ = fmt.Fprintf(w, "allowlisted but not in PACING.md (%d): %s\n", len(r.Unknown), strings.Join(r.Unknown, " "))
	}
	if len(r.Unresolved) > 0 {
		nums := make([]string, len(r.Unresolved))
		for i, n := range r.Unresolved {
			nums[i] = fmt.Sprint(n)
		}
		_, _ = fmt.Fprintf(w, "section 9 items neither fixed nor intended (%d of %d): %s\n", len(r.Unresolved), r.Deviations, strings.Join(nums, " "))
	}
}
