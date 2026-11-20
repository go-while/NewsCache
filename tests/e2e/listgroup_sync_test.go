package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// A cached chunk is synchronized with LISTGROUP: the numbers the news
// server no longer lists are removed from the spool, and the chunk is
// marked as synchronized for the group timeout. A LISTGROUP that didn't
// come whole was taken as a whole list: every number was set ("assume the
// server has them all", so nothing was removed) and the chunk was still
// marked, so an article the news server had deleted stayed listed (XOVER,
// STAT, ARTICLE from the spool) until the group timeout ran out again.
// That happened when the reply was cut off by the network, when the news
// server answered LISTGROUP with 403, and when the connection for it
// could not be set up (the upstream closes right after its greeting; the
// ResponseError out of connect() looked like "LISTGROUP unsupported").
// updatenews -g marked the chunk the same way. Now such a chunk is marked
// only for a short retry pause, a quarter of the group timeout and at most
// a minute (none for a timeout under 4 s, as in the first two tests), and
// the next synchronization after it removes the article. updatenews (-g,
// -o) writes no retry entry: such a chunk stays unmarked there.

// listgroupSyncTimeout is the group timeout of these tests: long enough
// for the session after the faulty one to fall inside it, short enough to
// wait out once (after the first synchronization).
const listgroupSyncTimeout = 3

// incompleteListgroupFaults: the upstream faults that leave the
// synchronization's LISTGROUP incomplete (the deleted article is 2 of 1-3).
var incompleteListgroupFaults = []struct {
	name   string
	faults fakeserver.Faults
}{
	// the status line and "1", then the connection closes
	{"cut-off", fakeserver.Faults{DropInMultiline: map[string]int{"LISTGROUP": 2}}},
	{"403", fakeserver.Faults{Override: map[string][]string{"LISTGROUP": {"403 internal fault"}}}},
	// every connection closes after the greeting, before the reply to
	// MODE READER (connect() throws ResponseError, or SystemError once
	// its retries are used up)
	{"closed-after-greeting", fakeserver.Faults{DropInMultiline: map[string]int{"MODE": 0}}},
}

// wantSyncedWithout: a session with a working upstream, inside the group
// timeout of the faulty synchronization, sees that the news server
// deleted article 2. faulty is the time just before the faulty
// synchronization started: its stamp (on main) is no older, and the check
// only shows the bug while that stamp is still valid, so a run that took
// longer than the group timeout is reported as inconclusive instead of
// passing on main too. (The stamps are whole seconds: a second's margin.)
func wantSyncedWithout(t *testing.T, e *env, group string, faulty time.Time) {
	t.Helper()
	upstreamWith(t, e, fakeserver.Options{})
	c := e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("working upstream: GROUP: %q", line)
	}
	wantNumbers(t, "working upstream: XOVER 1-3 (article 2 deleted upstream)",
		xover(t, c, 1, 3), 1, 3)
	if code, line := c.Cmd("STAT 2"); code != 423 {
		t.Errorf("working upstream: STAT 2 (deleted upstream): %q, want 423", line)
	}
	if d := time.Since(faulty); d+time.Second >= listgroupSyncTimeout*time.Second {
		t.Fatalf("inconclusive: %v from the faulty synchronization to the check, "+
			"not within the group timeout (%d s) its stamp would have had", d,
			listgroupSyncTimeout)
	}
	c.Cmd("QUIT")
	if n := e.Upstream.Count("LISTGROUP"); n == 0 {
		t.Errorf("working upstream: no LISTGROUP: the incomplete synchronization " +
			"marked the chunk as synchronized")
	}
}

