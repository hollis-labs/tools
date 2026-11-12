package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// The pre-Substrate schema, as the old binary created it.
const oldSchema = `
CREATE TABLE blobs (hash TEXT PRIMARY KEY, size INTEGER NOT NULL, mime TEXT NOT NULL, source_ref TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
CREATE TABLE name_revs (ns TEXT NOT NULL, name TEXT NOT NULL, rev INTEGER NOT NULL, hash TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', tags TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, PRIMARY KEY (ns, name, rev));
CREATE TABLE action_log (idem_key TEXT PRIMARY KEY, action TEXT NOT NULL, source_ref TEXT NOT NULL, outcome_ref TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE observations (id INTEGER PRIMARY KEY AUTOINCREMENT, text_hash TEXT NOT NULL, generation INTEGER NOT NULL, first_source TEXT NOT NULL DEFAULT '', first_at TEXT NOT NULL, last_at TEXT NOT NULL, seen INTEGER NOT NULL DEFAULT 1, candidates INTEGER NOT NULL, resolved INTEGER NOT NULL, would_show INTEGER NOT NULL, UNIQUE (text_hash, generation));
CREATE TABLE detections (id INTEGER PRIMARY KEY AUTOINCREMENT, observation_id INTEGER NOT NULL REFERENCES observations (id), extractor TEXT NOT NULL, confidence TEXT NOT NULL, ref TEXT NOT NULL, scheme TEXT NOT NULL, state TEXT NOT NULL, verdict TEXT NOT NULL, would_show INTEGER NOT NULL, error TEXT NOT NULL DEFAULT '', label TEXT NOT NULL DEFAULT '');
CREATE INDEX detections_ref ON detections (ref);
CREATE TABLE grants (id TEXT PRIMARY KEY, code_hash TEXT NOT NULL UNIQUE, scopes TEXT NOT NULL, label TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, expires_at TEXT NOT NULL, consumed_at TEXT, revoked_at TEXT);
CREATE TABLE devices (id TEXT PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE, label TEXT NOT NULL DEFAULT '', scopes TEXT NOT NULL, grant_id TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT, last_used_at TEXT);
CREATE VIRTUAL TABLE blob_fts USING fts5 (hash UNINDEXED, body);
`

type oldStore struct {
	dir   string
	files map[string][]byte // hash -> bytes
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// buildOld writes an old data directory: main.db with rows in every table, the blob tree, a leftover
// temp file. mutate may break it before it is closed.
func buildOld(t *testing.T, dir string) *oldStore {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "blobs", "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blobs", "tmp", "put-123"), []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	must := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(oldSchema)
	o := &oldStore{dir: dir, files: map[string][]byte{}}
	put := func(body []byte, mime string, index bool) string {
		h := sum(body)
		o.files[h] = body
		p := filepath.Join(dir, "blobs", "sha256", h[:2], h[2:])
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o400); err != nil {
			t.Fatal(err)
		}
		must(`INSERT INTO blobs VALUES(?,?,?,?,?)`, h, len(body), mime, "src", "2026-01-02T03:04:05.123456789Z")
		if index {
			must(`INSERT INTO blob_fts(hash, body) VALUES(?,?)`, h, string(body))
		}
		return h
	}
	text := put([]byte("walrus notes, ünïcode and all"), "text/plain; charset=utf-8", true)
	bin := put([]byte{0, 1, 2, 0xff, 0xfe, 0}, "application/octet-stream", false)
	put([]byte(""), "text/plain", true) // an empty text blob is indexed too
	must(`INSERT INTO name_revs VALUES('a','n',1,?,'T','x y','2026-01-02T03:04:05Z')`, text)
	must(`INSERT INTO name_revs VALUES('a','n',2,?,'','','2026-01-03T03:04:05Z')`, bin)
	must(`INSERT INTO action_log VALUES('k','act','caster://a/n@1','caster://a/o@1','2026-01-02T03:04:05Z')`)
	must(`INSERT INTO observations VALUES(7,'th',1,'s','2026-01-02T03:04:05Z','2026-01-02T03:04:06Z',3,2,1,0)`)
	must(`INSERT INTO detections VALUES(9,7,'x','high','caster://a/n','caster','resolved','show',1,'','lbl')`)
	must(`INSERT INTO grants VALUES('g1','chash','caster.read','L','2026-01-02T03:04:05Z','2026-01-02T04:04:05Z','2026-01-02T03:05:05Z',NULL)`)
	must(`INSERT INTO devices VALUES('d1','thash','L','caster.read','g1','2026-01-02T03:05:05Z','2026-02-02T03:05:05Z',NULL,'2026-01-05T00:00:00Z')`)
	return o
}

