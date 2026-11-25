package service_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/store"
)

func strPtr(s string) *string { return &s }

// fixtureState is the store state the settings fixture describes, with a
// history and a last good read the document must not show.
func fixtureState(t *testing.T) (store.Settings, []store.Account) {
	t.Helper()
	every := [7]float64{1, 1, 1, 1, 1, 1, 1}
	return store.Settings{RefreshIntervalMinutes: 15, RetentionDays: 90, NotificationThresholdPercent: 80},
		[]store.Account{
			{
				ID: "claude", Provider: "claude", Label: "Work", Enabled: true,
				Schedule:      store.Schedule{Enabled: true, Days: [7]float64{0, 1, 1, 1, 1, 0.5, 0}, HoursPerDay: 8},
				BinaryPath:    strPtr("/opt/claude/bin/claude"),
				LocalInsights: true,
				History:       map[string]history.State{"five-hour": {}},
				LastGood:      lastGood(t, "usage-ok"),
			},
			{
				ID: "copilot-1a2b3c4d", Provider: "copilot", Label: "Company", RenewalDay: 15,
				Schedule: store.Schedule{Days: every, HoursPerDay: 7.5}, Host: "acme.ghe.com", Scope: "organization",
				AuthMethod: "oauth",
			},
			{
				ID: "copilot", Provider: "copilot", Enabled: true, RenewalDay: 1,
				Schedule: store.Schedule{Days: every, HoursPerDay: 8}, Scope: "personal", ManualQuota: 300,
				AuthMethod: "pat",
			},
			{ID: "codex", Provider: "codex", Label: "Codex", Enabled: true, Schedule: store.Schedule{Days: every, HoursPerDay: 8}},
		}
}

func newSettingsAPI(t *testing.T, settings store.Settings, accounts ...store.Account) *apiFixture {
	t.Helper()
	api := newAPI(t, accounts...)
	if err := api.st.Update(func(s *store.State) error { s.Settings = settings; return nil }); err != nil {
		t.Fatal(err)
	}
	return api
}

// GET /api/v1/settings is the settings and the accounts' settings, never the
// history, the last good read or a secret (fixture contract/v1/service/settings.json).
func TestGetSettingsServesTheFixture(t *testing.T) {
	t.Parallel()
	settings, accounts := fixtureState(t)
	api := newSettingsAPI(t, settings, accounts...)
	w := api.do(t, http.MethodGet, "/api/v1/settings", "")
	if want := string(fixture(t, "service/settings")); w.Code != http.StatusOK || w.Body.String() != want {
		t.Errorf("status %d body\n%s\nwant\n%s", w.Code, w.Body, want)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// PUT /api/v1/settings replaces the settings and the account list, keeps each
// remaining account's history and last good read, and answers the new document.
func TestPutSettingsReplacesAndKeepsHistory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's binaryPath is a Unix absolute path")
	}
	settings, accounts := fixtureState(t)
	old := accounts[0]
	old.Label, old.BinaryPath = "Old", nil
	gone := store.Account{ID: "gone", Provider: "codex", Enabled: true, Schedule: store.Schedule{HoursPerDay: 8}}
	api := newSettingsAPI(t, store.Empty().Settings, old, gone)
	w := api.do(t, http.MethodPut, "/api/v1/settings", string(fixture(t, "service/settings")))
	if want := string(fixture(t, "service/settings")); w.Code != http.StatusOK || w.Body.String() != want {
		t.Fatalf("status %d body\n%s\nwant\n%s", w.Code, w.Body, want)
	}
	snap := api.st.Snapshot()
	if snap.Settings != settings {
		t.Errorf("settings = %+v; want %+v", snap.Settings, settings)
	}
	if len(snap.Accounts) != len(accounts) || snap.Account("gone") != nil {
		t.Fatalf("accounts = %+v", snap.Accounts)
	}
	kept := snap.Account("claude")
	if kept.Label != "Work" || kept.LastGood == nil || !reflect.DeepEqual(kept.History, old.History) {
		t.Errorf("claude = %+v; want the new label with its history and last good read", kept)
	}
	if added := snap.Account("codex"); added.LastGood != nil || len(added.History) != 0 {
		t.Errorf("an added account came with data: %+v", added)
	}
}

