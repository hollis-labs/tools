package service

import (
	"context"
	"time"
)

// WhoamiResult is how the service sees the caller.
type WhoamiResult struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind,omitempty"`
	Label     string     `json:"label,omitempty"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Identity is the domain service for who the caller is. Accounts, keys and sessions belong to the
// identity module; this only reports what the verified caller holds.
type Identity struct{}

// Whoami reports the caller's identity, scopes and expiry. Any verified caller may ask: it reveals
// nothing the caller does not already hold.
func (Identity) Whoami(ctx context.Context, _ struct{}) (WhoamiResult, error) {
	p, ok := caller(ctx)
	if !ok {
		return WhoamiResult{}, require(ctx)
	}
	out := WhoamiResult{ID: p.ID, Kind: p.Kind, Label: p.Display, Scopes: append([]string{}, p.Scopes...)}
	if !p.ExpiresAt.IsZero() {
		t := p.ExpiresAt
		out.ExpiresAt = &t
	}
	return out, nil
}