func snapshotOld(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a vanished file is simply not in the snapshot
		}
		b, _ := os.ReadFile(p) //nolint:gosec // a path in the test's temp directory
		fi, _ := d.Info()
		rel, _ := filepath.Rel(dir, p)
		out[rel] = sum(b) + " " + fi.ModTime().String() + " " + fi.Mode().String()
		return nil
	})
	return out
}

func newMigrateSite(t *testing.T) (*site, string) {
	t.Helper()
	s := newSite(t)
	ws := filepath.Join(s.dir, "data", appName, "workspaces", "default")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	return s, ws
}

func TestMigrateFixture(t *testing.T) {
	s, ws := newMigrateSite(t)
	old := buildOld(t, ws)
	before := snapshotOld(t, ws)

	out, errs, code := s.run("", nil, "migrate")
	if code != 0 || !strings.Contains(out, "PROVED EQUAL") {
		t.Fatalf("migrate: exit %d\n%s\n%s", code, out, errs)
	}
	if after := snapshotOld(t, ws); len(after) < len(before) {
		t.Fatal("files vanished")
	} else {
		for k, v := range before {
			if after[k] != v {
				t.Fatalf("old file %s changed: %q -> %q", k, v, after[k])
			}
		}
	}
	for _, h := range []string{"caster.db", "cas", "migrate-report.json"} {
		if _, err := os.Stat(filepath.Join(ws, h)); err != nil {
			t.Fatalf("missing %s: %v", h, err)
		}
	}
	for _, p := range []string{"caster.db.migrating", "cas.migrating", ".migrate-scratch"} {
		if _, err := os.Stat(filepath.Join(ws, p)); err == nil {
			t.Fatalf("staging %s left behind", p)
		}
	}
	// Every blob is in the new tree, byte for byte, and read-only; the leftover tmp file is not.
	for h, b := range old.files {
		got, err := os.ReadFile(filepath.Join(ws, "cas", "sha256", h[:2], h[2:])) //nolint:gosec // temp dir
		if err != nil || string(got) != string(b) {
			t.Fatalf("blob %s: %v", h, err)
		}
	}
	if _, err := os.Stat(filepath.Join(ws, "cas", "tmp")); err == nil {
		t.Fatal("old tmp directory was carried over")
	}
	var rep struct {
		OK           bool `json:"ok"`
		OldUnchanged bool `json:"old_unchanged"`
		Tables       []struct {
			Source     string `json:"source"`
			Target     string `json:"target"`
			TargetRows int    `json:"target_rows"`
			Equal      bool   `json:"equal"`
		} `json:"tables"`
		Blobs struct{ Rows, Verified int } `json:"blobs"`
		FTS   struct {
			SourceRows  int `json:"source_rows"`
			RebuiltRows int `json:"rebuilt_rows"`
		} `json:"fts"`
	}
	b, err := os.ReadFile(filepath.Join(ws, "migrate-report.json")) //nolint:gosec // temp dir
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"blobs": 3, "name_revs": 2, "action_log": 1, "observations": 1, "detections": 1, "legacy_grants": 1, "legacy_devices": 1}
	for _, tr := range rep.Tables {
		if !tr.Equal || tr.TargetRows != want[tr.Target] {
			t.Fatalf("table %s: %+v", tr.Target, tr)
		}
		delete(want, tr.Target)
	}
	if len(want) != 0 || !rep.OK || !rep.OldUnchanged || rep.Blobs.Verified != 3 || rep.FTS.SourceRows != 2 || rep.FTS.RebuiltRows != 2 {
		t.Fatalf("report: %+v (tables not seen: %v)", rep, want)
	}

	// A second run, and a verify-only run, on the migrated directory.
	if _, errs, code = s.run("", nil, "migrate"); code == 0 || !strings.Contains(errs, "already exists") {
		t.Fatalf("a second migrate must refuse: %d %s", code, errs)
	}
	if out, errs, code = s.run("", nil, "migrate", "--verify-only"); code != 0 || !strings.Contains(out, "PROVED EQUAL") {
		t.Fatalf("verify-only: %d %s %s", code, out, errs)
	}
	// The migrated store is readable by the data commands (the guard now passes).
	if out, errs, code = s.run("", nil, "ls"); code != 0 || !strings.Contains(out, "a/n") {
		t.Fatalf("ls on the migrated store: %d %s %s", code, out, errs)
	}
	if out, errs, code = s.run("", nil, "search", "walrus"); code != 0 || !strings.Contains(out, "walrus") {
		t.Fatalf("search on the migrated store: %d %s %s", code, out, errs)
	}
}

