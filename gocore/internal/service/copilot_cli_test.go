package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/service"
	"github.com/suppressio/aipermiler/gocore/internal/store"
)

// fakeCLIEnv makes the test binary a fake core CLI for the account commands:
// it appends its arguments and its stdin to files in that folder, writes its
// stdin to stderr too (a careless child), and prints what the real CLI would.
const fakeCLIEnv = "AIPERMILER_TEST_FAKE_ACCOUNT_CLI"

func fakeAccountCLI(dir string) int {
	stdin, _ := io.ReadAll(os.Stdin)
	appendTo := func(name string, data []byte) {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.Write(append(data, '\n'))
			_ = f.Close()
		}
	}
	appendTo("args", []byte(strings.Join(os.Args[1:], " ")))
	appendTo("stdin", stdin)
	_, _ = fmt.Fprintf(os.Stderr, "stdin was %s\n", stdin)
	switch {
	case len(os.Args) > 2 && os.Args[1] == "token" && os.Args[2] == "set":
		fmt.Println(`{"schemaVersion":1,"provider":"copilot","username":"octo"}`)
	case len(os.Args) > 2 && os.Args[1] == "token" && os.Args[2] == "delete":
		fmt.Println(`{"schemaVersion":1,"provider":"copilot","deleted":true}`)
	case len(os.Args) > 1 && os.Args[1] == "login":
		fmt.Println(`{"schemaVersion":1,"provider":"copilot","event":"authorize","url":"https://github.com/login/oauth/authorize?client_id=Iv1.synthetic&state=synthetic"}`)
		time.Sleep(50 * time.Millisecond) // the browser round trip
		fmt.Println(`{"schemaVersion":1,"provider":"copilot","username":"octo"}`)
	default:
		return 2
	}
	return 0
}

// End to end with a fake CLI child: the token and the client secret reach the
// child on stdin and appear in no argument, answer, log or file of the
// service (#285).
func TestSecretsReachOnlyTheChildsStdin(t *testing.T) {
	const (
		token        = "ghp_SYNTHETIC_TOKEN_0123456789"
		clientSecret = "SYNTHETIC_CLIENT_SECRET_abcdef"
	)
	cliDir, storeDir := t.TempDir(), t.TempDir()
	t.Setenv(fakeCLIEnv, cliDir)
	st, err := store.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	every := store.Schedule{Days: [7]float64{1, 1, 1, 1, 1, 1, 1}, HoursPerDay: 8}
	if err := st.Update(func(s *store.State) error {
		s.Accounts = []store.Account{{ID: "g1", Provider: "copilot", Label: "g1", Enabled: true, Host: "acme.ghe.com", Schedule: every}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	loop := &fakeLoop{errs: map[string]error{}}
	h := service.NewHandler(service.HandlerConfig{
		Token: testToken, Host: testHost, Store: st, Loop: loop,
		Accounts: refresh.CLIFetcher{Binary: os.Args[0], TokenTimeout: 10 * time.Second, LoginTimeout: 10 * time.Second, Margin: 5 * time.Second},
		Context:  context.Background(),
		Logger:   slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	var answers bytes.Buffer
	call := func(method, path string, body any) int {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, "http://"+testHost+path, bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		answers.Write(w.Body.Bytes())
		return w.Code
	}

	if code := call(http.MethodPut, "/api/v1/accounts/g1/token", map[string]string{"token": token}); code != http.StatusOK {
		t.Fatalf("token set: %d\n%s", code, answers.String())
	}
	if code := call(http.MethodPost, "/api/v1/accounts/g1/login", map[string]string{"clientId": "Iv1.synthetic", "clientSecret": clientSecret}); code != http.StatusAccepted {
		t.Fatalf("login: %d\n%s", code, answers.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for call(http.MethodGet, "/api/v1/accounts/g1/login", nil) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatalf("the login did not end\n%s", answers.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code := call(http.MethodDelete, "/api/v1/accounts/g1/token", nil); code != http.StatusOK {
		t.Fatalf("token delete: %d", code)
	}
	settings := `{"schemaVersion":1,"settings":{"refreshIntervalMinutes":30,"retentionDays":90,"notificationThresholdPercent":80},"accounts":[]}`
	if code := call(http.MethodPut, "/api/v1/settings", json.RawMessage(settings)); code != http.StatusOK {
		t.Fatalf("settings: %d", code)
	}

	args, _ := os.ReadFile(filepath.Join(cliDir, "args"))
	if want := 4; strings.Count(string(args), "\n") != want {
		t.Errorf("the child ran %d times; want %d:\n%s", strings.Count(string(args), "\n"), want, args)
	}
	stdin, _ := os.ReadFile(filepath.Join(cliDir, "stdin"))
	for _, secret := range []string{token, clientSecret} {
		if !strings.Contains(string(stdin), secret) {
			t.Errorf("%s did not reach the child's stdin", secret)
		}
		for name, data := range map[string]string{"argv": string(args), "answers": answers.String(), "log": logs.String()} {
			if strings.Contains(data, secret) {
				t.Errorf("%s is in the %s:\n%s", secret, name, data)
			}
		}
		_ = filepath.WalkDir(storeDir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if data, _ := os.ReadFile(path); bytes.Contains(data, []byte(secret)) {
					t.Errorf("%s is in %s", secret, path)
				}
			}
			return nil
		})
	}
	entries, _ := os.ReadDir(storeDir)
	if len(entries) == 0 {
		t.Error("no store file was written: the file check checked nothing")
	}
}
