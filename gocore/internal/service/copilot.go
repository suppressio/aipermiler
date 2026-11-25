package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// AccountCommands runs the CLI's account commands (refresh.CLIFetcher, #285):
// each returns the CLI's document as printed, with the code of an error
// document ("" otherwise). Secrets go to the child's stdin.
type AccountCommands interface {
	SetToken(ctx context.Context, account store.Account, token string) ([]byte, usage.Code, error)
	DeleteToken(ctx context.Context, account store.Account) ([]byte, usage.Code, error)
	// Login calls authorize with the CLI's authorize event as soon as the
	// child prints it, then returns the result document.
	Login(ctx context.Context, account store.Account, req refresh.LoginRequest, authorize func([]byte)) ([]byte, usage.Code, error)
}

const (
	// maxAccountBody bounds the bodies of the account actions: one secret of
	// at most 64 KiB (the CLI's limit) in a small object.
	maxAccountBody = 128 << 10
	// maxSecretBytes is the CLI's own limit on a secret read from stdin.
	maxSecretBytes = 64 << 10
	maxClientIDLen = 256
	// reasonLoginInProgress: a login runs already (one at a time: the OAuth
	// callback is the fixed port 8123).
	reasonLoginInProgress = "login_in_progress"
	reasonLoginCancelled  = "login_cancelled"
)

// loginStartTimeout bounds the wait for the child's authorize event: the CLI
// prints it as soon as its loopback listens, with no network call before.
var loginStartTimeout = 10 * time.Second

var (
	errLoginCancelled    = errors.New("the login was cancelled")
	errLoginNotAnnounced = errors.New("the login did not announce its URL in time")
)

// copilotAPI serves the Copilot account actions (#285): token set and delete,
// and the OAuth login, each wrapping the CLI command of the same name. The
// service holds no secret: a token or client secret lives in its request and
// in the child's stdin, nowhere else, and an answer that carries one is
// withheld.
type copilotAPI struct {
	store    *store.Store
	loop     Loop
	commands AccountCommands
	// work bounds a login, which outlives the request that starts it: a stop
	// of the service cancels it.
	work   context.Context
	logins *loginSlot
}

func (a copilotAPI) register(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/v1/accounts/{id}/token", a.setToken)
	mux.HandleFunc("DELETE /api/v1/accounts/{id}/token", a.deleteToken)
	mux.HandleFunc("POST /api/v1/accounts/{id}/login", a.startLogin)
	mux.HandleFunc("GET /api/v1/accounts/{id}/login", a.loginStatus)
	mux.HandleFunc("DELETE /api/v1/accounts/{id}/login", a.cancelLogin)
}

// copilotAccount finds the path's account, enabled or not (an account can be
// connected before it is enabled), and checks that the commands can run.
func (a copilotAPI) copilotAccount(w http.ResponseWriter, r *http.Request, what string) (store.Account, bool) {
	id := r.PathValue("id")
	account := a.store.Snapshot().Account(id)
	switch {
	case account == nil:
		writeUsageError(w, http.StatusNotFound, "No account with id "+id)
	case account.Provider != "copilot":
		writeUsageError(w, http.StatusBadRequest, "Provider "+account.Provider+" does not support "+what)
	case a.commands == nil:
		writeUnavailable(w, "Account actions are not available")
	default:
		return *account, true
	}
	return store.Account{}, false
}

func (a copilotAPI) setToken(w http.ResponseWriter, r *http.Request) {
	account, ok := a.copilotAccount(w, r, "tokens")
	if !ok {
		return
	}
	var req struct {
		Token *string `json:"token"`
	}
	if !decodeAccountBody(w, r, &req) {
		return
	}
	if req.Token == nil || *req.Token == "" || len(*req.Token) > maxSecretBytes {
		writeUsageError(w, http.StatusBadRequest, "token: a non-empty string of at most 64 KiB is required")
		return
	}
	doc, code, err := a.commands.SetToken(r.Context(), account, *req.Token)
	a.answer(w, r, account, doc, code, err, *req.Token)
}

func (a copilotAPI) deleteToken(w http.ResponseWriter, r *http.Request) {
	account, ok := a.copilotAccount(w, r, "tokens")
	if !ok {
		return
	}
	doc, code, err := a.commands.DeleteToken(r.Context(), account)
	a.answer(w, r, account, doc, code, err, "")
}

