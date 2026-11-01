// Package github holds what every GitHub call of the core shares: which hosts a token
// may be sent to (github.com or a <tenant>.ghe.com GitHub Enterprise Cloud tenant,
// port of services/githubHost.ts) and a JSON client with an explicit timeout.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// DefaultHost is the public GitHub.
const DefaultHost = "github.com"

// CallTimeout bounds every request (AGENTS.md: at most 10 seconds).
const CallTimeout = 10 * time.Second

var tenantHost = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.ghe\.com$`)

// NormalizeHost turns user input into "github.com" or "<tenant>.ghe.com", or "" when
// it is neither. Tolerates an https:// prefix, a trailing path, upper case, spaces and
// the "api." prefix; refuses http, ports, credentials and any other domain.
func NormalizeHost(value string) string {
	host := strings.ToLower(strings.TrimSpace(value))
	host = strings.TrimPrefix(host, "https://")
	if slash := strings.IndexByte(host, '/'); slash != -1 {
		host = host[:slash]
	}
	host = strings.TrimPrefix(host, "api.")
	if host == DefaultHost || tenantHost.MatchString(host) {
		return host
	}
	return ""
}

// Endpoints are the base URLs of one GitHub host.
type Endpoints struct {
	API   string // https://api.<host>
	OAuth string // https://<host>/login/oauth
}

// EndpointsFor returns the endpoints of a normalized host.
func EndpointsFor(host string) (Endpoints, error) {
	normalized := NormalizeHost(host)
	if normalized == "" {
		return Endpoints{}, fmt.Errorf("%q is not a supported GitHub host (github.com or <tenant>.ghe.com)", host)
	}
	return Endpoints{API: "https://api." + normalized, OAuth: "https://" + normalized + "/login/oauth"}, nil
}

// HTTPError is a non-2xx answer. Message is GitHub's own "message" field when the body
// is a GitHub error document; the rest of the body is never kept.
type HTTPError struct {
	Status  int
	Label   string
	Message string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s answered %d: %s", e.Label, e.Status, e.Message)
	}
	return fmt.Sprintf("%s answered %d", e.Label, e.Status)
}

// StatusOf returns the HTTP status of an HTTPError in err's chain, or 0.
func StatusOf(err error) int {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status
	}
	return 0
}

// Client sends JSON requests with a per-call timeout.
type Client struct {
	HTTP      *http.Client
	UserAgent string
}

// NewClient returns a client with the default transport.
func NewClient(userAgent string) *Client {
	return &Client{HTTP: &http.Client{}, UserAgent: userAgent}
}

// maxBody bounds a response: usage documents are a few KB.
const maxBody = 4 << 20

// GetJSON GETs url with the token as a Bearer and decodes the JSON body into any.
func (c *Client) GetJSON(ctx context.Context, url, token, label string) (any, error) {
	return decode(c.do(ctx, http.MethodGet, url, token, nil, label))
}

// GetRaw is GetJSON returning the body bytes, checked to be valid JSON: for readers
// that need the original key order.
func (c *Client) GetRaw(ctx context.Context, url, token, label string) ([]byte, error) {
	data, err := c.do(ctx, http.MethodGet, url, token, nil, label)
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("invalid JSON response from %s", label)
	}
	return data, nil
}

// PostJSON POSTs body as JSON (no Authorization) and decodes the answer.
func (c *Client) PostJSON(ctx context.Context, url string, body any, label string) (any, error) {
	return decode(c.do(ctx, http.MethodPost, url, "", body, label))
}

func decode(data []byte, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		// The parser error can quote the body: never forwarded.
		return nil, errors.New("invalid JSON response")
	}
	return decoded, nil
}

func (c *Client) do(ctx context.Context, method, url, token string, body any, label string) ([]byte, error) {
	callCtx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode %s request: %w", label, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(callCtx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", label, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.UserAgent)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("timeout calling %s: %w", label, context.DeadlineExceeded)
		}
		// The transport error can quote the URL, never the token (it is a header).
		return nil, fmt.Errorf("network error calling %s: %w", label, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", label, err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		httpErr := &HTTPError{Status: response.StatusCode, Label: label}
		var document struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &document) == nil && len(document.Message) <= 200 {
			httpErr.Message = document.Message
		}
		return nil, httpErr
	}
	if !json.Valid(data) {
		// Never the body itself: it could quote account data.
		return nil, fmt.Errorf("invalid JSON response from %s", label)
	}
	return data, nil
}
