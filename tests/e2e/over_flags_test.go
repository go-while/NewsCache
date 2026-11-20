package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
)

// The upstream refuses OVER, and then the XOVER that NewsCache tries
// instead, with a reply that doesn't say the command is unsupported: 403,
// or 480 when no login is configured. Any reply to OVER or XOVER but 224,
// 502, 412, 423 and 420 turned the command off for the rest of the
// process, so after one such pair every later overview request of the
// session, or of the updatenews run, failed with "neither OVER nor XOVER"
// without asking the upstream again. Now only 500, 501, 503 and a
// positive reply other than 224 turn a command off (as for LISTGROUP,
// listgroup_unsupported); a refusal for now fails the request it answers.

// refuseOnce: the first OVER and the first XOVER of a connection get these
// replies, the later ones their normal replies.
func refuseOnce(overReply, xoverReply string) fakeserver.Faults {
	return fakeserver.Faults{
		Override:      map[string][]string{"OVER": {overReply}, "XOVER": {xoverReply}},
		OverrideTimes: map[string]int{"OVER": 1, "XOVER": 1},
	}
}

// overCounts: how many OVER and XOVER commands the upstream got.
func overCounts(e *env) (int, int) {
	return e.Upstream.Count("OVER"), e.Upstream.Count("XOVER")
}

func wantOverCounts(t *testing.T, e *env, what string, nOver, nXover int) {
	t.Helper()
	if o, x := overCounts(e); o != nOver || x != nXover {
		t.Errorf("%s: upstream got %d OVER and %d XOVER in all, want %d and %d",
			what, o, x, nOver, nXover)
	}
}

// xoverAsked: XOVER a-b gives want, and the upstream was asked for it.
func xoverAsked(t *testing.T, e *env, c *client.Conn, what string, a, b int64, want ...int64) {
	t.Helper()
	o, x := overCounts(e)
	code, line := c.Cmd("XOVER %d-%d", a, b)
	if code != 224 {
		t.Fatalf("%s: XOVER %d-%d: %q, want 224 (did an earlier refusal turn OVER and XOVER off?)",
			what, a, b, line)
	}
	wantNumbers(t, what, xoverNumbers(t, c.ReadDotLines()), want...)
	if o2, x2 := overCounts(e); o2 == o && x2 == x {
		t.Errorf("%s: answered without asking the upstream", what)
	}
}

// TestOverRefusedOnce: a not-cached session. The first OVER and the first
// XOVER of the upstream connection are refused for now. The XOVER that
// meets them gets 403, after one OVER and one XOVER (no more: the
// fallback is bounded); the next one, in the same group and in another,
// gets the records with one OVER each. They got 403 ("neither OVER nor
// XOVER", the upstream not asked).
func TestOverRefusedOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    fakeserver.Faults
	}{
		{"403", refuseOnce("403 internal fault", "403 internal fault")},
		// (no User configured: issue() doesn't log in again)
		{"480-no-login", refuseOnce("480 Authentication required", "480 Authentication required")},
		// XOVER unsupported after OVER was refused for now: XOVER goes
		// off, OVER stays on for the next request
		{"403-then-xover-500", fakeserver.Faults{
			Override:      map[string][]string{"OVER": {"403 internal fault"}, "XOVER": {"500 unknown command"}},
			OverrideTimes: map[string]int{"OVER": 1},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.ServerOptions = "not-cached"
			e.addArticles("test.alpha", 3)
			e.addArticles("test.beta", 2)
			upstreamWith(t, e, fakeserver.Options{Faults: tc.f})
			c := e.session()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP test.alpha: %q", line)
			}
			if code, line := c.Cmd("XOVER 1-3"); code != 403 {
				readList(c, code)
				t.Errorf("XOVER 1-3 with OVER and XOVER refused: %q, want 403", line)
			}
			wantOverCounts(t, e, "the refused XOVER 1-3", 1, 1)
			xoverAsked(t, e, c, "XOVER 1-3 again", 1, 3, 1, 2, 3)
			wantOverCounts(t, e, "XOVER 1-3 again", 2, 1)
			if code, line := c.Cmd("GROUP test.beta"); code != 211 {
				t.Fatalf("GROUP test.beta: %q", line)
			}
			xoverAsked(t, e, c, "XOVER 1-2 in test.beta", 1, 2, 1, 2)
			wantOverCounts(t, e, "XOVER 1-2 in test.beta", 3, 1)
			if code, line := c.Cmd("DATE"); code != 111 {
				t.Errorf("DATE: %q", line)
			}
			c.Cmd("QUIT")
		})
	}
}

