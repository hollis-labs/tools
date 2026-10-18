package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tools/caster/internal/store"
)

// Pairing limits, following Tether's device pairing: a short-lived one-use code
// is exchanged once for a long-lived, renewable, revocable device token.
const (
	DefaultGrantTTL = 5 * time.Minute
	MaxGrantTTL     = time.Hour
	DeviceLifetime  = 30 * 24 * time.Hour

	tokenPrefix = "cst_"
	codePrefix  = "ccg_"
	devicePfx   = "device/"
)

// Access is the domain service for who may call caster: pairing grants, device
// tokens, and the verifier that turns a presented token into an identity.
// Codes and tokens carry 256 random bits and only their SHA-256 is stored.
type Access struct {
	store *store.Store
	now   func() time.Time
}

func secret(prefix string) (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	v := prefix + base64.RawURLEncoding.EncodeToString(b)
	return v, hashOf(v), nil
}

func hashOf(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }

func newID(prefix string) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

// NormalizeScopes accepts "read" or "caster.read" and returns canonical,
// de-duplicated scopes, rejecting anything outside the vocabulary.
func NormalizeScopes(in []string) ([]string, error) {
	known := map[string]bool{}
	for _, s := range AllScopes {
		known[s] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		for _, s := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
			if !strings.Contains(s, ".") {
				s = "caster." + s
			}
			if !known[s] {
				return nil, invalid(fmt.Errorf("unknown scope %q (have: %s)", s, strings.Join(AllScopes, ", ")))
			}
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// PairRequest asks for a one-use grant.
type PairRequest struct {
	Label      string   `json:"label,omitempty"`
	Scopes     []string `json:"scopes"`
	TTLSeconds int      `json:"ttl_seconds,omitempty"`
}

// PairResult carries the code once; it cannot be recovered later.
type PairResult struct {
	GrantID   string    `json:"grant_id"`
	Code      string    `json:"code"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreateGrant mints a pairing grant. It is a local-operator act: a paired
// device, even an admin one, cannot create grants for others, as in Tether.
func (a *Access) CreateGrant(ctx context.Context, req PairRequest) (PairResult, error) {
	if err := require(ctx, ScopeAdmin); err != nil {
		return PairResult{}, err
	}
	if p, _ := caller(ctx); p.ID != LocalOperatorID {
		return PairResult{}, &Error{KindDenied, errors.New("pairing grants are created at the local terminal only")}
	}
	scopes, err := NormalizeScopes(req.Scopes)
	if err != nil {
		return PairResult{}, err
	}
	if len(scopes) == 0 {
		return PairResult{}, invalid(errors.New("at least one scope is required"))
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = DefaultGrantTTL
	}
	if ttl < 0 || ttl > MaxGrantTTL {
		return PairResult{}, invalid(fmt.Errorf("ttl must be between 1s and %s", MaxGrantTTL))
	}
	code, hash, err := secret(codePrefix)
	if err != nil {
		return PairResult{}, classify(err)
	}
	id, err := newID("grt_")
	if err != nil {
		return PairResult{}, classify(err)
	}
	now := a.now()
	g := store.Grant{ID: id, Label: req.Label, Scopes: scopes, ExpiresAt: now.Add(ttl)}
	if err := a.store.CreateGrant(g, hash, now); err != nil {
		return PairResult{}, classify(err)
	}
	return PairResult{GrantID: id, Code: code, Scopes: scopes, ExpiresAt: g.ExpiresAt}, nil
}

// ExchangeRequest redeems a grant code. Scopes, if given, may only narrow it.
type ExchangeRequest struct {
	Code   string   `json:"code"`
	Scopes []string `json:"scopes,omitempty"`
	Label  string   `json:"label,omitempty"`
}

// ExchangeResult carries the device token once.
type ExchangeResult struct {
	DeviceID  string    `json:"device_id"`
	Token     string    `json:"token"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at"`
}

// errRefused is the single answer for every failed exchange.
func errRefused() error {
	return &Error{KindUnauthenticated, errors.New("the code is invalid, expired or already used")}
}

// Exchange redeems a code for a device token. It is deliberately open: the
// code is the credential. Every failure looks the same.
func (a *Access) Exchange(_ context.Context, req ExchangeRequest) (ExchangeResult, error) {
	if !strings.HasPrefix(req.Code, codePrefix) || len(req.Code) > 128 {
		return ExchangeResult{}, errRefused()
	}
	var want []string
	if len(req.Scopes) > 0 {
		var err error
		if want, err = NormalizeScopes(req.Scopes); err != nil {
			return ExchangeResult{}, errRefused() // an unknown scope is just another refusal
		}
	}
	tok, tokHash, err := secret(tokenPrefix)
	if err != nil {
		return ExchangeResult{}, classify(err)
	}
	id, err := newID("dev_")
	if err != nil {
		return ExchangeResult{}, classify(err)
	}
	now := a.now()
	d, err := a.store.ExchangeGrant(hashOf(req.Code), id, tokHash, req.Label, want, now.Add(DeviceLifetime), now)
	if errors.Is(err, store.ErrGrantRefused) {
		return ExchangeResult{}, errRefused()
	}
	if err != nil {
		return ExchangeResult{}, classify(err)
	}
	return ExchangeResult{DeviceID: d.ID, Token: tok, Scopes: d.Scopes, ExpiresAt: d.ExpiresAt}, nil
}

// RenewRequest extends the calling device. Scopes, if given, may only narrow.
type RenewRequest struct {
	Scopes []string `json:"scopes,omitempty"`
}

// DeviceInfo is a device without its token or its hash.
type DeviceInfo struct {
	ID         string     `json:"id"`
	Label      string     `json:"label,omitempty"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Revoked    bool       `json:"revoked"`
}

func infoOf(d store.Device) DeviceInfo {
	i := DeviceInfo{ID: d.ID, Label: d.Label, Scopes: d.Scopes, CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt, Revoked: d.Revoked}
	if !d.LastUsedAt.IsZero() {
		t := d.LastUsedAt
		i.LastUsedAt = &t
	}
	return i
}

// Renew extends the calling device's lifetime by DeviceLifetime from now,
// keeping its token. Only a device can renew itself; scopes can only narrow.
func (a *Access) Renew(ctx context.Context, req RenewRequest) (DeviceInfo, error) {
	p, ok := caller(ctx)
	if !ok {
		return DeviceInfo{}, require(ctx)
	}
	id, isDevice := strings.CutPrefix(p.ID, devicePfx)
	if p.Kind != auth.KindDevice || !isDevice {
		return DeviceInfo{}, &Error{KindDenied, errors.New("only a paired device can renew itself")}
	}
	var narrow []string
	if len(req.Scopes) > 0 {
		var err error
		if narrow, err = NormalizeScopes(req.Scopes); err != nil {
			return DeviceInfo{}, err
		}
	}
	now := a.now()
	d, err := a.store.RenewDevice(id, narrow, now.Add(DeviceLifetime), now)
	if errors.Is(err, store.ErrGrantRefused) {
		return DeviceInfo{}, &Error{KindDenied, errors.New("renewal may only narrow scopes")}
	}
	if err != nil {
		return DeviceInfo{}, classify(err)
	}
	return infoOf(d), nil
}

// DevicesResult lists paired devices.
type DevicesResult struct {
	Devices []DeviceInfo `json:"devices"`
}

// Devices lists paired devices.
func (a *Access) Devices(ctx context.Context, _ struct{}) (DevicesResult, error) {
	if err := require(ctx, ScopeAdmin); err != nil {
		return DevicesResult{}, err
	}
	ds, err := a.store.Devices()
	if err != nil {
		return DevicesResult{}, classify(err)
	}
	out := DevicesResult{Devices: make([]DeviceInfo, 0, len(ds))}
	for _, d := range ds {
		out.Devices = append(out.Devices, infoOf(d))
	}
	return out, nil
}

// RevokeRequest names a device (dev_…) or an unused grant (grt_…).
type RevokeRequest struct {
	ID string `json:"id"`
}

// RevokeResult confirms a revocation.
type RevokeResult struct {
	Revoked string `json:"revoked"`
}

// Revoke revokes a device or an unconsumed grant. A revoked device stops
// authenticating on its next request.
func (a *Access) Revoke(ctx context.Context, req RevokeRequest) (RevokeResult, error) {
	if err := require(ctx, ScopeAdmin); err != nil {
		return RevokeResult{}, err
	}
	var err error
	switch {
	case strings.HasPrefix(req.ID, "dev_"):
		err = a.store.RevokeDevice(req.ID, a.now())
	case strings.HasPrefix(req.ID, "grt_"):
		err = a.store.RevokeGrant(req.ID, a.now())
	default:
		return RevokeResult{}, invalid(errors.New("id must be a device (dev_…) or grant (grt_…) id"))
	}
	if err != nil {
		return RevokeResult{}, classify(err)
	}
	return RevokeResult{Revoked: req.ID}, nil
}

// Verifier returns the go-auth verifier for device tokens, for the composition
// root to put in its chain.
func (a *Access) Verifier() auth.Verifier { return deviceVerifier{a} }

type deviceVerifier struct{ a *Access }

func (v deviceVerifier) Verify(_ context.Context, token string) (auth.Principal, error) {
	if !strings.HasPrefix(token, tokenPrefix) || len(token) > 128 {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	d, err := v.a.store.DeviceByToken(hashOf(token), v.a.now())
	if errors.Is(err, store.ErrNotFound) {
		return auth.Principal{}, auth.ErrInvalidToken
	}
	if err != nil {
		return auth.Principal{}, auth.ErrUnavailable
	}
	return auth.Principal{Verified: true, ID: devicePfx + d.ID, Kind: auth.KindDevice, Display: d.Label,
		Audience: "caster", Scopes: d.Scopes, ExpiresAt: d.ExpiresAt}, nil
}

// WhoamiResult is how the service sees the caller.
type WhoamiResult struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind,omitempty"`
	Label     string     `json:"label,omitempty"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Whoami reports the caller's identity, scopes and expiry. Any verified caller
// may ask: it reveals nothing the caller does not already hold.
func (a *Access) Whoami(ctx context.Context, _ struct{}) (WhoamiResult, error) {
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