// Anything the proof cannot show equal fails the run and leaves no store that looks migrated.
func TestMigrateRefusesWhatItCannotProve(t *testing.T) {
	cases := []struct {
		name    string
		breakIt func(t *testing.T, o *oldStore)
		want    string
	}{
		{"a row without its file", func(t *testing.T, o *oldStore) {
			for h := range o.files {
				must(t, os.Chmod(filepath.Join(o.dir, "blobs", "sha256", h[:2]), 0o700)) //nolint:gosec // a temp directory
				must(t, os.Chmod(filepath.Join(o.dir, "blobs", "sha256", h[:2], h[2:]), 0o600))
				must(t, os.Remove(filepath.Join(o.dir, "blobs", "sha256", h[:2], h[2:])))
				return
			}
		}, "without a file"},
		{"a corrupt file", func(t *testing.T, o *oldStore) {
			for h := range o.files {
				p := filepath.Join(o.dir, "blobs", "sha256", h[:2], h[2:])
				must(t, os.Chmod(p, 0o600))
				must(t, os.WriteFile(p, []byte("tampered"), 0o600))
				return
			}
		}, "corrupt"},
		{"a stray file in the tree", func(t *testing.T, o *oldStore) {
			must(t, os.WriteFile(filepath.Join(o.dir, "blobs", "README"), []byte("x"), 0o600))
		}, "README"},
		{"a table the migration does not know", func(t *testing.T, o *oldStore) {
			db, err := sql.Open("sqlite", filepath.Join(o.dir, "main.db"))
			must(t, err)
			defer func() { _ = db.Close() }()
			_, err = db.Exec(`CREATE TABLE secrets_of_the_future (x TEXT)`)
			must(t, err)
		}, "secrets_of_the_future"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ws := newMigrateSite(t)
			o := buildOld(t, ws)
			tc.breakIt(t, o)
			before := snapshotOld(t, ws)
			out, errs, code := s.run("", nil, "migrate")
			if code == 0 || strings.Contains(out, "PROVED EQUAL") || !strings.Contains(out+errs, tc.want) {
				t.Fatalf("want a failure naming %q, got exit %d\n%s\n%s", tc.want, code, out, errs)
			}
			for _, p := range []string{"caster.db", "cas", "caster.db.migrating", "cas.migrating", ".migrate-scratch"} {
				if _, err := os.Stat(filepath.Join(ws, p)); err == nil {
					t.Fatalf("%s left behind after a failed migration", p)
				}
			}
			for k, v := range before {
				if got := snapshotOld(t, ws)[k]; got != v {
					t.Fatalf("old file %s changed", k)
				}
			}
			// And the guard still refuses to start on it.
			if _, errs, code = s.run("", nil, "ls"); code == 0 || !strings.Contains(errs, "caster migrate") {
				t.Fatalf("a failed migration must leave the guard refusing: %d %s", code, errs)
			}
		})
	}
}