// TestOverRefusedOnceCached: the same in cached groups (the default). The
// chunk refresh meets both refusals (one OVER, one XOVER) and fails, so the
// command asks the upstream for the range again (one more OVER) and gets
// the records; the next group's refresh asks the upstream too. Both got
// 403 ("neither OVER nor XOVER").
func TestOverRefusedOnceCached(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 3)
	e.addArticles("test.beta", 2)
	upstreamWith(t, e, fakeserver.Options{Faults: refuseOnce("403 internal fault", "403 internal fault")})
	c := e.session()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP test.alpha: %q", line)
	}
	xoverAsked(t, e, c, "XOVER 1-3, its refresh refused", 1, 3, 1, 2, 3)
	wantOverCounts(t, e, "XOVER 1-3, its refresh refused", 2, 1)
	if code, line := c.Cmd("GROUP test.beta"); code != 211 {
		t.Fatalf("GROUP test.beta: %q", line)
	}
	xoverAsked(t, e, c, "XOVER 1-2 in test.beta", 1, 2, 1, 2)
	wantOverCounts(t, e, "XOVER 1-2 in test.beta", 3, 1)
	c.Cmd("QUIT")
}

// TestOverRefusedEveryTime: the upstream refuses every OVER and XOVER with
// 403 (not-cached). Every XOVER gets 403 after exactly one OVER and one
// XOVER: no loop between the two, and no request answered without asking.
// The second one got 403 without any (the first had turned both off).
func TestOverRefusedEveryTime(t *testing.T) {
	e := newEnv(t)
	e.ServerOptions = "not-cached"
	e.addArticles("test.alpha", 3)
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		Override: map[string][]string{"OVER": {"403 internal fault"}, "XOVER": {"403 internal fault"}},
	}})
	c := e.session()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP test.alpha: %q", line)
	}
	for i := 1; i <= 3; i++ {
		if code, line := c.Cmd("XOVER 1-3"); code != 403 {
			readList(c, code)
			t.Errorf("XOVER 1-3 #%d: %q, want 403", i, line)
		}
		wantOverCounts(t, e, fmt.Sprintf("refused XOVER 1-3 #%d", i), i, i)
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE: %q", line)
	}
	c.Cmd("QUIT")
}

// TestOverUnsupportedTurnsOff: 500, 501 or 503 (the connection kept open)
// to OVER says the upstream doesn't support it: OVER is asked once in the
// session, XOVER from then on, and every request gets the records.
func TestOverUnsupportedTurnsOff(t *testing.T) {
	for _, reply := range []string{"500 unknown command", "501 syntax error", "503 feature not supported"} {
		t.Run(reply[:3], func(t *testing.T) {
			e := newEnv(t)
			e.ServerOptions = "not-cached"
			e.addArticles("test.alpha", 3)
			e.addArticles("test.beta", 2)
			upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
				Override: map[string][]string{"OVER": {reply}},
			}})
			c := e.session()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP test.alpha: %q", line)
			}
			xoverAsked(t, e, c, "XOVER 1-3", 1, 3, 1, 2, 3)
			wantOverCounts(t, e, "XOVER 1-3", 1, 1)
			xoverAsked(t, e, c, "XOVER 1-3 again", 1, 3, 1, 2, 3)
			wantOverCounts(t, e, "XOVER 1-3 again", 1, 2)
			if code, line := c.Cmd("GROUP test.beta"); code != 211 {
				t.Fatalf("GROUP test.beta: %q", line)
			}
			xoverAsked(t, e, c, "XOVER 1-2 in test.beta", 1, 2, 1, 2)
			wantOverCounts(t, e, "XOVER 1-2 in test.beta", 1, 3)
			c.Cmd("QUIT")
		})
	}
}

// TestUpdatenewsOverRefusedOnce: updatenews -o, one process for all
// groups. The first OVER and XOVER of the run are refused for now, in the
// first group (one OVER and one XOVER there). The next group's overview is
// still asked for; it wasn't (OVER and XOVER off for the rest of the run).
func TestUpdatenewsOverRefusedOnce(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 3)
	e.addArticles("test.beta", 2)
	upstreamWith(t, e, fakeserver.Options{Faults: refuseOnce("403 internal fault", "403 internal fault")})
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte("test.alpha\ntest.beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	e.writeConf()
	out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf, "-o").CombinedOutput()
	if err != nil {
		t.Fatalf("updatenews -o: %v\n%s", err, out)
	}
	if got := prefetched(e); len(got) == 0 || got[0] != "test.alpha" || got[len(got)-1] != "test.beta" {
		t.Fatalf("prefetched groups %q, want test.alpha, then test.beta", got)
	}
	// OVER and XOVER per group: the one selected last before them
	n := map[string]int{}
	group := ""
	for _, l := range e.Upstream.Lines() {
		f := strings.Fields(l)
		switch {
		case len(f) == 2 && strings.EqualFold(f[0], "GROUP"):
			group = f[1]
		case len(f) > 0 && (strings.EqualFold(f[0], "OVER") || strings.EqualFold(f[0], "XOVER")):
			n[group+" "+strings.ToUpper(f[0])]++
		}
	}
	if n["test.alpha OVER"] != 1 || n["test.alpha XOVER"] != 1 {
		t.Errorf("test.alpha: upstream got %d OVER and %d XOVER, want 1 and 1 (the refused pair)",
			n["test.alpha OVER"], n["test.alpha XOVER"])
	}
	if n["test.beta OVER"] < 1 || n["test.beta OVER"]+n["test.beta XOVER"] > 2 {
		t.Errorf("test.beta: upstream got %d OVER and %d XOVER, want an OVER (no more than one of each)",
			n["test.beta OVER"], n["test.beta XOVER"])
	}
}
