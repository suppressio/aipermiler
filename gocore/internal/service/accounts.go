package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/refresh/policy"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// Loop is the refresh loop the service runs and its endpoints read
// (refresh.Loop, #283).
type Loop interface {
	Run(ctx context.Context) error
	// Refresh returns once a run that started after the call has ended
	// (coalesced with the runs already asked for), ctx's error,
	// refresh.ErrStopped once the loop has stopped, or another error when it
	// cannot refresh at all.
	Refresh(ctx context.Context, mode policy.Mode) error
	// LastError is the account's last read error, nil after a good read, and
	// refresh.ErrNotRead before the loop has read the account in this process
	// (its lastGood is then not confirmed: stale).
	LastError(accountID string) error
	// Changed tells the loop that these accounts were added, removed, enabled,
	// disabled or are now read differently (#284): it forgets what it knew of
	// them, so LastError is refresh.ErrNotRead until it has read them again,
	// at once. It does not wait.
	Changed(accountIDs ...string)
}

// RawFunc reads an account's diagnostic raw document through the CLI
// (refresh.CLIFetcher.Raw): the document as printed, with the code of an
// error document ("" for a raw one).
type RawFunc func(ctx context.Context, account store.Account) ([]byte, usage.Code, error)

// noLoop is the loop of a service run without one: nothing is ever read.
type noLoop struct{}

var errNoLoop = errors.New("this service has no refresh loop")

func (noLoop) Run(ctx context.Context) error { <-ctx.Done(); return nil }
func (noLoop) Refresh(context.Context, policy.Mode) error {
	return errNoLoop
}
func (noLoop) LastError(string) error { return refresh.ErrNotRead }
func (noLoop) Changed(...string)      {}

// maxRefreshBody bounds the body of POST /api/v1/refresh: one small object.
const maxRefreshBody = 4 << 10

// accountsAPI serves the read side of the API (#248) from what the loop holds:
// the store's lastGood and the loop's last error per account. Nothing here
// reads a provider, except raw, which runs the CLI for the diagnostic report.
type accountsAPI struct {
	store *store.Store
	loop  Loop
	raw   RawFunc
	// rawSlot holds one token: one raw read at a time, as the loop reads one
	// account at a time; a second request meanwhile answers 503 at once.
	rawSlot chan struct{}
}

func (a accountsAPI) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/usage", a.list)
	mux.HandleFunc("GET /api/v1/accounts/{id}/usage", a.usage)
	mux.HandleFunc("GET /api/v1/accounts/{id}/raw", a.rawDocument)
	mux.HandleFunc("POST /api/v1/refresh", a.refresh)
}

// usage answers the account's document exactly as `usage <provider> --json`
// prints it.
func (a accountsAPI) usage(w http.ResponseWriter, r *http.Request) {
	account, ok := a.enabledAccount(w, r)
	if !ok {
		return
	}
	doc, status := a.document(account)
	writeDocument(w, status, doc)
}

func (a accountsAPI) list(w http.ResponseWriter, _ *http.Request) {
	writeList(w, a.documents())
}

// refresh asks the loop for a run in the given mode and answers with the
// documents as they are after it. The wait honours the request's context,
// which a stop cancels.
func (a accountsAPI) refresh(w http.ResponseWriter, r *http.Request) {
	mode, message := refreshMode(http.MaxBytesReader(w, r.Body, maxRefreshBody))
	if message != "" {
		writeUsageError(w, http.StatusBadRequest, message)
		return
	}
	err := a.loop.Refresh(r.Context(), mode)
	switch {
	case errors.Is(err, refresh.ErrStopped) || (err != nil && r.Context().Err() != nil):
		writeStopping(w)
		return
	case err != nil:
		writeUnavailable(w, "Refresh is not available: "+err.Error())
		return
	}
	writeList(w, a.documents())
}

// rawDocument passes the CLI's raw (or error) document through unchanged.
func (a accountsAPI) rawDocument(w http.ResponseWriter, r *http.Request) {
	account, ok := a.enabledAccount(w, r)
	if !ok {
		return
	}
	if a.raw == nil {
		writeDocument(w, http.StatusBadGateway, errorDocument(account.Provider, errors.New("raw responses are not available")))
		return
	}
	select {
	case <-a.rawSlot:
		defer func() { a.rawSlot <- struct{}{} }()
	default:
		writeUnavailable(w, "A raw read is already running")
		return
	}
	doc, code, err := a.raw(r.Context(), account)
	switch {
	case errors.Is(err, context.Canceled) || (err != nil && r.Context().Err() != nil):
		writeStopping(w)
	case err != nil:
		writeDocument(w, http.StatusBadGateway, errorDocument(account.Provider, err))
	default:
		writeDocument(w, errorStatus(code), doc)
	}
}

// enabledAccount finds the path's account; a missing or disabled one is a
// usage error (404): the service neither reads nor serves it.
func (a accountsAPI) enabledAccount(w http.ResponseWriter, r *http.Request) (store.Account, bool) {
	return enabledAccountIn(a.store.Snapshot(), w, r)
}

// enabledAccountIn is enabledAccount on a store snapshot the caller already
// holds, for a handler that reads more of the same state.
func enabledAccountIn(state store.State, w http.ResponseWriter, r *http.Request) (store.Account, bool) {
	id := r.PathValue("id")
	if account := state.Account(id); account != nil && account.Enabled {
		return *account, true
	}
	writeUsageError(w, http.StatusNotFound, "No enabled account with id "+id)
	return store.Account{}, false
}

