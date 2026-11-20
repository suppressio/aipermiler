package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/service"
)

// logBuffer collects the service's log lines; slog writes from several goroutines.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p) //nolint:wrapcheck // in-memory buffer
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type running struct {
	info      service.Info
	cancel    context.CancelFunc
	done      chan error
	logs      *logBuffer
	once      sync.Once
	err       error
	expectErr bool // the test checks Run's error itself
}

// wait returns Run's result, at most 5 s after it is asked; later calls repeat it.
func (r *running) wait() error {
	r.once.Do(func() {
		select {
		case r.err = <-r.done:
		case <-time.After(5 * time.Second):
			r.err = errors.New("Run did not stop")
		}
	})
	return r.err
}

func (r *running) stop() error {
	r.cancel()
	return r.wait()
}

// returned reports whether Run has already returned, without consuming its result.
func (r *running) returned() bool {
	select {
	case err := <-r.done:
		r.done <- err
		return true
	default:
		return false
	}
}

// startWith runs the service (on the listener listen makes, when not nil) and
// waits for its api.json. The cleanup stops it, waits, so Run is gone before
// the temp dir is removed, and requires a clean stop unless expectErr is set.
func startWith(t *testing.T, opts service.Options, listen func(context.Context) (net.Listener, error)) *running {
	t.Helper()
	if opts.Version == "" {
		opts.Version = "9.9.9"
	}
	if opts.ContractVersion == 0 {
		opts.ContractVersion = 1
	}
	if opts.ShutdownTimeout == 0 {
		opts.ShutdownTimeout = 2 * time.Second // below wait()'s 5 s
	}
	r := &running{done: make(chan error, 1), logs: new(logBuffer)}
	begin := time.Now()
	opts.Logger = slog.New(slog.NewTextHandler(r.logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go func() {
		if listen != nil {
			r.done <- service.RunWithListen(ctx, opts, listen)
			return
		}
		r.done <- service.Run(ctx, opts)
	}()
	t.Cleanup(func() {
		if err := r.stop(); err != nil && !r.expectErr {
			t.Errorf("stop: %v", err)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r.returned() {
			r.expectErr = true // reported here, not again by the cleanup
			t.Fatalf("Run returned early: %v", r.wait())
		}
		if raw, err := os.ReadFile(filepath.Join(opts.Dir, "api.json")); err == nil {
			// This instance's file, not one an earlier Run of this test binary (same
			// pid) or the test itself left: it was written after this start began.
			if json.Unmarshal(raw, &r.info) == nil && r.info.Port != 0 && !r.info.StartedAt.Before(begin) {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("api.json did not appear")
	return nil
}

func start(t *testing.T, dir string) *running {
	t.Helper()
	return startWith(t, service.Options{Dir: dir}, nil)
}

func (r *running) url(path string) string {
	return "http://127.0.0.1:" + strconv.Itoa(r.info.Port) + path
}

func (r *running) get(t *testing.T, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, r.url("/api/v1/health"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// holdConnection opens a connection whose request headers never complete and
// returns once the server tracks it. net/http keeps such a connection in
// StateNew and Shutdown waits for it while it is younger than 5 s (older ones
// count as idle, net/http issue 22682): the shutdown bounds in these tests stay
// below that.
func (r *running) holdConnection(t *testing.T) {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", "127.0.0.1:"+strconv.Itoa(r.info.Port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.Write([]byte("GET /api/v1/health HTTP/1.1\r\nHost: 127.0.0.1\r\n")); err != nil {
		t.Fatal(err)
	}
	// The accept loop is sequential and tracks a connection before accepting the
	// next, so a full request answered on a new connection proves this one is
	// tracked. No sleep, no timing guess.
	if resp := r.get(t, r.info.Token); resp.StatusCode != http.StatusOK {
		t.Fatalf("sync request = %d", resp.StatusCode)
	}
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

	if err := r.stop(); err != nil {
		t.Fatalf("Run after cancel: %v", err)
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
	err := service.Run(t.Context(), service.Options{Dir: dir, Version: "x", ContractVersion: 1})
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
	if r := start(t, dir); r.info.Token == "stale" || r.info.Port == 1 {
		t.Fatalf("the stale api.json was not replaced: %+v", r.info)
	}
}

func TestRunCleansLeftoversAtStart(t *testing.T) {
	t.Parallel()
	// A glob metacharacter in the path must not stop the cleanup.
	dir := filepath.Join(t.TempDir(), "x[1]")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
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
	logs := new(logBuffer)
	if err := service.Run(ctx, service.Options{Dir: dir, Version: "x", ContractVersion: 1, Logger: slog.New(slog.NewTextHandler(logs, nil))}); err != nil {
		t.Fatalf("Run with an already cancelled context = %v; want nil", err)
	}
	if strings.Contains(logs.String(), "Service listening") {
		t.Errorf("a service stopped before it started was advertised:\n%s", logs)
	}
	if _, err := os.Stat(filepath.Join(dir, "api.json")); !os.IsNotExist(err) {
		t.Errorf("api.json survived: %v", err)
	}
}

// A stop is a stop: neither a connection that outlasts the shutdown bound nor an
// api.json that cannot be deleted turns it into a failure (a supervisor would
// read exit 1 as a crash; api.json is only a hint). Both are logged.
func TestRunStopSucceedsWhenShutdownTimesOut(t *testing.T) {
	t.Parallel()
	r := startWith(t, service.Options{Dir: t.TempDir(), ShutdownTimeout: 100 * time.Millisecond}, nil)
	r.holdConnection(t)
	if err := r.stop(); err != nil {
		t.Fatalf("Run after a stop that hit the shutdown bound = %v; want nil", err)
	}
	if !strings.Contains(r.logs.String(), "Shutdown bound reached") {
		t.Errorf("the stop did not hit the bound, or did not log it:\n%s", r.logs)
	}
}

func TestRunStopSucceedsWhenInfoCannotBeDeleted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := startWith(t, service.Options{Dir: dir}, nil)
	// Replace api.json by a non-empty folder: os.Remove fails on it.
	path := filepath.Join(dir, "api.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := r.stop(); err != nil {
		t.Fatalf("Run with an undeletable api.json = %v; want nil", err)
	}
	// One warning for one failure, at warn (serve's default level), with its cause.
	logs := r.logs.String()
	if n := strings.Count(logs, "level=WARN"); n != 1 || !strings.Contains(logs, `level=WARN msg="Cannot delete api.json" error="remove api.json`) {
		t.Errorf("want exactly one WARN line for the failed delete, with its cause; got %d:\n%s", n, logs)
	}
}

// api.json goes first on a stop: while the shutdown waits for a stuck connection
// the service must not keep advertising a port and token that are going away.
func TestRunDeletesInfoBeforeWaitingForConnections(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The bound (3 s) is well above the polling window (1 s) and leaves 2 s below
	// net/http's 5 s StateNew rule (see holdConnection).
	r := startWith(t, service.Options{Dir: dir, ShutdownTimeout: 3 * time.Second}, nil)
	r.holdConnection(t)
	r.cancel()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "api.json")); os.IsNotExist(err) {
			if r.returned() {
				t.Fatalf("Run returned (%v): the connection should still be holding the shutdown", r.wait())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("api.json still advertised while the shutdown waits for a connection")
}

// brokenListener is a real loopback listener whose Accept fails for good once
// the test says so, as a listener the OS takes away would. Close also ends a
// blocked Accept, so a test that fails early leaks no Serve goroutine.
type brokenListener struct {
	net.Listener
	broken    chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func (l *brokenListener) Accept() (net.Conn, error) {
	select {
	case <-l.broken:
		return nil, errors.New("listener broke")
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *brokenListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return l.Listener.Close() //nolint:wrapcheck // test listener
}

func TestRunListenerFailureCleansUp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	broken := make(chan struct{})
	r := startWith(t, service.Options{Dir: dir}, func(ctx context.Context) (net.Listener, error) {
		l, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err //nolint:wrapcheck // test listener
		}
		return &brokenListener{Listener: l, broken: broken, closed: make(chan struct{})}, nil
	})
	r.expectErr = true
	close(broken)
	if err := r.wait(); err == nil || !strings.Contains(err.Error(), "listener broke") {
		t.Fatalf("Run after the listener broke = %v; want the listener error", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "api.json")); !os.IsNotExist(err) {
		t.Errorf("api.json survived a dead listener: %v", err)
	}
	if l, err := service.AcquireLock(dir); err != nil {
		t.Errorf("the lock was not released: %v", err)
	} else {
		_ = l.Release()
	}
}

func TestHTTPServerConfiguration(t *testing.T) {
	t.Parallel()
	logs := new(logBuffer)
	srv := service.NewHTTPServer(t.Context(), http.NotFoundHandler(), slog.New(slog.NewTextHandler(logs, nil)))
	if srv.MaxHeaderBytes != 64<<10 {
		t.Errorf("MaxHeaderBytes = %d; want 64 KiB", srv.MaxHeaderBytes)
	}
	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.IdleTimeout == 0 {
		t.Errorf("timeouts not set: header %v, read %v, idle %v", srv.ReadHeaderTimeout, srv.ReadTimeout, srv.IdleTimeout)
	}
	// net/http writes handler panics and accept errors only through ErrorLog: they
	// must reach the service's logger at a level shown by default (warn).
	srv.ErrorLog.Print("http: panic serving 127.0.0.1:1: boom")
	if got := logs.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, "panic serving") {
		t.Errorf("ErrorLog output = %q; want a WARN line on the service logger", got)
	}
}

func TestRunStopIgnoresMissingConfigFolder(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not remove a folder holding an open (locked) file")
	}
	dir := filepath.Join(t.TempDir(), "cfg")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r := startWith(t, service.Options{Dir: dir}, nil)
	// The user (or an uninstaller) removes the folder while the service runs.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := r.stop(); err != nil {
		t.Fatal(err)
	}
	if logs := r.logs.String(); strings.Contains(logs, "Cannot delete") {
		t.Errorf("a folder that is gone is nothing to clean up:\n%s", logs)
	}
}

// closeRecorder is a real listener that records whether it was closed.
type closeRecorder struct {
	net.Listener
	closed atomic.Bool
}

func (l *closeRecorder) Close() error {
	l.closed.Store(true)
	return l.Listener.Close() //nolint:wrapcheck // test listener
}

// Run returns only after Serve let the listener go, also when the stop came
// before Serve was fully up: no goroutine and no bound port outlive Run.
func TestRunReleasesListenerBeforeReturning(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var rec *closeRecorder
	err := service.RunWithListen(ctx, service.Options{Dir: t.TempDir(), Version: "x", ContractVersion: 1}, func(ctx context.Context) (net.Listener, error) {
		l, err := new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err //nolint:wrapcheck // test listener
		}
		rec = &closeRecorder{Listener: l}
		return rec, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.closed.Load() {
		t.Fatal("Run returned before the listener was closed")
	}
}

// A leftover that cannot be removed does not stop the start, but is logged.
func TestRunLogsLeftoversItCannotRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A non-empty folder named like a temp file: os.Remove fails on it.
	if err := os.MkdirAll(filepath.Join(dir, "api.json.1.tmp", "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := startWith(t, service.Options{Dir: dir}, nil)
	if logs := r.logs.String(); !strings.Contains(logs, "Cannot clean up the files of an earlier service") || !strings.Contains(logs, "remove leftover") {
		t.Errorf("the failed cleanup was not logged with its cause:\n%s", logs)
	}
}

func TestShutdownEndpointStopsRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := start(t, dir)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, r.url("/api/v1/shutdown"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+r.info.Token)
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d; want 202", resp.StatusCode)
	}
	if err := r.wait(); err != nil {
		t.Fatalf("Run after shutdown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "api.json")); !os.IsNotExist(err) {
		t.Errorf("api.json survived: %v", err)
	}
}
