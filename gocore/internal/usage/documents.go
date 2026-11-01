package usage

import (
	"encoding/json"
	"io"
	"time"
)

// SuccessDocument is shared by CLI output and future HTTP responses.
type SuccessDocument struct {
	SchemaVersion int        `json:"schemaVersion"`
	Provider      string     `json:"provider"`
	FetchedAt     string     `json:"fetchedAt"`
	Stale         bool       `json:"stale"`
	StaleReason   *StaleInfo `json:"staleReason,omitempty"`
	Usage         Usage      `json:"usage"`
}

// ErrorDocument omits the provider for command-line usage errors.
type ErrorDocument struct {
	SchemaVersion int    `json:"schemaVersion"`
	Provider      string `json:"provider,omitempty"`
	Error         *Error `json:"error"`
}

// WriteSuccess writes exactly one compact document followed by a newline.
func WriteSuccess(w io.Writer, provider string, u Usage, fetchedAt time.Time) error {
	return encode(w, SuccessDocument{
		SchemaVersion: SchemaVersion, Provider: provider,
		FetchedAt: fetchedAt.UTC().Format(time.RFC3339),
		Stale:     u.Stale != nil, StaleReason: u.Stale, Usage: u,
	})
}

// WriteError writes a provider error, or a usage error with an empty provider.
func WriteError(w io.Writer, provider string, err *Error) error {
	return encode(w, ErrorDocument{SchemaVersion, provider, err})
}

// WriteConnected writes an account connected (token set, login), with the provider's user name.
func WriteConnected(w io.Writer, provider, username string) error {
	return encode(w, struct {
		SchemaVersion int    `json:"schemaVersion"`
		Provider      string `json:"provider"`
		Username      string `json:"username"`
	}{SchemaVersion, provider, username})
}

// WriteDeleted writes that the account's token was removed.
func WriteDeleted(w io.Writer, provider string) error {
	return encode(w, struct {
		SchemaVersion int    `json:"schemaVersion"`
		Provider      string `json:"provider"`
		Deleted       bool   `json:"deleted"`
	}{SchemaVersion, provider, true})
}

// WriteAuthorize writes the login event carrying the authorization URL to open.
func WriteAuthorize(w io.Writer, provider, url string) error {
	return encode(w, struct {
		SchemaVersion int    `json:"schemaVersion"`
		Provider      string `json:"provider"`
		Event         string `json:"event"`
		URL           string `json:"url"`
	}{SchemaVersion, provider, "authorize", url})
}

// WriteRaw writes the provider's raw usage response (values included: for the diagnostic
// report, which redacts it).
func WriteRaw(w io.Writer, provider, endpoint string, response json.RawMessage) error {
	return encode(w, struct {
		SchemaVersion int             `json:"schemaVersion"`
		Provider      string          `json:"provider"`
		Endpoint      string          `json:"endpoint"`
		Response      json.RawMessage `json:"response"`
	}{SchemaVersion, provider, endpoint, response})
}

// encode writes one compact document and a newline. No HTML escaping: the documents
// are read by programs, and URLs (the login event) stay readable with a literal "&".
func encode(w io.Writer, v any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(v)
}