// The loop is told about added, removed, enabled, disabled accounts and those
// whose reads change; not about a label, schedule, renewal day, local insights
// or Copilot authentication method (#284, #336).
func TestPutSettingsTellsTheLoopWhichAccountsChanged(t *testing.T) {
	t.Parallel()
	schedule := store.Schedule{Days: [7]float64{1, 1, 1, 1, 1, 1, 1}, HoursPerDay: 8}
	acc := func(id, provider string, enabled bool) store.Account {
		return store.Account{ID: id, Provider: provider, Label: id, Enabled: enabled, Schedule: schedule}
	}
	cop := acc("cop", "copilot", true)
	cop.Host = "acme.ghe.com"
	api := newSettingsAPI(t, store.Empty().Settings,
		cop, acc("label", "claude", true), acc("off", "claude", false), acc("bin", "codex", true),
		acc("gone", "codex", true), acc("same", "codex", true), acc("disable", "codex", true))
	bin := filepath.Join(t.TempDir(), "codex")
	body := `{"schemaVersion":1,"settings":{"refreshIntervalMinutes":60,"retentionDays":30,"notificationThresholdPercent":90},"accounts":[` +
		`{"id":"cop","provider":"copilot","label":"cop","enabled":true,"renewalDay":0,"schedule":{"enabled":false,"days":[1,1,1,1,1,1,1],"hoursPerDay":8},"host":"https://API.Acme.ghe.com/","authMethod":"oauth"},` +
		`{"id":"label","provider":"claude","label":"New","enabled":true,"renewalDay":12,"schedule":{"enabled":true,"days":[0,1,1,1,1,1,0],"hoursPerDay":6},"localInsights":true},` +
		`{"id":"off","provider":"claude","label":"off","enabled":true,"renewalDay":0,"schedule":{"enabled":false,"days":[1,1,1,1,1,1,1],"hoursPerDay":8}},` +
		`{"id":"bin","provider":"codex","label":"bin","enabled":true,"renewalDay":0,"schedule":{"enabled":false,"days":[1,1,1,1,1,1,1],"hoursPerDay":8},"binaryPath":` + jsonString(bin) + `},` +
		`{"id":"same","provider":"codex","label":"same","enabled":true,"renewalDay":0,"schedule":{"enabled":false,"days":[1,1,1,1,1,1,1],"hoursPerDay":8}},` +
		`{"id":"disable","provider":"codex","label":"disable","enabled":false,"renewalDay":0,"schedule":{"enabled":false,"days":[1,1,1,1,1,1,1],"hoursPerDay":8}},` +
		`{"id":"new","provider":"codex","label":"new","enabled":true,"renewalDay":0,"schedule":{"enabled":false,"days":[1,1,1,1,1,1,1],"hoursPerDay":8}}]}`
	w := api.do(t, http.MethodPut, "/api/v1/settings", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
	want := [][]string{{"bin", "disable", "gone", "new", "off"}}
	if !reflect.DeepEqual(api.loop.changed, want) {
		t.Errorf("Changed = %v; want %v", api.loop.changed, want)
	}
	if got := api.st.Snapshot().Account("cop").Host; got != "acme.ghe.com" {
		t.Errorf("host stored as %q; want the normalized acme.ghe.com", got)
	}
	if snap := api.st.Snapshot(); snap.Account("cop").AuthMethod != "oauth" || !snap.Account("label").LocalInsights {
		t.Errorf("authMethod %q, localInsights %v; want oauth and true stored", snap.Account("cop").AuthMethod, snap.Account("label").LocalInsights)
	}
	// The same document again changes nothing: the loop is not told.
	api.loop.changed = nil
	if w := api.do(t, http.MethodPut, "/api/v1/settings", body); w.Code != http.StatusOK || api.loop.changed != nil {
		t.Errorf("status %d, Changed = %v; want 200 and no call", w.Code, api.loop.changed)
	}
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

// validAccount is one valid account object of a settings document.
func validAccount() map[string]any {
	return map[string]any{
		"id": "a", "provider": "copilot", "label": "A", "enabled": true, "renewalDay": 0,
		"schedule": map[string]any{"enabled": false, "days": []any{1, 1, 1, 1, 1, 1, 1}, "hoursPerDay": 8},
	}
}

func validDocument() map[string]any {
	return map[string]any{
		"schemaVersion": 1,
		"settings":      map[string]any{"refreshIntervalMinutes": 30, "retentionDays": 90, "notificationThresholdPercent": 80},
		"accounts":      []any{validAccount(), map[string]any{"id": "c", "provider": "claude", "label": "C", "enabled": true, "renewalDay": 0, "schedule": map[string]any{"enabled": false, "days": []any{1, 1, 1, 1, 1, 1, 1}, "hoursPerDay": 8}}},
	}
}

func settingsOf(doc map[string]any) map[string]any {
	return doc["settings"].(map[string]any) //nolint:forcetypeassert // built above
}

func accountOf(doc map[string]any, i int) map[string]any {
	return doc["accounts"].([]any)[i].(map[string]any) //nolint:forcetypeassert // built above
}

// Invalid input is a 400 usage error with reason invalid_settings, whose
// message starts with the field; nothing is written and the loop is not told.
func TestPutSettingsRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		body   string                   // used when set
		change func(doc map[string]any) // applied to a valid document
		field  string                   // the message's prefix
	}{
		{name: "not JSON", body: "{", field: "body"},
		{name: "two objects", body: "{} {}", field: "body"},
		{name: "empty body", body: " ", field: "body"},
		{name: "newer schema", change: func(d map[string]any) { d["schemaVersion"] = 2 }, field: "schemaVersion"},
		{name: "no settings", change: func(d map[string]any) { delete(d, "settings") }, field: "settings"},
		{name: "no accounts", change: func(d map[string]any) { delete(d, "accounts") }, field: "accounts"},
		{name: "interval too short", change: func(d map[string]any) { settingsOf(d)["refreshIntervalMinutes"] = 4 }, field: "settings.refreshIntervalMinutes"},
		{name: "interval too long", change: func(d map[string]any) { settingsOf(d)["refreshIntervalMinutes"] = 1441 }, field: "settings.refreshIntervalMinutes"},
		{name: "interval of the wrong type", change: func(d map[string]any) { settingsOf(d)["refreshIntervalMinutes"] = "30" }, field: "settings.refreshIntervalMinutes"},
		{name: "interval not an integer", change: func(d map[string]any) { settingsOf(d)["refreshIntervalMinutes"] = 7.5 }, field: "settings.refreshIntervalMinutes"},
		{name: "no retention", change: func(d map[string]any) { settingsOf(d)["retentionDays"] = 0 }, field: "settings.retentionDays"},
		{name: "threshold 0", change: func(d map[string]any) { settingsOf(d)["notificationThresholdPercent"] = 0 }, field: "settings.notificationThresholdPercent"},
		{name: "threshold over 100", change: func(d map[string]any) { settingsOf(d)["notificationThresholdPercent"] = 101 }, field: "settings.notificationThresholdPercent"},
		{name: "empty id", change: func(d map[string]any) { accountOf(d, 0)["id"] = "" }, field: "accounts[0].id"},
		{name: "id with a slash", change: func(d map[string]any) { accountOf(d, 0)["id"] = "a/b" }, field: "accounts[0].id"},
		{name: "duplicate id", change: func(d map[string]any) { accountOf(d, 1)["id"] = "a"; accountOf(d, 1)["provider"] = "copilot" }, field: "accounts[1].id"},
		{name: "unknown provider", change: func(d map[string]any) { accountOf(d, 0)["provider"] = "nope" }, field: "accounts[0].provider"},
		{name: "provider of an existing account", change: func(d map[string]any) { accountOf(d, 0)["id"] = "existing" }, field: "accounts[0].provider"},
		{name: "renewal day 32", change: func(d map[string]any) { accountOf(d, 1)["renewalDay"] = 32 }, field: "accounts[1].renewalDay"},
		{name: "negative renewal day", change: func(d map[string]any) { accountOf(d, 0)["renewalDay"] = -1 }, field: "accounts[0].renewalDay"},
		{name: "six days", change: func(d map[string]any) {
			accountOf(d, 0)["schedule"].(map[string]any)["days"] = []any{1, 1, 1, 1, 1, 1} //nolint:forcetypeassert // built above
		}, field: "accounts[0].schedule.days"},
		{name: "day weight 0.3", change: func(d map[string]any) {
			accountOf(d, 0)["schedule"].(map[string]any)["days"] = []any{1, 1, 0.3, 1, 1, 1, 1} //nolint:forcetypeassert // built above
		}, field: "accounts[0].schedule.days"},
		{name: "no hours", change: func(d map[string]any) {
			accountOf(d, 0)["schedule"].(map[string]any)["hoursPerDay"] = 0 //nolint:forcetypeassert // built above
		}, field: "accounts[0].schedule.hoursPerDay"},
		{name: "25 hours", change: func(d map[string]any) {
			accountOf(d, 0)["schedule"].(map[string]any)["hoursPerDay"] = 25 //nolint:forcetypeassert // built above
		}, field: "accounts[0].schedule.hoursPerDay"},
		{name: "foreign host", change: func(d map[string]any) { accountOf(d, 0)["host"] = "evil.example.com" }, field: "accounts[0].host"},
		{name: "host on claude", change: func(d map[string]any) { accountOf(d, 1)["host"] = "github.com" }, field: "accounts[1].host"},
		{name: "unknown scope", change: func(d map[string]any) { accountOf(d, 0)["scope"] = "team" }, field: "accounts[0].scope"},
		{name: "negative quota", change: func(d map[string]any) { accountOf(d, 0)["manualQuota"] = -1 }, field: "accounts[0].manualQuota"},
		{name: "relative binary", change: func(d map[string]any) { accountOf(d, 1)["binaryPath"] = "bin/claude" }, field: "accounts[1].binaryPath"},
		{name: "local insights on copilot", change: func(d map[string]any) { accountOf(d, 0)["localInsights"] = true }, field: "accounts[0].localInsights"},
		{name: "local insights of the wrong type", change: func(d map[string]any) { accountOf(d, 1)["localInsights"] = "yes" }, field: "accounts.1.localInsights"},
		{name: "local insights on two claude accounts", change: func(d map[string]any) {
			second := validAccount()
			second["id"], second["provider"], second["localInsights"] = "c2", "claude", true
			accountOf(d, 1)["localInsights"] = true
			d["accounts"] = append(d["accounts"].([]any), second) //nolint:forcetypeassert // built above
		}, field: "accounts[2].localInsights"},
		{name: "unknown auth method", change: func(d map[string]any) { accountOf(d, 0)["authMethod"] = "password" }, field: "accounts[0].authMethod"},
		{name: "auth method on claude", change: func(d map[string]any) { accountOf(d, 1)["authMethod"] = "pat" }, field: "accounts[1].authMethod"},
		{name: "binary on copilot", change: func(d map[string]any) { accountOf(d, 0)["binaryPath"] = filepath.Join(t.TempDir(), "x") }, field: "accounts[0].binaryPath"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			existing := store.Account{ID: "existing", Provider: "claude", Enabled: true, Schedule: store.Schedule{HoursPerDay: 8}}
			api := newSettingsAPI(t, store.Empty().Settings, existing)
			before := api.st.Snapshot()
			body := c.body
			if c.change != nil {
				doc := validDocument()
				c.change(doc)
				data, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				body = string(data)
			}
			w := api.do(t, http.MethodPut, "/api/v1/settings", body)
			var got struct {
				SchemaVersion int `json:"schemaVersion"`
				Error         struct {
					Code, Message, Reason string
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusBadRequest {
				t.Fatalf("status %d body %s", w.Code, w.Body)
			}
			if got.SchemaVersion != 1 || got.Error.Code != "usage" || got.Error.Reason != "invalid_settings" ||
				!strings.HasPrefix(got.Error.Message, c.field+": ") {
				t.Errorf("body %s; want a usage error, reason invalid_settings, message on %s", w.Body, c.field)
			}
			if !reflect.DeepEqual(api.st.Snapshot(), before) || api.loop.changed != nil {
				t.Error("an invalid document changed the store or told the loop")
			}
		})
	}
}

// The error document of invalid settings is stable
// (fixture contract/v1/service/error-invalid-settings.json).
func TestInvalidSettingsFixture(t *testing.T) {
	t.Parallel()
	api := newSettingsAPI(t, store.Empty().Settings)
	doc := validDocument()
	accountOf(doc, 1)["renewalDay"] = 40
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	w := api.do(t, http.MethodPut, "/api/v1/settings", string(data))
	if want := string(fixture(t, "service/error-invalid-settings")); w.Code != http.StatusBadRequest || w.Body.String() != want {
		t.Errorf("status %d body %s; want %s", w.Code, w.Body, want)
	}
}

// A secret sent along (a token field a client should not send) is neither
// stored nor echoed: the settings document has no field for it (#179).
func TestSettingsCarryNoSecret(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	api := &apiFixture{st: st, loop: &fakeLoop{}}
	doc := validDocument()
	doc["token"] = "ghp_topsecret"
	accountOf(doc, 0)["token"] = "ghp_topsecret"
	accountOf(doc, 0)["clientSecret"] = "ghp_topsecret"
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	w := api.do(t, http.MethodPut, "/api/v1/settings", string(data))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
	file, err := os.ReadFile(filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"answer": w.Body.String(), "store.json": string(file)} {
		if strings.Contains(out, "ghp_topsecret") {
			t.Errorf("the secret reached the %s", name)
		}
	}
}

func TestSettingsMethods(t *testing.T) {
	t.Parallel()
	api := newSettingsAPI(t, store.Empty().Settings)
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		if w := api.do(t, method, "/api/v1/settings", "{}"); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d; want 405", method, w.Code)
		}
	}
}

// A body over the limit is refused without being read whole.
func TestPutSettingsBodyLimit(t *testing.T) {
	t.Parallel()
	api := newSettingsAPI(t, store.Empty().Settings)
	body := `{"schemaVersion":1,"x":"` + strings.Repeat("a", 300<<10) + `"}`
	w := api.do(t, http.MethodPut, "/api/v1/settings", body)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), `"reason":"invalid_settings"`) {
		t.Errorf("status %d body %.200s; want 413 with an invalid_settings error", w.Code, w.Body)
	}
}