// answer writes the CLI's document of a token command; after a success the
// loop reads the account again at once (it is connected, or signed out now).
func (a copilotAPI) answer(w http.ResponseWriter, r *http.Request, account store.Account, doc []byte, code usage.Code, err error, secret string) {
	switch {
	case err != nil && r.Context().Err() != nil:
		writeStopping(w)
		return
	case err != nil:
		doc, code = errorDocument(account.Provider, err), classify(err).Code
	}
	doc, code = withoutSecret(account.Provider, doc, code, secret)
	if code == "" {
		a.loop.Changed(account.ID)
	}
	writeDocument(w, errorStatus(code), doc)
}

// withoutSecret replaces a document that carries the secret, as it is or
// JSON-escaped, with an error document. The CLI redacts its own messages, so
// this is the last guard, not the first.
func withoutSecret(provider string, doc []byte, code usage.Code, secret string) ([]byte, usage.Code) {
	if secret == "" {
		return doc, code
	}
	escaped, _ := json.Marshal(secret) // a string: cannot fail
	if !bytes.Contains(doc, []byte(secret)) && !bytes.Contains(doc, escaped[1:len(escaped)-1]) {
		return doc, code
	}
	withheld := &usage.Error{Code: usage.CodeUnavailable, Message: "The core's answer was withheld: it carried the secret"}
	var buf bytes.Buffer
	_ = usage.WriteError(&buf, provider, withheld)
	return buf.Bytes(), withheld.Code
}

// decodeAccountBody reads one JSON object into v; on failure it has answered
// a usage error that never quotes the body (it may hold a secret).
func decodeAccountBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAccountBody))
	err := dec.Decode(v)
	if err == nil {
		if _, tokErr := dec.Token(); !errors.Is(tokErr, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeUsageError(w, http.StatusRequestEntityTooLarge, "The body is over 128 KiB")
		return false
	case err != nil:
		writeUsageError(w, http.StatusBadRequest, "The body must be one JSON object with the documented fields")
		return false
	}
	return true
}

// loginSlot holds the one login of the service: running, or the last one
// that ended, whose result the status endpoint answers until the next starts.
type loginSlot struct {
	mu      sync.Mutex
	current *loginRun
}

type loginRun struct {
	accountID string
	cancel    context.CancelCauseFunc
	announce  sync.Once
	// authorized is closed once authorize holds the CLI's authorize event.
	authorized chan struct{}
	authorize  []byte
	// done is closed once result and status hold the login's end.
	done   chan struct{}
	result []byte
	status int
}

func (l *loginRun) ended() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

type loginBody struct {
	ClientID     *string `json:"clientId"`
	ClientSecret *string `json:"clientSecret"`
	Locale       string  `json:"locale"`
}