// A blob file with no row is what a failed write leaves behind (the file goes down before the
// transaction). It is copied, re-hashed and reported; it does not fail the run.
func TestMigrateCarriesAnUnreferencedBlobFile(t *testing.T) {
	s, ws := newMigrateSite(t)
	buildOld(t, ws)
	h := sum([]byte("orphan"))
	p := filepath.Join(ws, "blobs", "sha256", h[:2], h[2:])
	must(t, os.MkdirAll(filepath.Dir(p), 0o700))
	must(t, os.WriteFile(p, []byte("orphan"), 0o400))
	out, errs, code := s.run("", nil, "migrate")
	if code != 0 || !strings.Contains(out, "1 without a row") {
		t.Fatalf("%d\n%s\n%s", code, out, errs)
	}
	if got, err := os.ReadFile(filepath.Join(ws, "cas", "sha256", h[:2], h[2:])); err != nil || string(got) != "orphan" { //nolint:gosec // temp dir
		t.Fatalf("the unreferenced file was not carried: %v", err)
	}
}

// Changing a row after the copy shows up in verify-only: the comparison is not a count.
func TestMigrateVerifyCatchesADriftedRow(t *testing.T) {
	s, ws := newMigrateSite(t)
	buildOld(t, ws)
	if _, errs, code := s.run("", nil, "migrate"); code != 0 {
		t.Fatalf("migrate: %s", errs)
	}
	db, err := sql.Open("sqlite", filepath.Join(ws, "caster.db"))
	must(t, err)
	_, err = db.Exec(`UPDATE name_revs SET title='changed' WHERE rev=1`)
	must(t, err)
	_, err = db.Exec(`DELETE FROM blob_fts WHERE rowid=(SELECT min(rowid) FROM blob_fts)`)
	must(t, err)
	must(t, db.Close())
	out, _, code := s.run("", nil, "migrate", "--verify-only")
	if code == 0 || !strings.Contains(out, "DIFFERENT") || !strings.Contains(out, "NOT PROVED") {
		t.Fatalf("a changed row must fail verification: %d\n%s", code, out)
	}
}

// migrate refuses while something listens on the address the configuration serves, and refuses an
// old database whose write-ahead log was not merged. Neither changes anything.
func TestMigrateRefusesALiveServiceAndAnUnmergedWAL(t *testing.T) {
	s, ws := newMigrateSite(t)
	buildOld(t, ws)
	before := snapshotOld(t, ws)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	defer func() { _ = ln.Close() }()
	cfg := filepath.Join(s.dir, "live.yaml")
	must(t, os.WriteFile(cfg, []byte("httphost:\n  addr: \""+ln.Addr().String()+"\"\n"), 0o600))
	_, errs, code := s.run("", []string{"SUBSTRATE_CONFIG=" + cfg}, "migrate")
	if code == 0 || !strings.Contains(errs, "listening") {
		t.Fatalf("a listening service must stop the migration: %d %s", code, errs)
	}
	_ = ln.Close()

	must(t, os.WriteFile(filepath.Join(ws, "main.db-wal"), []byte("pretend frames"), 0o600))
	before["main.db-wal"] = snapshotOld(t, ws)["main.db-wal"]
	_, errs, code = s.run("", nil, "migrate")
	if code == 0 || !strings.Contains(errs, "unmerged") {
		t.Fatalf("an unmerged WAL must stop the migration: %d %s", code, errs)
	}
	after := snapshotOld(t, ws)
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s changed", k)
		}
	}
	for _, p := range []string{"caster.db", "cas", "caster.db.migrating", "cas.migrating"} {
		if _, err := os.Stat(filepath.Join(ws, p)); err == nil {
			t.Fatalf("%s left behind", p)
		}
	}
}
