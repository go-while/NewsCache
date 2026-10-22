package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// updatenewsBin returns the updatenews binary next to newscache, or skips.
func updatenewsBin(t *testing.T, e *env) string {
	t.Helper()
	p := filepath.Join(filepath.Dir(e.Bin), "updatenews")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("updatenews not found next to %s", e.Bin)
	}
	return p
}

// prefetched returns the groups updatenews selected on the upstream, in order.
func prefetched(e *env) []string {
	var groups []string
	for _, l := range e.Upstream.Lines() {
		if f := strings.Fields(l); len(f) == 2 && strings.EqualFold(f[0], "GROUP") {
			groups = append(groups, f[1])
		}
	}
	return groups
}

// TestPrefetchWildcards: lines of the prefetch file can be wildmat patterns,
// and "!pattern" excludes groups. updatenews used to take every line as a
// group name, so "de.*" asked the upstream for a group called "de.*".
func TestPrefetchWildcards(t *testing.T) {
	e := newEnv(t)
	for _, g := range []string{"de.test", "de.comp.lang.c", "de.alt.test", "de.alt.binaries.x",
		"at.test", "at.tast", "at.toast", "comp.misc", "comp.lang.go"} {
		e.Store.AddGroup(g, g)
		e.Store.AddArticle(g, groupArticle(g, 1, []string{"body of " + g}))
	}
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte(`# groups to prefetch

comp.misc
de.*
!de.alt.*
at.t?st
  comp.misc  
comp.lang.[gx]o
`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	e.writeConf()

	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf, "-o").CombinedOutput()
	if err != nil {
		t.Fatalf("updatenews -o: %v\n%s", err, out)
	}
	got := prefetched(e)
	want := []string{"comp.misc", "de.comp.lang.c", "de.test", "at.tast", "at.test", "comp.lang.go"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("prefetched groups:\n  got  %q\n  want %q", got, want)
	}
}

// TestPrefetchLiteral: plain group names are prefetched as before, also
// groups the active database doesn't list (yet); "!group" leaves one out.
func TestPrefetchLiteral(t *testing.T) {
	e := newEnv(t)
	for _, g := range []string{"de.test", "at.test"} {
		e.Store.AddGroup(g, g)
		e.Store.AddArticle(g, groupArticle(g, 1, []string{"body of " + g}))
	}
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte("at.test\nno.such.group\nde.test\n!at.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	e.writeConf()

	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf, "-o").CombinedOutput()
	if err != nil {
		t.Fatalf("updatenews -o: %v\n%s", err, out)
	}
	got := prefetched(e)
	// A name is used as it is, even if the active list doesn't have it.
	want := []string{"no.such.group", "de.test"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("prefetched groups:\n  got  %q\n  want %q", got, want)
	}
}
