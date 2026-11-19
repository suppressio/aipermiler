package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/service"
)

type running struct {
	info   service.Info
	cancel context.CancelFunc
	done   chan error
}

func start(t *testing.T, dir string) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{cancel: cancel, done: make(chan error, 1)}
	go func() {
		r.done <- service.Run(ctx, service.Options{
			Dir: dir, Version: "9.9.9", ContractVersion: 1,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), ShutdownTimeout: 2 * time.Second,
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-r.done; err != nil {
			t.Errorf("Run after cleanup cancel: %v", err)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-r.done:
			r.done <- err
			t.Fatalf("Run returned early: %v", err)
		default:
		}
		if raw, err := os.ReadFile(filepath.Join(dir, "api.json")); err == nil {
			if json.Unmarshal(raw, &r.info) == nil && r.info.Port != 0 {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("api.json did not appear")
	return nil
}

func (r *running) get(t *testing.T, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(r.info.Port)+"/api/v1/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestRunServesHealthAndCleansUp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := start(t, dir)

	if r.info.SchemaVersion != 1 || r.info.Version != "9.9.9" || r.info.ContractVersion != 1 || r.info.PID != os.Getpid() || r.info.StartedAt.IsZero() {
		t.Errorf("api.json = %+v", r.info)
	}
	if len(r.info.Token) != 64 { // 32 random bytes, hex
		t.Errorf("token length = %d; want 64 hex characters", len(r.info.Token))
	}
	if resp := r.get(t, r.info.Token); resp.StatusCode != http.StatusOK {
		t.Fatalf("health with the token = %d", resp.StatusCode)
	}
	if resp := r.get(t, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("health without the token = %d", resp.StatusCode)
	}

	r.cancel()
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatalf("Run after cancel: %v", err)
		}
		r.done <- nil
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if _, err := os.Stat(filepath.Join(dir, "api.json")); !os.IsNotExist(err) {
		t.Errorf("api.json survived a clean stop: %v", err)
	}
	l, err := service.AcquireLock(dir)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	_ = l.Release()
}

func TestRunRefusesSecondInstance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := start(t, dir)
	err := service.Run(t.Context(), service.Options{Dir: dir, Version: "x", ContractVersion: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if !errors.Is(err, service.ErrAlreadyRunning) {
		t.Fatalf("second Run = %v; want ErrAlreadyRunning", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "api.json"))
	if err != nil {
		t.Fatal(err)
	}
	var still service.Info
	if err := json.Unmarshal(raw, &still); err != nil || still.Token != first.info.Token {
		t.Fatalf("the refused instance touched api.json: %s (%v)", raw, err)
	}
}

func TestRunReplacesStaleInfo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "api.json"), []byte(`{"schemaVersion":1,"port":1,"token":"stale"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- service.Run(ctx, service.Options{Dir: dir, Version: "x", ContractVersion: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run after cancel: %v", err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(filepath.Join(dir, "api.json")); err == nil {
			var info service.Info
			if json.Unmarshal(raw, &info) == nil && info.Token != "stale" && info.Port > 1 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the stale api.json was not replaced")
}

func TestRunCleansLeftoversAtStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	leftover := filepath.Join(dir, "api.json.12345.tmp")
	if err := os.WriteFile(leftover, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	start(t, dir)
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("the temp file of a dead instance survived: %v", err)
	}
}

func TestRunStopBeforeStartIsClean(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // the stop arrives before Run gets going
	err := service.Run(ctx, service.Options{Dir: dir, Version: "x", ContractVersion: 1})
	if err != nil {
		t.Fatalf("Run with an already cancelled context = %v; want nil", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "api.json")); !os.IsNotExist(err) {
		t.Errorf("api.json survived: %v", err)
	}
}
