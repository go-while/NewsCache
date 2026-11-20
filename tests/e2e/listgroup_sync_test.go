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
// updatenews -g marked the chunk the same way. Now such a chunk stays
// unmarked, and the next synchronization removes the article.

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
// deleted article 2.
func wantSyncedWithout(t *testing.T, e *env, group string) {
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

			wantSyncedWithout(t, e, group)
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
			upstreamWith(t, e, fakeserver.Options{Faults: tc.faults})
			e.writeConf()
			out, err := exec.Command(bin, "-c", e.Conf, "-g").CombinedOutput()
			t.Logf("faulty upstream: updatenews -g: %v\n%s", err, out)

			wantSyncedWithout(t, e, group)
		})
	}
}
