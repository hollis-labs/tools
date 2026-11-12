package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// Nothing accepts a legacy_grants code or a legacy_devices token as a credential: the tables are
// history (the schema comment promises it). The rows hold the sha256 the old binary stored; the
// plaintext is presented on every surface that takes a credential.
func TestNoLegacyGrantOrDeviceTokenIsAccepted(t *testing.T) {
	s := newSite(t)
	s.bootstrap("caster.read")
	// Built at run time: the shapes of the old pairing code and device token, with no literal credential.
	code, token := "pair_"+strings.Repeat("c", 24), "cst_"+strings.Repeat("t", 24)
	h := func(v string) string { x := sha256.Sum256([]byte(v)); return hex.EncodeToString(x[:]) }
	db, err := sql.Open("sqlite", filepath.Join(s.dir, "data", appName, "workspaces", "default", "caster.db"))
	must(t, err)
	_, err = db.Exec(`INSERT INTO legacy_grants(id,code_hash,scopes,created_at,expires_at) VALUES('g',?, 'caster.read caster.write caster.admin','2026-01-01T00:00:00Z','2099-01-01T00:00:00Z')`, h(code))
	must(t, err)
	_, err = db.Exec(`INSERT INTO legacy_devices(id,token_hash,scopes,grant_id,created_at,expires_at) VALUES('d',?, 'caster.read caster.write caster.admin','g','2026-01-01T00:00:00Z','2099-01-01T00:00:00Z')`, h(token))
	must(t, err)
	must(t, db.Close())

	base, _ := s.serve()
	for _, tok := range []string{token, h(token), strings.TrimPrefix(token, "cst_")} {
		for _, path := range []string{"/api/list", "/api/whoami", "/api/get?ref=a/b"} {
			if c, b := httpDo(t, "GET", base+path, tok, ""); c != 401 {
				t.Fatalf("GET %s with the legacy token: want 401, got %d %s", path, c, b)
			}
		}
		if c, b := httpDo(t, "POST", base+"/api/put", tok, `{"name":"a/b","content":"x"}`); c != 401 {
			t.Fatalf("put with the legacy token: %d %s", c, b)
		}
	}
	// The old code is not a pairing code: redeeming it gives no key.
	for _, c := range []string{code, h(code)} {
		if st, b := httpDo(t, "POST", base+"/api/pair", "", `{"code":"`+c+`"}`); st == 200 || strings.Contains(b, `"key"`) {
			t.Fatalf("the legacy code was redeemed: %d %s", st, b)
		}
	}
	// And the CLI, as a client of the service with that token, is refused too.
	if _, errs, code := s.run("", []string{"CASTER_REMOTE=" + base, "CASTER_ALLOW_HTTP=1", "CASTER_TOKEN=" + token}, "ls"); code == 0 {
		t.Fatalf("the CLI accepted the legacy token: %s", errs)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
