package usage_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

func contractFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contract", "v1", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Parsing a document and writing it back gives the same bytes: the reader
// understands every field the writer produces.
func TestParseDocumentRoundTripsTheSuccessFixtures(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"usage-ok", "usage-claude", "usage-stale", "usage-count-window"} {
		data := contractFixture(t, name)
		doc, err := usage.ParseDocument(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if doc.Err != nil {
			t.Fatalf("%s: unexpected error document %+v", name, doc.Err)
		}
		var out bytes.Buffer
		if err := usage.WriteSuccess(&out, doc.Provider, doc.Usage, doc.FetchedAt); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), data) {
			t.Errorf("%s round trip:\n got %s\nwant %s", name, out.Bytes(), data)
		}
	}
}

func TestParseDocumentRoundTripsTheErrorFixtures(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"error-timeout", "error-signed-out", "error-not-ready", "error-unavailable", "error-unavailable-reason", "error-format-drift"} {
		data := contractFixture(t, name)
		doc, err := usage.ParseDocument(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if doc.Err == nil {
			t.Fatalf("%s: no error parsed", name)
		}
		var out bytes.Buffer
		if err := usage.WriteError(&out, doc.Provider, doc.Err); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), data) {
			t.Errorf("%s round trip:\n got %s\nwant %s", name, out.Bytes(), data)
		}
	}
}

func TestParseDocumentReadsTheFields(t *testing.T) {
	t.Parallel()
	doc, err := usage.ParseDocument(contractFixture(t, "usage-stale"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Usage.Stale == nil || doc.Usage.Stale.Code != usage.CodeUnavailable {
		t.Errorf("stale = %+v", doc.Usage.Stale)
	}
	w := doc.Usage.QuotaWindows[1]
	if w.ID != "weekly" || w.Used != 42 || w.ResetsAt == nil || !w.ResetsAt.Equal(time.Date(2026, 11, 7, 21, 10, 0, 0, time.UTC)) {
		t.Errorf("window = %+v", w)
	}
}

func TestParseDocumentRejectsBadOutput(t *testing.T) {
	t.Parallel()
	for name, data := range map[string]string{
		"not json":         "claude: command not found",
		"two documents":    `{"schemaVersion":1,"provider":"x","error":{"code":"timeout","message":"m"}}` + "\n" + `{}`,
		"newer schema":     `{"schemaVersion":2,"provider":"x","fetchedAt":"2026-10-31T21:10:00Z","stale":false,"usage":{"planTier":null,"subscriptionRenewsAt":null,"quotaWindows":[]}}`,
		"no fetchedAt":     `{"schemaVersion":1,"provider":"x","stale":false,"usage":{"planTier":null,"subscriptionRenewsAt":null,"quotaWindows":[]}}`,
		"no usage":         `{"schemaVersion":1,"provider":"x","fetchedAt":"2026-10-31T21:10:00Z","stale":false}`,
		"stale, no reason": `{"schemaVersion":1,"provider":"x","fetchedAt":"2026-10-31T21:10:00Z","stale":true,"usage":{"planTier":null,"subscriptionRenewsAt":null,"quotaWindows":[]}}`,
		"error, no code":   `{"schemaVersion":1,"provider":"x","error":{"message":"m"}}`,
		"bad reset":        `{"schemaVersion":1,"provider":"x","fetchedAt":"2026-10-31T21:10:00Z","stale":false,"usage":{"planTier":null,"subscriptionRenewsAt":null,"quotaWindows":[{"id":"w","label":"w","periodType":"rolling-hours","periodLength":5,"unit":"percentage","used":1,"total":100,"resetsAt":"soon"}]}}`,
	} {
		_, err := usage.ParseDocument([]byte(data))
		if !errors.Is(err, usage.ErrBadDocument) {
			t.Errorf("%s: err = %v; want ErrBadDocument", name, err)
		}
	}
}