type listEntry struct {
	ID       string          `json:"id"`
	Document json.RawMessage `json:"document"`
}

func (a accountsAPI) documents() []listEntry {
	snap := a.store.Snapshot()
	entries := []listEntry{}
	for _, account := range snap.Accounts {
		if !account.Enabled {
			continue
		}
		doc, _ := a.document(account)
		entries = append(entries, listEntry{ID: account.ID, Document: bytes.TrimSpace(doc)})
	}
	return entries
}

// document is the account's contract v1 document and its HTTP status: the
// last good read (stale while the last read failed, as the CLI marks the
// provider's last known values), the last error when there was never a good
// read, ErrNotRead before the loop's first read of the account. The loop commits lastGood and then
// clears the error in two steps, so a request in between may see fresh values
// still marked stale (or the reverse) for that instant; the next request is
// right. Accepted: the document is a view, nothing is recorded from it.
func (a accountsAPI) document(account store.Account) ([]byte, int) {
	lastErr := a.loop.LastError(account.ID)
	if lastErr == nil && account.LastGood == nil { // a loop that read nothing for it
		lastErr = refresh.ErrNotRead
	}
	if account.LastGood != nil {
		var buf bytes.Buffer
		_ = usage.WriteSuccess(&buf, account.Provider, lastGoodUsage(*account.LastGood, lastErr), account.LastGood.UpdatedAt) // a bytes.Buffer does not fail
		return buf.Bytes(), http.StatusOK
	}
	return errorDocument(account.Provider, lastErr), statusOf(lastErr)
}

// lastGoodUsage is the last good read, stale with lastErr when it is not nil.
func lastGoodUsage(lg store.LastGood, lastErr error) usage.Usage {
	u := lg.Usage()
	if lastErr != nil {
		e := classify(lastErr)
		u.Stale = &usage.StaleInfo{Code: e.Code, Message: e.Message, Reason: e.Reason}
	}
	return u
}

// statusOf is the HTTP status of a read error's document: errorStatus of its
// code, and 503 (retry later) for refresh.ErrNotRead, an unavailable the loop
// is about to clear.
func statusOf(err error) int {
	if errors.Is(err, refresh.ErrNotRead) {
		return http.StatusServiceUnavailable
	}
	return errorStatus(classify(err).Code)
}

// classify maps a read error to the CLI's error: a child that outlived its
// deadline is a timeout, as the CLI's own deadline is.
func classify(err error) *usage.Error {
	if errors.Is(err, refresh.ErrChildTimeout) {
		return &usage.Error{Code: usage.CodeTimeout, Message: err.Error()}
	}
	return usage.ClassifyError(err)
}

func errorDocument(provider string, err error) []byte {
	var buf bytes.Buffer
	_ = usage.WriteError(&buf, provider, classify(err))
	return buf.Bytes()
}

// errorStatus is the HTTP status of a document with this error code: 200 for
// none, 400 for a usage error (as exit 2), 503 for not_ready (retry later),
// 502 for any other provider error (as exit 1). A read error goes through
// statusOf, which also knows not_read_yet.
func errorStatus(code usage.Code) int {
	switch code {
	case "":
		return http.StatusOK
	case usage.CodeUsage:
		return http.StatusBadRequest
	case usage.CodeNotReady:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

// refreshMode reads {"mode": "scheduled"|"manual"|"forced"}; an empty body or
// no mode means manual (a client asking is a user asking). Unknown fields are
// ignored (additive changes).
func refreshMode(body io.Reader) (policy.Mode, string) {
	var req struct {
		Mode *policy.Mode `json:"mode"`
	}
	dec := json.NewDecoder(body)
	if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		return "", "The body must be one JSON object with an optional mode"
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", "The body must be one JSON object with an optional mode"
	}
	if req.Mode == nil {
		return policy.Manual, ""
	}
	switch *req.Mode {
	case policy.Scheduled, policy.Manual, policy.Forced:
		return *req.Mode, ""
	}
	return "", "Unknown refresh mode: " + string(*req.Mode)
}

func writeList(w http.ResponseWriter, entries []listEntry) {
	data, _ := json.Marshal(struct { // RawMessage of valid documents: cannot fail
		SchemaVersion int         `json:"schemaVersion"`
		Accounts      []listEntry `json:"accounts"`
	}{usage.SchemaVersion, entries})
	writeDocument(w, http.StatusOK, append(data, '\n'))
}

func writeUsageError(w http.ResponseWriter, status int, message string) {
	var buf bytes.Buffer
	_ = usage.WriteError(&buf, "", &usage.Error{Code: usage.CodeUsage, Message: message})
	writeDocument(w, status, buf.Bytes())
}

// writeStopping answers a request cut by the stop (or by its client leaving).
func writeStopping(w http.ResponseWriter) { writeUnavailable(w, "The service is stopping") }

func writeUnavailable(w http.ResponseWriter, message string) {
	var buf bytes.Buffer
	_ = usage.WriteError(&buf, "", &usage.Error{Code: usage.CodeUnavailable, Message: message})
	writeDocument(w, http.StatusServiceUnavailable, buf.Bytes())
}

func writeDocument(w http.ResponseWriter, status int, doc []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(doc)
}