// TestCachedIncompleteListgroupNotSynced: a client's XOVER synchronizes
// the cached chunk while the upstream's LISTGROUP fails; the next session
// (inside the group timeout, with a working upstream) synchronizes it
// again and no longer lists the deleted article.
func TestCachedIncompleteListgroupNotSynced(t *testing.T) {
	const group = "test.alpha"
	for _, tc := range incompleteListgroupFaults {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.GroupTimeout = listgroupSyncTimeout
			e.addArticles(group, 3)

			// the chunk, cached and synchronized
			c := e.session()
			if code, line := c.Cmd("GROUP %s", group); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			wantNumbers(t, "first XOVER 1-3", xover(t, c, 1, 3), 1, 2, 3)
			c.Cmd("QUIT")

			if !e.Store.RemoveArticle(group, 2) {
				t.Fatal("remove article 2 upstream")
			}
			// the first synchronization's timeout runs out
			time.Sleep((listgroupSyncTimeout+1)*time.Second + 100*time.Millisecond)

			// the synchronization with the faulty LISTGROUP: the
			// client still gets the cached records
			faulty := time.Now()
			upstreamWith(t, e, fakeserver.Options{Faults: tc.faults})
			c = e.session()
			if code, line := c.Cmd("GROUP %s", group); code != 211 {
				t.Fatalf("faulty upstream: GROUP: %q", line)
			}
			got := xover(t, c, 1, 3)
			t.Logf("faulty upstream: XOVER 1-3: %v", got)
			if len(got) < 2 || got[0] != 1 || got[len(got)-1] != 3 {
				t.Errorf("faulty upstream: XOVER 1-3: numbers %v, want 1 and 3 at least", got)
			}
			c.Cmd("QUIT")

			wantSyncedWithout(t, e, group, faulty)
		})
	}
}

// TestPrefetchIncompleteListgroupNotSynced: the same for updatenews -g,
// whose chunk (prefetchchunk) was marked after an incomplete LISTGROUP as
// well. (A 403 also switched LISTGROUP off for the process, and the chunk
// was then not marked; the case guards the stamp once only an unsupported
// LISTGROUP switches it off.)
func TestPrefetchIncompleteListgroupNotSynced(t *testing.T) {
	const group = "test.alpha"
	for _, tc := range incompleteListgroupFaults {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.GroupTimeout = listgroupSyncTimeout
			e.addArticles(group, 3)
			prefetch := filepath.Join(e.Dir, "prefetch")
			if err := os.WriteFile(prefetch, []byte(group+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			e.Extra = "PrefetchFile " + prefetch
			e.writeConf()
			bin := updatenewsBin(t, e)

			// the articles, prefetched and the chunk synchronized
			if out, err := exec.Command(bin, "-c", e.Conf, "-g").CombinedOutput(); err != nil {
				dumpUpdatenewsFailure(t, e, string(out))
				t.Fatalf("updatenews -g: %v", err)
			}
			if n := e.Upstream.Count("ARTICLE"); n < 3 {
				t.Fatalf("updatenews -g fetched %d articles, want 3", n)
			}

			if !e.Store.RemoveArticle(group, 2) {
				t.Fatal("remove article 2 upstream")
			}
			time.Sleep((listgroupSyncTimeout+1)*time.Second + 100*time.Millisecond)

			// the prefetch with the faulty LISTGROUP (its exit status
			// is not what this test is about)
			faulty := time.Now()
			upstreamWith(t, e, fakeserver.Options{Faults: tc.faults})
			e.writeConf()
			out, err := exec.Command(bin, "-c", e.Conf, "-g").CombinedOutput()
			t.Logf("faulty upstream: updatenews -g: %v\n%s", err, out)

			wantSyncedWithout(t, e, group, faulty)
		})
	}
}

// retryPauseTimeout is the group timeout of the retry pause tests, and
// retryPause its pause (a quarter of it): the pause long enough for a
// session right after the faulty synchronization to fall inside it, the
// timeout long enough for the session after the pause to fall inside the
// stamp the faulty synchronization had before.
const (
	retryPauseTimeout = 12
	retryPause        = retryPauseTimeout / 4
)

