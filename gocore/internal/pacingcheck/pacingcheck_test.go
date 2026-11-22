package pacingcheck_test

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/suppressio/aipermiler/gocore/internal/pacingcheck"
)

const sampleDoc = `# PACING

## 3. Quantities

- **D-3.1 Used** x.
**D-3.10** Autonomy.

| # | Input | Expected |
|---|---|---|
| A-1 | baseline | none |
| **G-12** | near reset | 0.57 |
| E3b | over the limit | 103 |

## 6. Invariants

- **I-1** The target never exceeds the ceiling.

## 9. Known deviations

1. **Target = ceiling**: wrong.
2. **Reset passed** answers 0.
   Intended: the gauge reads as over.
3. ~~**Overflow**~~ — fixed (#111).
4. **Mixed file**, continued
   on a second line.

## 10. Decisions

- **D1** Not an id of the checked kind.
`

func TestExtractIDs(t *testing.T) {
	t.Parallel()
	got := pacingcheck.ExtractIDs(sampleDoc)
	want := []string{"D-3.1", "D-3.10", "A-1", "G-12", "E3b", "I-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestExtractIDsIgnoresMentionsInProse(t *testing.T) {
	t.Parallel()
	got := pacingcheck.ExtractIDs("| A-1 | uses B-2 and G-7 | x |\nsee A-1 and I-4\n")
	if want := []string{"A-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestExtractDeviations(t *testing.T) {
	t.Parallel()
	got := pacingcheck.ExtractDeviations(sampleDoc)
	want := []pacingcheck.Deviation{{N: 1}, {N: 2, Resolved: true}, {N: 3, Resolved: true}, {N: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deviations = %+v, want %+v", got, want)
	}
}

func TestScanCoveredMatchesWholeIDsInTestsAndFixtures(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"pace/a_test.go":              {Data: []byte("// D-3.1 and E3b; G-12: x\n// A-10 only")},
		"pace/testdata/cases.json":    {Data: []byte(`{"name":"I-1"}`)},
		"pace/impl.go":                {Data: []byte("// A-1 mentioned in production code")},
		"pace/testdata/notes.md":      {Data: []byte("D-3.10")},
		"pace/prefixed_test.go":       {Data: []byte("XA-1 and 3E3 and D-3.10x")},
		"pace/testdata/ranged.golden": {Data: []byte("D-3.1.")},
	}
	got, err := pacingcheck.ScanCovered(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"D-3.1", "E3b", "G-12", "I-1", "A-10", "D-3.10"} {
		if !got[id] {
			t.Errorf("%s not covered", id)
		}
	}
	for _, id := range []string{"A-1", "E3"} {
		if got[id] {
			t.Errorf("%s wrongly covered", id)
		}
	}
}

func TestParseAllowlist(t *testing.T) {
	t.Parallel()
	got, err := pacingcheck.ParseAllowlist(strings.NewReader("# comment\n\nG-12  needs a clock fixture\nI-1 reason\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"G-12": "needs a clock fixture", "I-1": "reason"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("allowlist = %v, want %v", got, want)
	}
}

func TestParseAllowlistRejectsMissingReasonAndDuplicates(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"no reason": "G-12\n",
		"duplicate": "G-12 a\nG-12 b\n",
	} {
		if _, err := pacingcheck.ParseAllowlist(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{"D-3.1": true, "A-1": true, "I-1": true}
	allow := map[string]string{"G-12": "reason", "A-1": "stale: covered", "Z-9": "not in the doc"}
	r := pacingcheck.Check(sampleDoc, covered, allow)

	if want := []string{"D-3.10", "E3b"}; !reflect.DeepEqual(r.Missing, want) {
		t.Errorf("Missing = %v, want %v", r.Missing, want)
	}
	if want := []string{"A-1"}; !reflect.DeepEqual(r.Stale, want) {
		t.Errorf("Stale = %v, want %v", r.Stale, want)
	}
	if want := []string{"Z-9"}; !reflect.DeepEqual(r.Unknown, want) {
		t.Errorf("Unknown = %v, want %v", r.Unknown, want)
	}
	if want := []int{1, 4}; !reflect.DeepEqual(r.Unresolved, want) {
		t.Errorf("Unresolved = %v, want %v", r.Unresolved, want)
	}
	if r.OK() {
		t.Error("OK() = true, want false")
	}
}

func TestCheckOK(t *testing.T) {
	t.Parallel()
	doc := "| A-1 | x | y |\n\n## 9. Known deviations\n\n1. ~~done~~ fixed\n"
	r := pacingcheck.Check(doc, map[string]bool{"A-1": true}, nil)
	if !r.OK() {
		t.Fatalf("report not OK: %+v", r)
	}
	var b strings.Builder
	r.Write(&b)
	if got, want := b.String(), "pacing check: ok, 1 ids covered, 1 deviations resolved\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestReportWriteIsStableAndShort(t *testing.T) {
	t.Parallel()
	r := pacingcheck.Report{
		Total:      10,
		Missing:    []string{"A-1", "A-2"},
		Stale:      []string{"B-1"},
		Unknown:    []string{"Z-9"},
		Unresolved: []int{3, 5},
		Deviations: 6,
	}
	var b strings.Builder
	r.Write(&b)
	want := "pacing check: FAILED\n" +
		"missing a test (2 of 10): A-1 A-2\n" +
		"allowlisted but covered, remove from the allowlist (1): B-1\n" +
		"allowlisted but not in PACING.md (1): Z-9\n" +
		"section 9 items neither fixed nor intended (2 of 6): 3 5\n"
	if got := b.String(); got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
