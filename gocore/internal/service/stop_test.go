package service_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/service"
)

func stopWith(t *testing.T, dir string, timeout time.Duration) service.StopStatus {
	t.Helper()
	status, err := service.Stop(t.Context(), service.StopOptions{Dir: dir, Timeout: timeout})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	return status
}

// fakeService answers like a service that accepted the shutdown but never lets go of the lock.
func fakeService(t *testing.T, dir, token string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/shutdown" && r.Header.Get("Authorization") == "Bearer "+token {
			calls.Add(1)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	port := srv.Listener.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // a tcp listener
	if err := service.WriteInfo(dir, service.Info{SchemaVersion: 1, Port: port, Token: token, Version: "9.9.9", ContractVersion: 1, PID: 1, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return &calls
}

func TestStopWithNothingRunning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got := stopWith(t, dir, time.Second); got != service.StopNotRunning {
		t.Fatalf("status = %q; want %q", got, service.StopNotRunning)
	}
}

// A crash leaves api.json behind: with the lock free there is no service, and the file is not trusted.
func TestStopIgnoresAStaleAPIFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	calls := fakeService(t, dir, "stale")
	if got := stopWith(t, dir, time.Second); got != service.StopNotRunning {
		t.Fatalf("status = %q; want %q", got, service.StopNotRunning)
	}
	if calls.Load() != 0 {
		t.Error("Stop sent the shutdown to whatever the stale file points at")
	}
}

func TestStopStopsARunningService(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := start(t, dir)
	if got := stopWith(t, dir, 5*time.Second); got != service.StopStopped {
		t.Fatalf("status = %q; want %q", got, service.StopStopped)
	}
	if !r.returned() {
		t.Error("Stop reported stopped while Run was still going")
	}
	if err := r.wait(); err != nil {
		t.Errorf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "api.json")); !os.IsNotExist(err) {
		t.Errorf("api.json survived: %v", err)
	}
	lock, err := service.AcquireLock(dir)
	if err != nil {
		t.Fatalf("the lock is not free after Stop: %v", err)
	}
	_ = lock.Release()
	// Nothing left to stop.
	if got := stopWith(t, dir, time.Second); got != service.StopNotRunning {
		t.Errorf("second Stop = %q; want %q", got, service.StopNotRunning)
	}
}

func TestStopTimesOutWhenTheLockIsNeverFreed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock, err := service.AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	calls := fakeService(t, dir, "tok")
	begin := time.Now()
	if got := stopWith(t, dir, 300*time.Millisecond); got != service.StopTimeout {
		t.Fatalf("status = %q; want %q", got, service.StopTimeout)
	}
	if calls.Load() == 0 {
		t.Error("the shutdown was never sent")
	}
	if elapsed := time.Since(begin); elapsed > 3*time.Second {
		t.Errorf("Stop took %v; the timeout is a bound", elapsed)
	}
}

// Lock held and no api.json: a service starting or already stopping. Stop waits for the lock.
func TestStopWaitsForAServiceThatIsAlreadyStopping(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock, err := service.AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	time.AfterFunc(150*time.Millisecond, func() { _ = lock.Release() })
	if got := stopWith(t, dir, 5*time.Second); got != service.StopStopped {
		t.Fatalf("status = %q; want %q", got, service.StopStopped)
	}
}

// A service that starts while Stop waits (api.json appears late) still gets the shutdown.
func TestStopRetriesUntilAPIFileAppears(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock, err := service.AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	time.AfterFunc(150*time.Millisecond, func() {
		calls := fakeService(t, dir, "late")
		go func() {
			for calls.Load() == 0 {
				time.Sleep(5 * time.Millisecond)
			}
			_ = lock.Release()
		}()
	})
	if got := stopWith(t, dir, 5*time.Second); got != service.StopStopped {
		t.Fatalf("status = %q; want %q", got, service.StopStopped)
	}
}

func TestStopOnlyTalksToLoopback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock, err := service.AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	// A hand-edited file with a port outside the range must not become a URL.
	if err := service.WriteInfo(dir, service.Info{SchemaVersion: 1, Port: 70000, Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if got := stopWith(t, dir, 200*time.Millisecond); got != service.StopTimeout {
		t.Fatalf("status = %q; want %q", got, service.StopTimeout)
	}
}
