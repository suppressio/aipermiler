package copilot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/secrets"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// OAuth App login, Authorization Code + PKCE over a loopback (port of
// main/copilot-oauth.ts). The user registers their own GitHub OAuth App with the
// callback http://127.0.0.1:8123/callback. The core prints the authorization URL and
// the caller opens it in the system browser (Electron with shell.openExternal, a person
// by hand): the core starts no browser and no OS-specific command.

const oauthScope = "read:user"

// PKCEPair is an RFC 7636 verifier and its S256 challenge.
type PKCEPair struct{ Verifier, Challenge string }

// NewPKCEPair returns a fresh verifier (48 random bytes, base64url) and challenge.
func NewPKCEPair() (PKCEPair, error) {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return PKCEPair{}, fmt.Errorf("random verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	return PKCEPair{Verifier: verifier, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// AuthorizationURL builds the GitHub authorize URL.
func AuthorizationURL(oauthBase, clientID, redirectURI, state, challenge string) string {
	query := url.Values{}
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", oauthScope)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	return oauthBase + "/authorize?" + query.Encode()
}

type pageText struct{ success, successMessage, failed, invalid, closeTab string }

var pages = map[string]pageText{
	"en": {"Copilot connected", "You can go back to AIpermiler.", "Copilot sign-in failed", "Invalid callback: start the sign-in again from the app.", "You can close this tab."},
	"it": {"Copilot collegato", "Puoi tornare a AIpermiler.", "Accesso a Copilot non riuscito", "Callback non valida: ripeti l'accesso dall'app.", "Puoi chiudere questa scheda."},
}

func page(lang, title, message string) string {
	text := pages[lang]
	return fmt.Sprintf("<!doctype html>\n<html lang=%q><head><meta charset=\"utf-8\"><title>%s</title></head>\n<body><h1>%s</h1><p>%s</p><p>%s</p></body></html>\n",
		lang, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message), html.EscapeString(text.closeTab))
}

type loginResult struct {
	username string
	err      error
}

// Login runs the OAuth flow: announce receives the authorization URL to open; the
// token is checked (GET /user) and stored in the keyring; the GitHub login is returned.
// The caller's ctx bounds the wait for the user (the CLI gives it 5 minutes).
func (p *Provider) Login(ctx context.Context, clientSecret string, announce func(string) error) (string, error) {
	clientID, clientSecret := strings.TrimSpace(p.clientID), strings.TrimSpace(clientSecret)
	if clientID == "" || clientSecret == "" {
		return "", &usage.Error{Code: usage.CodeUnavailable, Message: "Copilot OAuth: Client ID (--client-id) and Client Secret (stdin) are required"}
	}
	lang := p.locale
	if _, ok := pages[lang]; !ok {
		lang = "en"
	}
	pair, err := NewPKCEPair()
	if err != nil {
		return "", err
	}
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", fmt.Errorf("random state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", p.listenAddr)
	if err != nil {
		return "", &usage.Error{Code: usage.CodeUnavailable, Message: "Copilot OAuth: cannot start the callback server on " + p.listenAddr + ": " + err.Error()}
	}
	redirectURI := "http://" + listener.Addr().String() + "/callback"
	endpoints := p.apiEndpoints()

	results := make(chan loginResult, 1)
	finish := func(r loginResult) {
		select {
		case results <- r:
		default: // a second callback after the first result is ignored
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		text := pages[lang]
		query := r.URL.Query()
		if denied := query.Get("error"); denied != "" {
			description := query.Get("error_description")
			if description == "" {
				description = denied
			}
			_, _ = fmt.Fprint(w, page(lang, text.failed, description))
			finish(loginResult{err: &usage.Error{Code: usage.CodeUnavailable, Message: "GitHub authorization failed: " + description}})
			return
		}
		code := query.Get("code")
		if code == "" || query.Get("state") != state {
			_, _ = fmt.Fprint(w, page(lang, text.failed, text.invalid))
			finish(loginResult{err: &usage.Error{Code: usage.CodeUnavailable, Message: "GitHub OAuth callback validation failed"}})
			return
		}
		// The exchange stops when the request ends or the login deadline passes: a
		// token is never stored after Login has answered (timeout).
		work, cancel := context.WithCancel(r.Context())
		defer cancel()
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
		username, err := p.completeLogin(work, endpoints.OAuth, clientID, clientSecret, code, pair.Verifier, redirectURI)
		if err != nil {
			_, _ = fmt.Fprint(w, page(lang, text.failed, err.Error()))
			finish(loginResult{err: err})
			return
		}
		_, _ = fmt.Fprint(w, page(lang, text.success, text.successMessage))
		finish(loginResult{username: username})
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan struct{})
	go func() {
		// Serve returns http.ErrServerClosed after Shutdown: nothing to report.
		_ = server.Serve(listener)
		close(served)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		<-served
	}()

	if err := announce(AuthorizationURL(endpoints.OAuth, clientID, redirectURI, state, pair.Challenge)); err != nil {
		return "", fmt.Errorf("announce the authorization URL: %w", err)
	}
	select {
	case r := <-results:
		return r.username, r.err
	case <-ctx.Done():
		// A callback that succeeded at the same moment wins: its token is stored. A
		// failed one (cancelled by this very deadline) does not: the answer is timeout.
		select {
		case r := <-results:
			if r.err == nil {
				return r.username, nil
			}
		default:
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &usage.Error{Code: usage.CodeTimeout, Message: "GitHub login timed out: no authorization in time"}
		}
		return "", ctx.Err()
	}
}

// completeLogin exchanges the code (10 s), resolves the username and stores the token.
func (p *Provider) completeLogin(ctx context.Context, oauthBase, clientID, clientSecret, code, verifier, redirectURI string) (string, error) {
	answer, err := p.client.PostJSON(ctx, oauthBase+"/access_token", map[string]string{
		"client_id": clientID, "client_secret": clientSecret, "code": code,
		"code_verifier": verifier, "redirect_uri": redirectURI,
	}, "GitHub OAuth code exchange")
	if err != nil {
		return "", &usage.Error{Code: usage.CodeUnavailable, Message: strings.ReplaceAll(err.Error(), clientSecret, "[redacted]")}
	}
	document, _ := answer.(map[string]any)
	token, _ := document["access_token"].(string)
	if token == "" {
		reason, _ := document["error_description"].(string)
		if reason == "" {
			reason, _ = document["error"].(string)
		}
		if reason == "" {
			reason = "GitHub did not return an access_token"
		}
		return "", &usage.Error{Code: usage.CodeUnavailable, Message: "GitHub OAuth: " + reason}
	}
	username, err := Username(ctx, p.client, p.apiEndpoints().API, token)
	if err != nil {
		return "", classify(err, token)
	}
	if err := secrets.Set(ctx, p.store, secrets.CopilotKey(p.account), token); err != nil {
		return "", keyringError(err)
	}
	return username, nil
}
