package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// StopStatus is the outcome of Stop.
type StopStatus string

// The three outcomes a planned stop can have.
const (
	StopStopped    StopStatus = "stopped"     // a service was running and the lock is now free
	StopNotRunning StopStatus = "not_running" // nobody held the lock: nothing to stop
	StopTimeout    StopStatus = "timeout"     // the lock was still held when the time ran out
)

// DefaultStopTimeout is a little above the service's own 10 s stop bound.
const DefaultStopTimeout = 15 * time.Second

const stopPoll = 50 * time.Millisecond

// StopOptions configures Stop.
type StopOptions struct {
	Dir     string        // the config dir
	Timeout time.Duration // zero means DefaultStopTimeout
}

// Stop asks the running service to stop through the API and waits until its
// lock is free, the same on every OS (#286): no signal is involved. The lock,
// not api.json, says whether a service exists: a free lock is "not running"
// (a stale api.json is not even read). While the lock is held without a usable
// api.json the service is starting or already stopping, so Stop keeps trying
// until the time is up. The token read from api.json goes only to
// 127.0.0.1:<port>, never to a host taken from the file.
func Stop(ctx context.Context, opts StopOptions) (StopStatus, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultStopTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// No config folder: no service ever ran for this user; do not create one.
	if _, err := os.Stat(opts.Dir); errors.Is(err, os.ErrNotExist) {
		return StopNotRunning, nil
	}
	free, err := lockIsFree(opts.Dir)
	if err != nil {
		return "", err
	}
	if free {
		return StopNotRunning, nil
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 5 * time.Second}
	// The token of the instance that accepted: a new instance that takes the lock
	// meanwhile has another token in api.json, and gets the shutdown too.
	accepted := ""
	ticker := time.NewTicker(stopPoll)
	defer ticker.Stop()
	for {
		if token := requestShutdown(ctx, client, opts.Dir, accepted); token != "" {
			accepted = token
		}
		select {
		case <-ctx.Done():
			return StopTimeout, nil
		case <-ticker.C:
		}
		free, err := lockIsFree(opts.Dir)
		if err != nil {
			return "", err
		}
		if free {
			return StopStopped, nil
		}
	}
}

// lockIsFree takes the lock and gives it back at once.
func lockIsFree(dir string) (bool, error) {
	lock, err := AcquireLock(dir)
	if errors.Is(err, ErrAlreadyRunning) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, lock.Release()
}

// requestShutdown reads api.json and posts the shutdown to the instance it names,
// unless that instance (its token) already accepted one. It returns the token of
// an instance that accepted now, "" otherwise (try again later).
func requestShutdown(ctx context.Context, client *http.Client, dir, accepted string) string {
	raw, err := os.ReadFile(filepath.Join(dir, infoName)) //nolint:gosec // dir is the core's own config folder
	if err != nil {
		return ""
	}
	var info Info
	if json.Unmarshal(raw, &info) != nil || info.Port < 1 || info.Port > 65535 || info.Token == "" || info.Token == accepted {
		return ""
	}
	url := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(info.Port)) + "/api/v1/shutdown"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+info.Token)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		return ""
	}
	return info.Token
}
