package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/security/secrets"
)

// Profile says which home node this machine talks to and where its credential
// lives. It never contains the credential, only a secrets reference to it, so
// the file is safe to back up and to read in a bug report.
type Profile struct {
	URL       string    `json:"url"`
	TokenRef  string    `json:"token_ref"` // keychain://…, file://ns/key, file:///abs/path, op://…
	DeviceID  string    `json:"device_id,omitempty"`
	Scopes    []string  `json:"scopes,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	AllowHTTP bool      `json:"allow_http,omitempty"`
}

// ProfilePath is where the profile lives.
func ProfilePath(configDir string) string { return filepath.Join(configDir, "remote.json") }

// LoadProfile reads the saved profile, or returns nil if there is none.
func LoadProfile(configDir string) (*Profile, error) {
	b, err := os.ReadFile(ProfilePath(configDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", ProfilePath(configDir), err)
	}
	if p.URL == "" || p.TokenRef == "" {
		return nil, fmt.Errorf("%s needs url and token_ref", ProfilePath(configDir))
	}
	return &p, nil
}

// SaveProfile writes the profile atomically with mode 0600.
func SaveProfile(configDir string, p Profile) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(configDir, ".remote-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), ProfilePath(configDir))
}

// RemoveProfile deletes the saved profile.
func RemoveProfile(configDir string) error {
	err := os.Remove(ProfilePath(configDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Active decides whether this process should use a remote home node. The
// environment wins over the saved profile; CASTER_LOCAL=1 forces the local
// store even when a profile exists (what you want on the home node itself).
//
//	CASTER_REMOTE       home node URL; needs CASTER_TOKEN_REF
//	CASTER_TOKEN_REF    secrets reference to the device token
//	CASTER_ALLOW_HTTP   1 = accept plain http off loopback/tailnet
//	CASTER_LOCAL        1 = ignore any remote configuration
func Active(configDir string, getenv func(string) string) (*Profile, error) {
	if getenv("CASTER_LOCAL") == "1" {
		return nil, nil
	}
	if u := getenv("CASTER_REMOTE"); u != "" {
		ref := getenv("CASTER_TOKEN_REF")
		if ref == "" {
			return nil, errors.New("CASTER_REMOTE is set but CASTER_TOKEN_REF is not (a secrets reference such as keychain://caster/home)")
		}
		return &Profile{URL: u, TokenRef: ref, AllowHTTP: getenv("CASTER_ALLOW_HTTP") == "1"}, nil
	}
	p, err := LoadProfile(configDir)
	if p != nil && getenv("CASTER_ALLOW_HTTP") == "1" {
		p.AllowHTTP = true
	}
	return p, err
}

// SecretsTokenSource resolves ref with the secrets lib. The value is cached
// briefly so one command does not hit the keychain for every request, but a
// rotated token is picked up within the minute.
func SecretsTokenSource(ref string) TokenSource {
	var (
		mu      sync.Mutex
		val     string
		fetched time.Time
	)
	r := secrets.New()
	return func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if val != "" && time.Since(fetched) < time.Minute {
			return val, nil
		}
		res, err := r.Resolve(ctx, ref)
		if err != nil {
			return "", err // secrets errors never contain the value
		}
		val, fetched = strings.TrimSpace(res.Secret.Reveal()), time.Now()
		return val, nil
	}
}

// StoreToken saves a device token under a secrets reference. With ref empty it
// prefers the OS keychain and falls back to the managed file store (0600 under
// the state dir) where no keychain exists, as on headless Linux. It returns the
// reference used.
func StoreToken(ctx context.Context, name, ref, token string) (string, error) {
	r := secrets.New()
	if ref != "" {
		return ref, r.Set(ctx, ref, strings.NewReader(token))
	}
	ref = "keychain://caster/" + name
	err := r.Set(ctx, ref, strings.NewReader(token))
	if err == nil {
		return ref, nil
	}
	if !errors.Is(err, secrets.ErrUnavailable) {
		return "", err
	}
	ref = "file://caster/" + name
	return ref, r.Set(ctx, ref, strings.NewReader(token))
}

// DeleteToken removes a stored token.
func DeleteToken(ctx context.Context, ref string) error { return secrets.New().Delete(ctx, ref) }
