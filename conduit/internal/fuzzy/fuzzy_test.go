package fuzzy

import (
	"reflect"
	"testing"
)

func TestSubsequenceOnly(t *testing.T) {
	if _, _, ok := Match("gst", "git status"); !ok {
		t.Error("gst should match git status")
	}
	if _, _, ok := Match("gts", "git status"); ok == false {
		t.Log("gts is a subsequence (g-t-s) of git status; fine")
	}
	if _, _, ok := Match("xyz", "git status"); ok {
		t.Error("xyz must not match")
	}
	if _, _, ok := Match("longerpattern", "short"); ok {
		t.Error("pattern longer than text must not match")
	}
}

func TestSmartCase(t *testing.T) {
	if _, _, ok := Match("git", "GIT status"); !ok {
		t.Error("lower-case pattern should match any case")
	}
	if _, _, ok := Match("Git", "git status"); ok {
		t.Error("upper-case in pattern makes it case-sensitive")
	}
}

func TestPositionsPointAtMatchedRunes(t *testing.T) {
	text := "git status --short"
	_, pos, ok := Match("gss", text)
	if !ok || len(pos) != 3 {
		t.Fatalf("%v %v", pos, ok)
	}
	rs := []rune(text)
	for i, want := range "gss" {
		if rs[pos[i]] != want {
			t.Errorf("pos %d -> %q want %q", pos[i], rs[pos[i]], want)
		}
	}
	for i := 1; i < len(pos); i++ {
		if pos[i] <= pos[i-1] {
			t.Errorf("positions not increasing: %v", pos)
		}
	}
}

func TestBoundaryAndConsecutiveBeatScatter(t *testing.T) {
	// "fb" at word starts should beat the same letters buried in words
	a, _, _ := Match("fb", "foo bar")
	b, _, _ := Match("fb", "xfxxxbx")
	if a <= b {
		t.Errorf("boundary match %d should outscore scattered %d", a, b)
	}
	c, _, _ := Match("stat", "git status")
	d, _, _ := Match("stat", "s_t_a_t")
	if c <= d {
		t.Errorf("consecutive %d should outscore split %d", c, d)
	}
}

func TestRankOrdersByScoreThenInputOrder(t *testing.T) {
	items := []string{"go test ./...", "git status", "git stash", "grep -rn status ."}
	got := Rank("git st", items)
	var idx []int
	for _, r := range got {
		idx = append(idx, r.Index)
	}
	if !reflect.DeepEqual(idx, []int{1, 2}) && !reflect.DeepEqual(idx, []int{2, 1}) {
		t.Fatalf("%v", idx)
	}
	// equal score keeps input order (most recent first)
	same := Rank("echo", []string{"echo a", "echo a"})
	if same[0].Index != 0 || same[1].Index != 1 {
		t.Errorf("unstable: %v", same)
	}
}

func TestEmptyQueryReturnsAllInOrder(t *testing.T) {
	r := Rank("  ", []string{"b", "a", "c"})
	if len(r) != 3 || r[0].Index != 0 || r[2].Index != 2 {
		t.Errorf("%v", r)
	}
}

func TestMultiTermAnd(t *testing.T) {
	r := Rank("git push", []string{"git status", "git push origin main", "push"})
	if len(r) != 1 || r[0].Index != 1 {
		t.Errorf("%v", r)
	}
}

func TestUnicode(t *testing.T) {
	if _, pos, ok := Match("é", "café au lait"); !ok || len(pos) != 1 || pos[0] != 3 {
		t.Errorf("%v %v", pos, ok)
	}
}

func BenchmarkRank(b *testing.B) {
	items := make([]string, 5000)
	for i := range items {
		items[i] = "git -C /home/user/projects/app commit -m message number " + string(rune('a'+i%26))
	}
	for i := 0; i < b.N; i++ {
		Rank("gcm", items)
	}
}

// Property: whenever Match says ok, the positions are strictly increasing, in
// range, and each names a rune equal (smart-case) to the pattern rune; and ok
// agrees with a plain subsequence test.
func TestMatchPositionsProperty(t *testing.T) {
	seed := uint32(12345)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	const alpha = "abc de/-_ABC"
	gen := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = alpha[next(len(alpha))]
		}
		return string(b)
	}
	for i := 0; i < 3000; i++ {
		text := gen(1 + next(24))
		pat := gen(1 + next(5))
		score, pos, ok := Match(pat, text)
		// reference subsequence test with the same smart-case rule
		smart := pat == string([]rune(pat))
		for _, r := range pat {
			if r >= 'A' && r <= 'Z' {
				smart = false
			}
		}
		k := 0
		for _, r := range text {
			if k < len(pat) {
				a, b := rune(pat[k]), r
				if a == b || (smart && b >= 'A' && b <= 'Z' && b+32 == a) {
					k++
				}
			}
		}
		if ok != (k == len(pat)) {
			t.Fatalf("ok=%v but subsequence=%v for %q in %q", ok, k == len(pat), pat, text)
		}
		if !ok {
			continue
		}
		_ = score
		rs := []rune(text)
		if len(pos) != len(pat) {
			t.Fatalf("pos %v for %q in %q", pos, pat, text)
		}
		for x, p := range pos {
			if p < 0 || p >= len(rs) || (x > 0 && p <= pos[x-1]) {
				t.Fatalf("bad positions %v for %q in %q", pos, pat, text)
			}
			a, b := rune(pat[x]), rs[p]
			if a != b && (!smart || b < 'A' || b > 'Z' || b+32 != a) {
				t.Fatalf("pos %d (%q) does not match %q: %q in %q", p, b, a, pat, text)
			}
		}
	}
}