// cutOffListgroup: the status line and "1" of LISTGROUP, then the
// connection closes.
var cutOffListgroup = fakeserver.Faults{DropInMultiline: map[string]int{"LISTGROUP": 2}}

// wantRetryPause: faultySync synchronizes the chunk of 1-3 while the
// upstream cuts LISTGROUP off (the chunk was synchronized before, its
// timeout has run out, and article 2 is deleted upstream). A session right
// after it, with a working upstream, is served from the cache: the chunk
// counts as synchronized for the retry pause. One after the pause
// synchronizes it again and no longer lists article 2.
//
// Without the retry pause, the chunk was synchronized again on every
// command while the upstream kept failing (the first session sends
// LISTGROUP); before that, it was marked for the whole group timeout (the
// second session doesn't). The stamps are whole seconds; a run too slow to
// tell these apart is reported as inconclusive.
func wantRetryPause(t *testing.T, e *env, group string, faultySync func()) {
	t.Helper()
	start := time.Now()
	faultySync()
	end := time.Now()

	// within the pause: served from the cache, no LISTGROUP
	upstreamWith(t, e, fakeserver.Options{})
	c := e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("within the retry pause: GROUP: %q", line)
	}
	got := xover(t, c, 1, 3)
	c.Cmd("QUIT")
	if d := time.Since(start); d+time.Second >= retryPause*time.Second {
		t.Fatalf("inconclusive: %v from the faulty synchronization to the "+
			"check, not within the retry pause (%d s)", d, retryPause)
	}
	if n := e.Upstream.Count("LISTGROUP"); n != 0 {
		t.Errorf("within the retry pause: %d LISTGROUP, want none: the chunk "+
			"is synchronized again right after an incomplete list", n)
	}
	wantNumbers(t, "within the retry pause: XOVER 1-3 (from the cache)", got, 1, 2, 3)

	// after the pause (its stamp is no newer than end): synchronized
	// again, article 2 is gone
	time.Sleep(time.Until(end.Add((retryPause+1)*time.Second + 100*time.Millisecond)))
	upstreamWith(t, e, fakeserver.Options{})
	c = e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("after the retry pause: GROUP: %q", line)
	}
	wantNumbers(t, "after the retry pause: XOVER 1-3 (article 2 deleted upstream)",
		xover(t, c, 1, 3), 1, 3)
	if code, line := c.Cmd("STAT 2"); code != 423 {
		t.Errorf("after the retry pause: STAT 2 (deleted upstream): %q, want 423", line)
	}
	c.Cmd("QUIT")
	if n := e.Upstream.Count("LISTGROUP"); n == 0 {
		t.Errorf("after the retry pause: no LISTGROUP: the incomplete " +
			"synchronization marked the chunk for the whole group timeout")
	}
	// (the faulty synchronization's stamp is no older than start: a
	// stamp for the whole timeout would still count here)
	if d := time.Since(start); d+time.Second >= retryPauseTimeout*time.Second {
		t.Fatalf("inconclusive: %v from the faulty synchronization to the "+
			"check after the pause, not within the group timeout (%d s)", d,
			retryPauseTimeout)
	}
}

// TestCachedIncompleteListgroupRetryPause: after a client's
// synchronization with a cut-off LISTGROUP, the chunk counts as
// synchronized for the retry pause only: a failing upstream isn't asked
// on every command, and a deleted article goes soon after.
func TestCachedIncompleteListgroupRetryPause(t *testing.T) {
	const group = "test.alpha"
	e := newEnv(t)
	e.GroupTimeout = retryPauseTimeout
	e.addArticles(group, 3)

	// the chunk, cached and synchronized
	c := e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	wantNumbers(t, "first XOVER 1-3", xover(t, c, 1, 3), 1, 2, 3)
	c.Cmd("QUIT")

	if !e.Store.RemoveArticle(group, 2) {
		t.Fatal("remove article 2 upstream")
	}
	// the first synchronization's timeout runs out
	time.Sleep((retryPauseTimeout+1)*time.Second + 100*time.Millisecond)

	wantRetryPause(t, e, group, func() {
		upstreamWith(t, e, fakeserver.Options{Faults: cutOffListgroup})
		c := e.session()
		if code, line := c.Cmd("GROUP %s", group); code != 211 {
			t.Fatalf("faulty upstream: GROUP: %q", line)
		}
		got := xover(t, c, 1, 3)
		c.Cmd("QUIT")
		wantNumbers(t, "faulty upstream: XOVER 1-3 (from the cache)", got, 1, 2, 3)
		if n := e.Upstream.Count("LISTGROUP"); n == 0 {
			t.Fatal("faulty upstream: no LISTGROUP: the chunk wasn't synchronized")
		}
	})
}

