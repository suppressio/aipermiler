package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/service"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const (
	connectedDoc = `{"schemaVersion":1,"provider":"copilot","username":"octo"}` + "\n"
	deletedDoc   = `{"schemaVersion":1,"provider":"copilot","deleted":true}` + "\n"
	authorizeDoc = `{"schemaVersion":1,"provider":"copilot","event":"authorize","url":"https://github.com/login/oauth/authorize?client_id=Iv1.synthetic&state=synthetic"}` + "\n"
)

// fakeCommands stands for refresh.CLIFetcher's account commands.
type fakeCommands struct {
	mu       sync.Mutex
	tokens   []string // every token SetToken got
	deleted  []string // the account of every DeleteToken
	setDoc   []byte
	setCode  usage.Code
	deleteFn func(account store.Account) ([]byte, usage.Code, error)
	login    func(ctx context.Context, account store.Account, req refresh.LoginRequest, authorize func([]byte)) ([]byte, usage.Code, error)
}

func (f *fakeCommands) SetToken(_ context.Context, _ store.Account, token string) ([]byte, usage.Code, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, token)
	if f.setDoc == nil {
		return []byte(connectedDoc), "", nil
	}
	return f.setDoc, f.setCode, nil
}

func (f *fakeCommands) DeleteToken(_ context.Context, account store.Account) ([]byte, usage.Code, error) {
	f.mu.Lock()
	f.deleted = append(f.deleted, account.ID)
	fn := f.deleteFn
	f.mu.Unlock()
	if fn != nil {
		return fn(account)
	}
	return []byte(deletedDoc), "", nil
}

func (f *fakeCommands) Login(ctx context.Context, account store.Account, req refresh.LoginRequest, authorize func([]byte)) ([]byte, usage.Code, error) {
	return f.login(ctx, account, req, authorize)
}

// accountAPI is one handler kept across requests (the login lives in it).
type accountAPI struct {
	*apiFixture
	commands *fakeCommands
	h        http.Handler
	logs     *bytes.Buffer
}

func newAccountAPI(t *testing.T, accounts ...store.Account) *accountAPI {
	t.Helper()
	api := newAPI(t, accounts...)
	commands := &fakeCommands{}
	logs := &bytes.Buffer{}
	return &accountAPI{apiFixture: api, commands: commands, logs: logs, h: service.NewHandler(service.HandlerConfig{
		Token: testToken, Host: testHost, Version: "1.2.3", ContractVersion: 1,
		Store: api.st, Loop: api.loop, Accounts: commands, Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})}
}

func (a *accountAPI) call(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://"+testHost+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	return w
}

func (a *accountAPI) changed() [][]string {
	a.loop.mu.Lock()
	defer a.loop.mu.Unlock()
	return append([][]string(nil), a.loop.changed...)
}

func copilotAccount(id string) store.Account {
	return store.Account{ID: id, Provider: "copilot", Label: id, Enabled: true, Host: "acme.ghe.com"}
}

