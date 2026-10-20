package connectmatrix

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Check outcomes.
const (
	Pass = "pass"
	Fail = "fail"
	Skip = "skip"
)

// CheckResult is one step of a pair's proof.
type CheckResult struct {
	Name    string  `json:"name"`
	Result  string  `json:"result"`
	Detail  string  `json:"detail,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
}

// PairResult is the outcome for one pair.
type PairResult struct {
	Name   string        `json:"name"`
	Target string        `json:"target"`
	Pass   bool          `json:"pass"`
	Checks []CheckResult `json:"checks"`
}

// Runner proves pairs against running targets.
type Runner struct {
	Issuer Issuer
	Client *http.Client
	Cfg    Config
	// Now and Sleep are replaceable for tests.
	Now   func() time.Time
	Sleep func(time.Duration)
}

// Run proves each pair (or only those named) and returns the results in
// config order.
func (r *Runner) Run(ctx context.Context, only map[string]bool) []PairResult {
	if r.Client == nil {
		r.Client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Sleep == nil {
		r.Sleep = time.Sleep
	}
	var out []PairResult
	for _, p := range r.Cfg.Pairs {
		if len(only) > 0 && !only[p.Name] {
			continue
		}
		out = append(out, r.runPair(ctx, p))
	}
	return out
}

func (r *Runner) runPair(ctx context.Context, p Pair) PairResult {
	res := PairResult{Name: p.Name, Target: p.Target, Pass: true}
	add := func(c CheckResult) bool {
		res.Checks = append(res.Checks, c)
		if c.Result == Fail {
			res.Pass = false
		}
		return c.Result != Fail
	}

	good, err := r.Issuer.Mint(ctx, MintRequest{Name: p.Name, Audience: p.Audience, Scopes: p.Scopes})
	if !add(check("mint", err, "issuer minted a service token")) {
		return res
	}
	st, err := r.probe(ctx, p, good.Token)
	if !add(expectStatus("accepted", st, err, p.Probe.OKStatus)) {
		_ = r.Issuer.Revoke(ctx, good) // do not leave a live token behind
		return res
	}

	if !p.enforcing() {
		add(CheckResult{Name: "wrong_audience", Result: Skip, Detail: "pair is not enforcing"})
		add(CheckResult{Name: "revoked", Result: Skip, Detail: "pair is not enforcing"})
		_ = r.Issuer.Revoke(ctx, good)
		return res
	}

	other, err := r.Issuer.Mint(ctx, MintRequest{Name: p.Name + "-other", Audience: p.OtherAudience, Scopes: p.Scopes})
	if check("mint_other", err, "").Result == Fail {
		add(check("wrong_audience", err, ""))
	} else {
		st, err = r.probe(ctx, p, other.Token)
		add(expectRejected("wrong_audience", st, err))
		_ = r.Issuer.Revoke(ctx, other)
	}

	if err := r.Issuer.Revoke(ctx, good); !add(check("revoke", err, "issuer revoked the token")) {
		return res
	}
	start := r.Now()
	deadline := start.Add(r.Cfg.Bound())
	for {
		st, err = r.probe(ctx, p, good.Token)
		if err == nil && (st == http.StatusUnauthorized || st == http.StatusForbidden) {
			add(CheckResult{Name: "revoked", Result: Pass, Seconds: r.Now().Sub(start).Seconds(),
				Detail: fmt.Sprintf("target answered %d", st)})
			return res
		}
		if !r.Now().Before(deadline) || ctx.Err() != nil {
			detail := fmt.Sprintf("still answered %d after %s", st, r.Cfg.Bound())
			if err != nil {
				detail = "probe error: " + err.Error()
			}
			add(CheckResult{Name: "revoked", Result: Fail, Seconds: r.Now().Sub(start).Seconds(), Detail: detail})
			return res
		}
		r.Sleep(r.Cfg.Poll())
	}
}

// probe makes the pair's request with token ("" = no Authorization header).
func (r *Runner) probe(ctx context.Context, p Pair, token string) (int, error) {
	var body io.Reader
	if p.Probe.Body != "" {
		body = strings.NewReader(p.Probe.Body)
	}
	req, err := http.NewRequestWithContext(ctx, p.Probe.Method, strings.TrimRight(p.Target, "/")+p.Probe.Path, body)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.Client.Do(req) // #nosec G704 -- operator-supplied target
	if err != nil {
		return 0, fmt.Errorf("%s", scrub(err))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func check(name string, err error, ok string) CheckResult {
	if err != nil {
		return CheckResult{Name: name, Result: Fail, Detail: err.Error()}
	}
	return CheckResult{Name: name, Result: Pass, Detail: ok}
}

func expectStatus(name string, got int, err error, want int) CheckResult {
	switch {
	case err != nil:
		return CheckResult{Name: name, Result: Fail, Detail: "probe error: " + err.Error()}
	case got != want:
		return CheckResult{Name: name, Result: Fail, Detail: fmt.Sprintf("target answered %d, want %d", got, want)}
	}
	return CheckResult{Name: name, Result: Pass, Detail: fmt.Sprintf("target answered %d", got)}
}

func expectRejected(name string, got int, err error) CheckResult {
	switch {
	case err != nil:
		return CheckResult{Name: name, Result: Fail, Detail: "probe error: " + err.Error()}
	case got != http.StatusUnauthorized && got != http.StatusForbidden:
		return CheckResult{Name: name, Result: Fail, Detail: fmt.Sprintf("target answered %d, want 401 or 403 (is the target enforcing?)", got)}
	}
	return CheckResult{Name: name, Result: Pass, Detail: fmt.Sprintf("target answered %d", got)}
}
