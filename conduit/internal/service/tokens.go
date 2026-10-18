package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hollis-labs/conduit/internal/store"
)

// Scopes. A scope may carry a host suffix (exec.run:worker-1) or :* ; exec.admin
// covers every other scope.
const (
	ScopeRead    = "exec.read"     // see every principal's runs and output
	ScopeReadOwn = "exec.read.own" // see only runs this principal submitted (exec.run implies it)
	ScopeRun     = "exec.run"
	ScopeApprove = "exec.approve"
	ScopeAdmin   = "exec.admin"
)

var knownScopes = []string{ScopeRead, ScopeReadOwn, ScopeRun, ScopeApprove, ScopeAdmin} // for messages

// ErrUnauthenticated is returned for a missing, unknown or revoked token.
var ErrUnauthenticated = errors.New("unauthenticated")

// Principal is an authenticated API caller.
type Principal struct {
	Name    string
	TokenID string
	Scopes  []string
}

// ID is how runs submitted by this principal are attributed.
func (p *Principal) ID() string { return "token:" + p.Name }

// Can reports whether the principal holds scope want for this host.
func (p *Principal) Can(want, host string) bool {
	if p.holds(want, host) {
		return true
	}
	// Seeing your own runs is implied by seeing everyone's, and by being allowed
	// to submit at all (you must be able to poll what you started).
	return want == ScopeReadOwn && (p.holds(ScopeRead, host) || p.holds(ScopeRun, host))
}

func (p *Principal) holds(want, host string) bool {
	for _, s := range p.Scopes {
		if s == ScopeAdmin || s == want || s == want+":*" || (host != "" && s == want+":"+host) {
			return true
		}
	}
	return false
}

// SeesAll reports whether the principal may read other principals' runs.
func (p *Principal) SeesAll(host string) bool { return p.holds(ScopeRead, host) }

// Host is the name of the machine this service runs commands on.
func (s *Service) Host() string { return s.host }

func hashToken(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// scopeRe is the whole scope string, so a value cannot smuggle a second scope in after a space.
var scopeRe = regexp.MustCompile(`^exec\.(read(\.own)?|run|approve|admin)(:[A-Za-z0-9._*-]+)?$`)

func validScope(sc string) bool { return scopeRe.MatchString(sc) }

// CreateToken mints a token. The secret is returned once and only its hash is kept.
func (s *Service) CreateToken(name string, scopes []string) (string, *store.Token, error) {
	if name == "" || strings.ContainsAny(name, " \t\n:") {
		return "", nil, errors.New("token name must be a non-empty word without spaces or colons")
	}
	if len(scopes) == 0 {
		return "", nil, errors.New("a token needs at least one scope")
	}
	for _, sc := range scopes {
		if !validScope(sc) {
			return "", nil, fmt.Errorf("unknown scope %q (want %s, optionally :host)", sc, strings.Join(knownScopes, ", "))
		}
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, err
	}
	secret := "cdt_" + base64.RawURLEncoding.EncodeToString(raw[:])
	t := &store.Token{ID: "tok_" + hex.EncodeToString(raw[:4]), Name: name, Hash: hashToken(secret), Scopes: strings.Join(scopes, " "), CreatedAt: time.Now()}
	if err := s.store.InsertToken(t); err != nil {
		return "", nil, err
	}
	return secret, t, nil
}

// Authenticate resolves a bearer secret to a principal.
func (s *Service) Authenticate(secret string) (*Principal, error) {
	if !strings.HasPrefix(secret, "cdt_") {
		return nil, ErrUnauthenticated
	}
	t, err := s.store.TokenByHash(hashToken(secret))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	_ = s.store.TouchToken(t.ID, time.Now())
	return &Principal{Name: t.Name, TokenID: t.ID, Scopes: strings.Fields(t.Scopes)}, nil
}

// Tokens lists tokens (never secrets).
func (s *Service) Tokens() ([]*store.Token, error) { return s.store.ListTokens() }

// RevokeToken revokes by id or name.
func (s *Service) RevokeToken(idOrName string) (bool, error) {
	return s.store.RevokeToken(idOrName, time.Now())
}