// TestUpdatenewsIncompleteListgroupNoStamp: updatenews -g and -o leave
// a chunk whose LISTGROUP was cut off unmarked, also with a long group
// timeout: the next session synchronizes it and no longer lists the
// article the news server deleted. (updatenews marked it for the whole
// timeout; and a back-dated retry stamp it wrote, now - 45 s under its
// own group timeout of 60 s, counted for almost the whole 3600 s in the
// sessions.)
func TestUpdatenewsIncompleteListgroupNoStamp(t *testing.T) {
	const group = "test.alpha"
	for _, mode := range []string{"-g", "-o"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			e.GroupTimeout = 3600
			e.addArticles(group, 3)
			prefetch := filepath.Join(e.Dir, "prefetch")
			if err := os.WriteFile(prefetch, []byte(group+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			e.Extra = "PrefetchFile " + prefetch
			bin := updatenewsBin(t, e)

			// The articles, prefetched without a mark: the upstream
			// doesn't support LISTGROUP (500), and updatenews -g marks
			// a chunk only after a LISTGROUP. (Any mark would count
			// for the whole hour in the sessions.)
			upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
				Override: map[string][]string{"LISTGROUP": {"500 unsupported"}},
			}})
			e.writeConf()
			if out, err := exec.Command(bin, "-c", e.Conf, "-g").CombinedOutput(); err != nil {
				dumpUpdatenewsFailure(t, e, string(out))
				t.Fatalf("updatenews -g: %v", err)
			}
			if n := e.Upstream.Count("ARTICLE"); n < 3 {
				t.Fatalf("updatenews -g fetched %d articles, want 3", n)
			}

			if !e.Store.RemoveArticle(group, 2) {
				t.Fatal("remove article 2 upstream")
			}

			// the run with the cut-off LISTGROUP (its exit status is
			// not what this test is about)
			upstreamWith(t, e, fakeserver.Options{Faults: cutOffListgroup})
			e.writeConf()
			out, err := exec.Command(bin, "-c", e.Conf, mode).CombinedOutput()
			t.Logf("faulty upstream: updatenews %s: %v\n%s", mode, err, out)
			if n := e.Upstream.Count("LISTGROUP"); n == 0 {
				t.Fatalf("faulty upstream: updatenews %s sent no LISTGROUP", mode)
			}

			upstreamWith(t, e, fakeserver.Options{})
			c := e.session()
			if code, line := c.Cmd("GROUP %s", group); code != 211 {
				t.Fatalf("working upstream: GROUP: %q", line)
			}
			wantNumbers(t, "working upstream: XOVER 1-3 (article 2 deleted upstream)",
				xover(t, c, 1, 3), 1, 3)
			if code, line := c.Cmd("STAT 2"); code != 423 {
				t.Errorf("working upstream: STAT 2 (deleted upstream): %q, want 423", line)
			}
			c.Cmd("QUIT")
			if n := e.Upstream.Count("LISTGROUP"); n == 0 {
				t.Errorf("working upstream: no LISTGROUP: updatenews %s marked the "+
					"chunk after an incomplete LISTGROUP", mode)
			}
		})
	}
}