// checkLogin validates a login body; the message never quotes a value.
func checkLogin(b loginBody) string {
	switch {
	case b.ClientID == nil || *b.ClientID == "" || len(*b.ClientID) > maxClientIDLen ||
		strings.ContainsFunc(*b.ClientID, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		return "clientId: the OAuth App's client ID is required (no spaces, at most 256 characters)"
	case b.ClientSecret == nil || *b.ClientSecret == "" || len(*b.ClientSecret) > maxSecretBytes:
		return "clientSecret: a non-empty string of at most 64 KiB is required"
	case b.Locale != "" && b.Locale != "en" && b.Locale != "it":
		return "locale: must be en or it"
	}
	return ""
}

// startLogin starts the OAuth login in the background and answers the CLI's
// authorize event (202) as soon as the child prints it, or the login's error
// when it ends first. One login at a time: another one meanwhile gets 409.
func (a copilotAPI) startLogin(w http.ResponseWriter, r *http.Request) {
	account, ok := a.copilotAccount(w, r, "login")
	if !ok {
		return
	}
	var body loginBody
	if !decodeAccountBody(w, r, &body) {
		return
	}
	if message := checkLogin(body); message != "" {
		writeUsageError(w, http.StatusBadRequest, message)
		return
	}
	req := refresh.LoginRequest{ClientID: *body.ClientID, ClientSecret: *body.ClientSecret, Locale: body.Locale}

	a.logins.mu.Lock()
	if cur := a.logins.current; cur != nil && !cur.ended() {
		a.logins.mu.Unlock()
		var buf bytes.Buffer
		_ = usage.WriteError(&buf, "", &usage.Error{Code: usage.CodeUnavailable, Reason: reasonLoginInProgress,
			Message: "A login is already running (account " + cur.accountID + "): cancel it or wait for its end"})
		writeDocument(w, http.StatusConflict, buf.Bytes())
		return
	}
	ctx, cancel := context.WithCancelCause(a.work)
	l := &loginRun{accountID: account.ID, cancel: cancel, authorized: make(chan struct{}), done: make(chan struct{})}
	a.logins.current = l
	a.logins.mu.Unlock()
	go a.runLogin(ctx, l, account, req)

	timer := time.NewTimer(loginStartTimeout)
	defer timer.Stop()
	select {
	case <-l.authorized:
		writeDocument(w, http.StatusAccepted, l.authorize)
		return
	case <-l.done:
	case <-timer.C:
		l.cancel(errLoginNotAnnounced)
		<-l.done
	case <-r.Context().Done():
		// The client is gone or the service stops: nobody will open the URL.
		l.cancel(errLoginCancelled)
		<-l.done
	}
	writeDocument(w, l.status, l.result)
}

// runLogin runs the child to its end and records the result. A connected
// account is read again at once, as a forced read (Loop.Changed).
func (a copilotAPI) runLogin(ctx context.Context, l *loginRun, account store.Account, req refresh.LoginRequest) {
	defer close(l.done)
	defer l.cancel(nil)
	doc, code, err := a.commands.Login(ctx, account, req, func(event []byte) {
		l.announce.Do(func() {
			l.authorize, _ = withoutSecret(account.Provider, event, "", req.ClientSecret)
			close(l.authorized)
		})
	})
	status := 0
	if err != nil {
		var e *usage.Error
		switch cause := context.Cause(ctx); {
		case ctx.Err() == nil:
			e = classify(err)
		case errors.Is(cause, errLoginCancelled):
			e = &usage.Error{Code: usage.CodeUnavailable, Reason: reasonLoginCancelled, Message: "The login was cancelled"}
		case errors.Is(cause, errLoginNotAnnounced):
			e = &usage.Error{Code: usage.CodeTimeout, Message: "The login did not announce its URL within " + loginStartTimeout.String()}
		default:
			e = &usage.Error{Code: usage.CodeUnavailable, Message: "The service is stopping"}
			status = http.StatusServiceUnavailable
		}
		var buf bytes.Buffer
		_ = usage.WriteError(&buf, account.Provider, e)
		doc, code = buf.Bytes(), e.Code
	}
	doc, code = withoutSecret(account.Provider, doc, code, req.ClientSecret)
	if status == 0 {
		status = errorStatus(code)
	}
	l.result, l.status = doc, status
	if code == "" {
		a.loop.Changed(account.ID)
	}
}

// login is the slot's login when it is the path account's; otherwise it has
// answered 404.
func (a copilotAPI) login(w http.ResponseWriter, r *http.Request) (*loginRun, bool) {
	id := r.PathValue("id")
	a.logins.mu.Lock()
	l := a.logins.current
	a.logins.mu.Unlock()
	if l == nil || l.accountID != id {
		writeUsageError(w, http.StatusNotFound, "No login for account "+id)
		return nil, false
	}
	return l, true
}

// loginStatus answers 202 with the authorize event while the login waits for
// the browser, then its result: the `username` document (200) or the error
// document (timeout, denied, cancelled…).
func (a copilotAPI) loginStatus(w http.ResponseWriter, r *http.Request) {
	l, ok := a.login(w, r)
	if !ok {
		return
	}
	writeLoginStatus(w, l)
}

func writeLoginStatus(w http.ResponseWriter, l *loginRun) {
	if l.ended() {
		writeDocument(w, l.status, l.result)
		return
	}
	select {
	case <-l.authorized:
		writeDocument(w, http.StatusAccepted, l.authorize)
	default:
		writeUnavailable(w, "The login is starting")
	}
}

// cancelLogin stops a running login (the child ends, the port is freed) and
// answers its status, as GET does; on an ended login it only answers it.
func (a copilotAPI) cancelLogin(w http.ResponseWriter, r *http.Request) {
	l, ok := a.login(w, r)
	if !ok {
		return
	}
	l.cancel(errLoginCancelled)
	select {
	case <-l.done:
	case <-r.Context().Done():
		writeStopping(w)
		return
	}
	writeLoginStatus(w, l)
}

// deleteRemovedTokens deletes the token of every Copilot account a settings
// write removed (#285). The removal is already committed, so a token that
// cannot be deleted is logged (no secret in it), not answered.
func deleteRemovedTokens(ctx context.Context, commands AccountCommands, logger *slog.Logger, removed []store.Account) {
	if commands == nil {
		return
	}
	for _, account := range removed {
		doc, code, err := commands.DeleteToken(ctx, account)
		if err == nil && code == "" {
			continue
		}
		e := &usage.Error{Code: code}
		if err != nil {
			e = classify(err)
		} else if parsed, perr := usage.ParseDocument(doc); perr == nil && parsed.Err != nil {
			e = parsed.Err
		}
		logger.Warn("Cannot delete the token of a removed account", "account", account.ID, "code", e.Code, "reason", e.Reason, "message", e.Message)
	}
}
