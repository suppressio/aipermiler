// Package secrets keeps provider credentials in the OS keyring (Windows Credential
// Manager, macOS Keychain, Secret Service on Linux), decided on #179: the core owns
// the Copilot token, so it never crosses argv, the JSON documents or the Electron
// store. There is no file fallback: an unavailable keyring is an explicit error.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"

	"github.com/zalando/go-keyring"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
)

// ErrNotFound means no secret is stored under the key (account not connected).
var ErrNotFound = errors.New("secret not found")

// Store reads and writes secrets by key.
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	// Delete removes the secret; deleting a missing one is not an error.
	Delete(key string) error
}

var accountID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// ValidAccount reports whether id can name a keyring entry: the Electron account ids
// ("copilot", "copilot-1a2b3c4d") and nothing that could address another entry.
func ValidAccount(id string) bool { return accountID.MatchString(id) }

// CopilotKey is the keyring entry of a Copilot account's token.
func CopilotKey(account string) string { return "copilot/" + account }

// Get reads key within ctx. The keyring calls take no context and can wait on an
// unlock prompt with no end, so the call runs aside and the caller stops waiting at the
// deadline. The pending call is not cancelled: in-process backends (Secret Service,
// Credential Manager) end with the CLI process, but on macOS go-keyring runs
// /usr/bin/security, which can outlive it and complete once the keychain is unlocked.
// Accepted: that late completion is exactly the write or delete that was asked for, of
// the same value, never another one; the caller saw a timeout and retries. Cancelling
// it would need a context in go-keyring (a fork, or another dependency).
func Get(ctx context.Context, store Store, key string) (string, error) {
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := store.Get(key)
		done <- result{value, err}
	}()
	select {
	case r := <-done:
		return r.value, r.err
	case <-ctx.Done():
		return "", fmt.Errorf("OS keyring did not answer: %w", ctx.Err())
	}
}

// Set writes key within ctx (see Get).
func Set(ctx context.Context, store Store, key, value string) error {
	return within(ctx, func() error { return store.Set(key, value) })
}

// Delete removes key within ctx (see Get).
func Delete(ctx context.Context, store Store, key string) error {
	return within(ctx, func() error { return store.Delete(key) })
}

func within(ctx context.Context, call func() error) error {
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("OS keyring did not answer: %w", ctx.Err())
	}
}

// LegacyService is the keyring service of the core before the AIpermiler rename
// (#200); Migrating still reads its entries and moves them on the next write.
const LegacyService = "hypermiler"

// OSKeyring is the real store, under the service name of the binary, migrating
// entries left under LegacyService.
type OSKeyring struct{}

func (OSKeyring) store() Migrating {
	return Migrating{Current: serviceKeyring(buildinfo.Name), Legacy: serviceKeyring(LegacyService)}
}

// Get reads a secret; ErrNotFound when there is none.
func (k OSKeyring) Get(key string) (string, error) { return k.store().Get(key) }

// Set stores a secret, replacing any previous value.
func (k OSKeyring) Set(key, value string) error { return k.store().Set(key, value) }

// Delete removes a secret; a missing one is fine.
func (k OSKeyring) Delete(key string) error { return k.store().Delete(key) }

// serviceKeyring is the OS keyring under one service name.
type serviceKeyring string

func (s serviceKeyring) Get(key string) (string, error) {
	value, err := keyring.Get(string(s), key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("OS keyring: %w", err)
	}
	return value, nil
}

func (s serviceKeyring) Set(key, value string) error {
	if err := keyring.Set(string(s), key, value); err != nil {
		return fmt.Errorf("OS keyring: %w", err)
	}
	return nil
}

func (s serviceKeyring) Delete(key string) error {
	err := keyring.Delete(string(s), key)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("OS keyring: %w", err)
	}
	return nil
}

// Migrating reads Current, falling back to Legacy, and never writes on a read: a read
// running while the same secret is deleted (refresh or diagnostics against disconnect)
// could otherwise write it back. A secret left in Legacy moves when it is next set
// (reconnect: Set writes Current and clears Legacy) and goes when it is deleted.
type Migrating struct {
	Current Store
	Legacy  Store
}

// Get reads Current, then Legacy.
func (m Migrating) Get(key string) (string, error) {
	value, err := m.Current.Get(key)
	if !errors.Is(err, ErrNotFound) {
		return value, err
	}
	return m.Legacy.Get(key)
}

// Set writes Current, then removes any Legacy copy (best-effort: Current wins on read).
func (m Migrating) Set(key, value string) error {
	if err := m.Current.Set(key, value); err != nil {
		return err
	}
	_ = m.Legacy.Delete(key)
	return nil
}

// Delete removes key from Current and Legacy.
func (m Migrating) Delete(key string) error {
	if err := m.Current.Delete(key); err != nil {
		return err
	}
	return m.Legacy.Delete(key)
}

// Memory is an in-process store for tests.
type Memory struct {
	mu     sync.Mutex
	values map[string]string
	// Err, when set, is returned by every call (an unavailable keyring).
	Err error
}

// Get reads a secret; ErrNotFound when there is none.
func (m *Memory) Get(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return "", m.Err
	}
	value, ok := m.values[key]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

// Set stores a secret.
func (m *Memory) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[key] = value
	return nil
}

// Delete removes a secret.
func (m *Memory) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	delete(m.values, key)
	return nil
}
