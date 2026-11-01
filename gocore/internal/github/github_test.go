package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/github"
)

func TestNormalizeHost(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"github.com": "github.com", "\tGitHub.com\n": "github.com", "https://github.com/settings": "github.com",
		"api.github.com": "github.com", "acme.ghe.com": "acme.ghe.com", "https://API.Acme.ghe.com/": "acme.ghe.com",
		"http://github.com": "", "github.com:443": "", "evil.com": "", "user@github.com": "", "a.b.ghe.com": "",
		"-acme.ghe.com": "", "": "",
	} {
		if got := github.NormalizeHost(input); got != want {
			t.Errorf("NormalizeHost(%q) = %q; want %q", input, got, want)
		}
	}
}

func TestEndpointsFor(t *testing.T) {
	t.Parallel()
	e, err := github.EndpointsFor("acme.ghe.com")
	if err != nil || e.API != "https://api.acme.ghe.com" || e.OAuth != "https://acme.ghe.com/login/oauth" {
		t.Fatalf("EndpointsFor = %+v, %v", e, err)
	}
	if _, err := github.EndpointsFor("evil.com"); err == nil {
		t.Fatal("EndpointsFor(evil.com) returned no error")
	}
}

func TestGetJSON(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			if r.Header.Get("Authorization") != "Bearer synthetic-token" || r.Header.Get("X-GitHub-Api-Version") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"login":"octo"}`))
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found","documentation_url":"x"}`))
		case "/garbage":
			_, _ = w.Write([]byte(`{"login":`))
		}
	}))
	defer server.Close()
	client := github.NewClient("test")
	got, err := client.GetJSON(context.Background(), server.URL+"/ok", "synthetic-token", "user")
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := got.(map[string]any); !ok || m["login"] != "octo" {
		t.Fatalf("GetJSON = %#v", got)
	}
	_, err = client.GetJSON(context.Background(), server.URL+"/missing", "synthetic-token", "billing")
	if github.StatusOf(err) != http.StatusNotFound || !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("missing: %v", err)
	}
	_, err = client.GetJSON(context.Background(), server.URL+"/garbage", "synthetic-token", "user")
	if err == nil || strings.Contains(err.Error(), "login") {
		t.Fatalf("garbage: %v (must not quote the body)", err)
	}
}

func TestPostJSONAndTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("Authorization") != "" || body["code"] != "c" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"t"}`))
	}))
	defer server.Close()
	client := github.NewClient("test")
	got, err := client.PostJSON(context.Background(), server.URL+"/token", map[string]string{"code": "c"}, "exchange")
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := got.(map[string]any); !ok || m["access_token"] != "t" {
		t.Fatalf("PostJSON = %#v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*1000*1000)
	defer cancel()
	_, err = client.GetJSON(ctx, server.URL+"/slow", "x", "slow")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow: %v; want a deadline", err)
	}
}