// errorOf decodes an error document's code and reason.
func errorOf(t *testing.T, body []byte) (usage.Code, string) {
	t.Helper()
	var doc struct {
		Error struct {
			Code   usage.Code `json:"code"`
			Reason string     `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("not a document: %s", body)
	}
	return doc.Error.Code, doc.Error.Reason
}

// PUT .../token hands the token to the CLI and answers its document; a
// connected account is read again at once (Loop.Changed, a forced read).
func TestSetTokenAnswersTheCLIDocument(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("g1"), store.Account{ID: "off", Provider: "copilot"})
	w := api.call(t, http.MethodPut, "/api/v1/accounts/g1/token", `{"token":"ghp_synthetic"}`)
	if w.Code != http.StatusOK || w.Body.String() != connectedDoc {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
	if !reflect.DeepEqual(api.commands.tokens, []string{"ghp_synthetic"}) {
		t.Errorf("tokens = %q", api.commands.tokens)
	}
	if got := api.changed(); !reflect.DeepEqual(got, [][]string{{"g1"}}) {
		t.Errorf("Changed = %v", got)
	}
	// A disabled account can be connected too: it is read once enabled.
	if w := api.call(t, http.MethodPut, "/api/v1/accounts/off/token", `{"token":"ghp_other"}`); w.Code != http.StatusOK {
		t.Errorf("disabled account: status %d", w.Code)
	}
}

func TestSetTokenRejectedByGitHub(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("g1"))
	api.commands.setDoc = []byte(`{"schemaVersion":1,"provider":"copilot","error":{"code":"signed_out","message":"GitHub rejected the token","reason":"token_rejected"}}` + "\n")
	api.commands.setCode = usage.CodeSignedOut
	w := api.call(t, http.MethodPut, "/api/v1/accounts/g1/token", `{"token":"ghp_bad"}`)
	if w.Code != http.StatusBadGateway || !bytes.Equal(w.Body.Bytes(), api.commands.setDoc) {
		t.Errorf("status %d body %s", w.Code, w.Body)
	}
	if got := api.changed(); got != nil {
		t.Errorf("Changed = %v; a rejected token changes nothing", got)
	}
}

func TestSetTokenRejectsABadRequest(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("g1"), store.Account{ID: "c", Provider: "claude", Enabled: true})
	cases := []struct {
		path, body string
		status     int
	}{
		{"/api/v1/accounts/nope/token", `{"token":"x"}`, http.StatusNotFound},
		{"/api/v1/accounts/c/token", `{"token":"x"}`, http.StatusBadRequest},
		{"/api/v1/accounts/g1/token", `{}`, http.StatusBadRequest},
		{"/api/v1/accounts/g1/token", `{"token":""}`, http.StatusBadRequest},
		{"/api/v1/accounts/g1/token", `{"token":1}`, http.StatusBadRequest},
		{"/api/v1/accounts/g1/token", `{"token":"x"} {}`, http.StatusBadRequest},
		{"/api/v1/accounts/g1/token", `{"token":"` + strings.Repeat("x", 65<<10) + `"}`, http.StatusBadRequest},
		{"/api/v1/accounts/g1/token", `{"token":"` + strings.Repeat("x", 200<<10) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		w := api.call(t, http.MethodPut, c.path, c.body)
		if code, _ := errorOf(t, w.Body.Bytes()); w.Code != c.status || code != usage.CodeUsage {
			t.Errorf("%s %.40s: status %d, code %q; want %d usage", c.path, c.body, w.Code, code, c.status)
		}
		if strings.Contains(w.Body.String(), "xxxx") {
			t.Errorf("%s: the answer quotes the body", c.path)
		}
	}
	if len(api.commands.tokens) != 0 {
		t.Errorf("the CLI ran for a bad request: %d tokens", len(api.commands.tokens))
	}
}

func TestDeleteToken(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("g1"))
	w := api.call(t, http.MethodDelete, "/api/v1/accounts/g1/token", "")
	if w.Code != http.StatusOK || w.Body.String() != deletedDoc {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
	if !reflect.DeepEqual(api.commands.deleted, []string{"g1"}) {
		t.Errorf("deleted = %v", api.commands.deleted)
	}
	if got := api.changed(); !reflect.DeepEqual(got, [][]string{{"g1"}}) {
		t.Errorf("Changed = %v; a disconnected account is read again (signed out)", got)
	}
}

// Whatever the child prints, a document carrying the secret never leaves
// the service.
func TestAnswerCarryingTheSecretIsWithheld(t *testing.T) {
	t.Parallel()
	const token = `ghp_secret"\value`
	escaped, _ := json.Marshal(token)
	api := newAccountAPI(t, copilotAccount("g1"))
	api.commands.setDoc = []byte(`{"schemaVersion":1,"provider":"copilot","error":{"code":"unavailable","message":"bad ` + string(escaped[1:len(escaped)-1]) + `"}}` + "\n")
	api.commands.setCode = usage.CodeUnavailable
	body, _ := json.Marshal(map[string]string{"token": token})
	w := api.call(t, http.MethodPut, "/api/v1/accounts/g1/token", string(body))
	if code, _ := errorOf(t, w.Body.Bytes()); w.Code != http.StatusBadGateway || code != usage.CodeUnavailable {
		t.Errorf("status %d body %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "ghp_secret") {
		t.Errorf("the secret is in the answer: %s", w.Body)
	}
}

func TestAccountActionsWithoutCommands(t *testing.T) {
	t.Parallel()
	api := newAPI(t, copilotAccount("g1"))
	w := api.do(t, http.MethodPut, "/api/v1/accounts/g1/token", `{"token":"x"}`)
	if code, _ := errorOf(t, w.Body.Bytes()); w.Code != http.StatusServiceUnavailable || code != usage.CodeUnavailable {
		t.Errorf("status %d body %s", w.Code, w.Body)
	}
}

// Removing a Copilot account through the settings deletes its token.
func TestRemovingACopilotAccountDeletesItsToken(t *testing.T) {
	t.Parallel()
	every := store.Schedule{Days: [7]float64{1, 1, 1, 1, 1, 1, 1}, HoursPerDay: 8}
	keep, gone, other := copilotAccount("keep"), copilotAccount("gone"), store.Account{ID: "cl", Provider: "claude", Schedule: every}
	keep.Schedule, gone.Schedule = every, every
	api := newAccountAPI(t, keep, gone, other)
	if err := api.st.Update(func(s *store.State) error { s.Settings = store.Empty().Settings; return nil }); err != nil {
		t.Fatal(err)
	}
	body := `{"schemaVersion":1,"settings":{"refreshIntervalMinutes":30,"retentionDays":90,"notificationThresholdPercent":80},"accounts":[` +
		`{"id":"keep","provider":"copilot","label":"keep","enabled":true,"renewalDay":0,"schedule":{"enabled":false,"days":[1,1,1,1,1,1,1],"hoursPerDay":8},"host":"acme.ghe.com"}]}`
	if w := api.call(t, http.MethodPut, "/api/v1/settings", body); w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
	if !reflect.DeepEqual(api.commands.deleted, []string{"gone"}) {
		t.Errorf("deleted = %v; want only the removed Copilot account", api.commands.deleted)
	}
}

// A token that cannot be deleted does not undo the removal: it is logged.
func TestRemovalLogsATokenItCannotDelete(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("gone"))
	api.commands.deleteFn = func(store.Account) ([]byte, usage.Code, error) {
		return []byte(`{"schemaVersion":1,"provider":"copilot","error":{"code":"unavailable","message":"no keyring","reason":"keyring_unavailable"}}` + "\n"), usage.CodeUnavailable, nil
	}
	body := `{"schemaVersion":1,"settings":{"refreshIntervalMinutes":30,"retentionDays":90,"notificationThresholdPercent":80},"accounts":[]}`
	if w := api.call(t, http.MethodPut, "/api/v1/settings", body); w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
	if api.st.Snapshot().Account("gone") != nil {
		t.Error("the account was not removed")
	}
	if !strings.Contains(api.logs.String(), "gone") || !strings.Contains(api.logs.String(), "keyring_unavailable") {
		t.Errorf("log = %q", api.logs)
	}
}

// loginControl drives a fake login: it announces when told, then ends with
// the result it is given, or when cancelled.
type loginControl struct {
	announce chan struct{}
	result   chan []byte
	requests chan refresh.LoginRequest
}

func newLoginControl(api *accountAPI) *loginControl {
	c := &loginControl{announce: make(chan struct{}, 1), result: make(chan []byte, 1), requests: make(chan refresh.LoginRequest, 4)}
	api.commands.login = func(ctx context.Context, _ store.Account, req refresh.LoginRequest, authorize func([]byte)) ([]byte, usage.Code, error) {
		c.requests <- req
		select {
		case <-c.announce:
			authorize([]byte(authorizeDoc))
		case doc := <-c.result:
			return doc, usage.CodeUnavailable, nil
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
		select {
		case doc := <-c.result:
			var code usage.Code
			if bytes.Contains(doc, []byte(`"error"`)) {
				code = usage.CodeTimeout
			}
			return doc, code, nil
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	return c
}

const loginBody = `{"clientId":"Iv1.synthetic","clientSecret":"synthetic-client-secret","locale":"it"}`

// waitStatus polls GET .../login until it answers want.
func waitStatus(t *testing.T, api *accountAPI, path string, want int) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := api.call(t, http.MethodGet, path, "")
		if w.Code == want || time.Now().After(deadline) {
			return w
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The login answers the authorize URL at once; the status endpoint then says
// pending (202), and the connected username once the browser is back.
func TestLoginAnswersTheURLThenTheResult(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("g1"))
	c := newLoginControl(api)
	c.announce <- struct{}{}
	w := api.call(t, http.MethodPost, "/api/v1/accounts/g1/login", loginBody)
	if w.Code != http.StatusAccepted || w.Body.String() != authorizeDoc {
		t.Fatalf("status %d body %s", w.Code, w.Body)
	}
	if req := <-c.requests; req != (refresh.LoginRequest{ClientID: "Iv1.synthetic", ClientSecret: "synthetic-client-secret", Locale: "it"}) {
		t.Errorf("request = %+v", req)
	}
	if w := api.call(t, http.MethodGet, "/api/v1/accounts/g1/login", ""); w.Code != http.StatusAccepted || w.Body.String() != authorizeDoc {
		t.Errorf("pending: status %d body %s", w.Code, w.Body)
	}
	if got := api.changed(); got != nil {
		t.Errorf("Changed before the login ended: %v", got)
	}
	c.result <- []byte(connectedDoc)
	if w := waitStatus(t, api, "/api/v1/accounts/g1/login", http.StatusOK); w.Code != http.StatusOK || w.Body.String() != connectedDoc {
		t.Fatalf("connected: status %d body %s", w.Code, w.Body)
	}
	if got := api.changed(); !reflect.DeepEqual(got, [][]string{{"g1"}}) {
		t.Errorf("Changed = %v; want the connected account read at once", got)
	}
	// Another account has no login to report.
	if w := api.call(t, http.MethodGet, "/api/v1/accounts/g2/login", ""); w.Code != http.StatusNotFound {
		t.Errorf("other account: status %d", w.Code)
	}
}

// The browser never came back: the CLI's timeout document is the status.
func TestLoginTimeoutIsTheStatus(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("g1"))
	c := newLoginControl(api)
	c.announce <- struct{}{}
	if w := api.call(t, http.MethodPost, "/api/v1/accounts/g1/login", loginBody); w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	timeout := `{"schemaVersion":1,"provider":"copilot","error":{"code":"timeout","message":"GitHub login timed out: no authorization in time"}}` + "\n"
	c.result <- []byte(timeout)
	w := waitStatus(t, api, "/api/v1/accounts/g1/login", http.StatusBadGateway)
	if w.Code != http.StatusBadGateway || w.Body.String() != timeout {
		t.Errorf("status %d body %s", w.Code, w.Body)
	}
	if got := api.changed(); got != nil {
		t.Errorf("Changed = %v after a failed login", got)
	}
}

// One login at a time (port 8123): a second one gets an explicit error.
func TestSecondLoginIsRefused(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("g1"), copilotAccount("g2"))
	c := newLoginControl(api)
	c.announce <- struct{}{}
	if w := api.call(t, http.MethodPost, "/api/v1/accounts/g1/login", loginBody); w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	w := api.call(t, http.MethodPost, "/api/v1/accounts/g2/login", loginBody)
	if code, reason := errorOf(t, w.Body.Bytes()); w.Code != http.StatusConflict || code != usage.CodeUnavailable || reason != "login_in_progress" {
		t.Errorf("status %d body %s", w.Code, w.Body)
	}
	// Once the first one ended, a new one may start.
	c.result <- []byte(connectedDoc)
	waitStatus(t, api, "/api/v1/accounts/g1/login", http.StatusOK)
	c.announce <- struct{}{}
	if w := api.call(t, http.MethodPost, "/api/v1/accounts/g2/login", loginBody); w.Code != http.StatusAccepted {
		t.Errorf("after the first: status %d body %s", w.Code, w.Body)
	}
	if w := api.call(t, http.MethodGet, "/api/v1/accounts/g1/login", ""); w.Code != http.StatusNotFound {
		t.Errorf("the old login is still reported: %d", w.Code)
	}
}

func TestLoginCanBeCancelled(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("g1"))
	c := newLoginControl(api)
	c.announce <- struct{}{}
	if w := api.call(t, http.MethodPost, "/api/v1/accounts/g1/login", loginBody); w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	w := api.call(t, http.MethodDelete, "/api/v1/accounts/g1/login", "")
	if code, reason := errorOf(t, w.Body.Bytes()); w.Code != http.StatusBadGateway || code != usage.CodeUnavailable || reason != "login_cancelled" {
		t.Errorf("status %d body %s", w.Code, w.Body)
	}
	if w2 := api.call(t, http.MethodGet, "/api/v1/accounts/g1/login", ""); w2.Body.String() != w.Body.String() {
		t.Errorf("status after cancel = %s", w2.Body)
	}
	if got := api.changed(); got != nil {
		t.Errorf("Changed = %v after a cancel", got)
	}
	if w := api.call(t, http.MethodDelete, "/api/v1/accounts/g2/login", ""); w.Code != http.StatusNotFound {
		t.Errorf("cancel without a login: status %d", w.Code)
	}
}

// A login that fails before announcing (port 8123 busy) answers its error.
func TestLoginFailingAtOnce(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("g1"))
	c := newLoginControl(api)
	busy := `{"schemaVersion":1,"provider":"copilot","error":{"code":"unavailable","message":"port busy"}}` + "\n"
	c.result <- []byte(busy)
	w := api.call(t, http.MethodPost, "/api/v1/accounts/g1/login", loginBody)
	if w.Code != http.StatusBadGateway || w.Body.String() != busy {
		t.Errorf("status %d body %s", w.Code, w.Body)
	}
}

// A child that does not announce its URL in time is stopped.
func TestLoginThatNeverAnnounces(t *testing.T) { //nolint:paralleltest // changes the start bound
	restore := service.SetLoginStartTimeout(50 * time.Millisecond)
	defer restore()
	api := newAccountAPI(t, copilotAccount("g1"))
	newLoginControl(api)
	w := api.call(t, http.MethodPost, "/api/v1/accounts/g1/login", loginBody)
	if code, _ := errorOf(t, w.Body.Bytes()); w.Code != http.StatusBadGateway || code != usage.CodeTimeout {
		t.Errorf("status %d body %s", w.Code, w.Body)
	}
	// The slot is free again.
	if w := api.call(t, http.MethodGet, "/api/v1/accounts/g1/login", ""); w.Code == http.StatusAccepted {
		t.Errorf("still pending: %s", w.Body)
	}
}

func TestLoginRejectsABadRequest(t *testing.T) {
	t.Parallel()
	api := newAccountAPI(t, copilotAccount("g1"), store.Account{ID: "x", Provider: "codex"})
	newLoginControl(api)
	cases := []struct{ path, body string }{
		{"/api/v1/accounts/x/login", loginBody},
		{"/api/v1/accounts/g1/login", `{"clientId":"Iv1.synthetic"}`},
		{"/api/v1/accounts/g1/login", `{"clientSecret":"s"}`},
		{"/api/v1/accounts/g1/login", `{"clientId":"a b","clientSecret":"s"}`},
		{"/api/v1/accounts/g1/login", `{"clientId":"a","clientSecret":"s","locale":"fr"}`},
		{"/api/v1/accounts/g1/login", `[]`},
	}
	for _, c := range cases {
		w := api.call(t, http.MethodPost, c.path, c.body)
		if code, _ := errorOf(t, w.Body.Bytes()); w.Code != http.StatusBadRequest || code != usage.CodeUsage {
			t.Errorf("%s %s: status %d body %s", c.path, c.body, w.Code, w.Body)
		}
	}
	if w := api.call(t, http.MethodPost, "/api/v1/accounts/nope/login", loginBody); w.Code != http.StatusNotFound {
		t.Errorf("unknown account: status %d", w.Code)
	}
}

// A stop of the service ends a pending login.
func TestStopEndsAPendingLogin(t *testing.T) {
	t.Parallel()
	api := newAPI(t, copilotAccount("g1"))
	commands := &fakeCommands{}
	ended := make(chan error, 1)
	commands.login = func(ctx context.Context, _ store.Account, _ refresh.LoginRequest, authorize func([]byte)) ([]byte, usage.Code, error) {
		authorize([]byte(authorizeDoc))
		<-ctx.Done()
		ended <- ctx.Err()
		return nil, "", ctx.Err()
	}
	work, stop := context.WithCancel(context.Background())
	defer stop()
	h := service.NewHandler(service.HandlerConfig{
		Token: testToken, Host: testHost, Store: api.st, Loop: api.loop, Accounts: commands, Context: work,
	})
	r := httptest.NewRequest(http.MethodPost, "http://"+testHost+"/api/v1/accounts/g1/login", strings.NewReader(loginBody))
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}
	stop()
	select {
	case err := <-ended:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the login outlived the stop")
	}
}
