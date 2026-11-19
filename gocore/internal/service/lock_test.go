package service_test

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/service"
)

const holderEnv = "AIPERMILER_TEST_LOCK_HOLDER"

// TestMain lets the test binary double as a process that holds the lock, so the
// kill -9 case runs against a real process death and not a simulated release.
func TestMain(m *testing.M) {
	if dir := os.Getenv(holderEnv); dir != "" {
		if _, err := service.AcquireLock(dir); err != nil {
			os.Exit(3)
		}
		_, _ = os.Stdout.WriteString("locked\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestAcquireLockIsExclusive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, err := service.AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcquireLock(dir); !errors.Is(err, service.ErrAlreadyRunning) {
		t.Fatalf("second AcquireLock error = %v; want ErrAlreadyRunning", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := service.AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock after Release: %v", err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
}

// Serial on purpose: the fork below briefly gives the child a copy of every open
// descriptor, and a flock lives as long as any copy does. Run in parallel with the
// other lock tests, a Release in one of them could still see its lock held by that
// copy (found with -race -count=40). Serial tests never overlap parallel ones.
func TestAcquireLockSurvivesKilledHolder(t *testing.T) { //nolint:paralleltest // see above
	if runtime.GOOS == "windows" {
		t.Skip("covered by the manual check on the Windows 11 VM (#247)")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), holderEnv+"="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("holder did not lock: %q, %v", line, err)
	}
	if _, err := service.AcquireLock(dir); !errors.Is(err, service.ErrAlreadyRunning) {
		t.Fatalf("AcquireLock with a live holder = %v; want ErrAlreadyRunning", err)
	}
	if err := cmd.Process.Kill(); err != nil { // SIGKILL: no deferred cleanup runs
		t.Fatal(err)
	}
	_ = cmd.Wait()
	l, err := service.AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock after kill -9: %v", err)
	}
	_ = l.Release()
}
