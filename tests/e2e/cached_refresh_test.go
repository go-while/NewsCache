package e2e

import (
	"os"
	"testing"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
)

// A failed refresh of a cached group's chunk (the default mode). The
// refresh ends with an error that is not a SystemError when the upstream
// answers OVER with 502 (or 412), or with 403 to OVER and then to XOVER
// ("neither OVER nor XOVER"); a std::bad_alloc ends it with an exception
// that is no Error at all. Each was taken as "the index holds the chunk":
// the index's records, none here, were printed as the complete list
// (224 and "." with every record missing, or 423 for OVER and HDR), and
// STAT answered 423 for an article the upstream has. The chunk is now
// asked for from the upstream again; when that fails too, the client gets
// 403 before anything was sent (a partial list is cut off instead, see
// TestCachedOverFailsAfterFirstChunk).

// cachedRefreshCmds: the commands that print a cached range through the
// chunk refresh, and STAT, which looks one number up through it.
var cachedRefreshCmds = []string{"XOVER 1-3", "OVER 1-3", "XHDR Subject 1-3",
	"HDR Subject 1-3", "XPAT Subject 1-3 *", "STAT 2"}

// readList reads the block of a list reply, if code is one.
func readList(c *client.Conn, code int) {
	switch code {
	case 221, 224, 225:
		c.ReadDotLines()
	}
}

// TestCachedRefreshRefused: the upstream refuses OVER every time. Every
// command gets 403, never an empty list or a 423. A later session with a
// working upstream gets the records: the failure did not mark the chunk
// as synchronized.
func TestCachedRefreshRefused(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override map[string][]string
	}{
		{"502", map[string][]string{"OVER": {"502 upstream denied"}}},
		{"412", map[string][]string{"OVER": {"412 no newsgroup selected"}}},
		{"403-over-and-xover", map[string][]string{"OVER": {"403 internal fault"},
			"XOVER": {"403 internal fault"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.addArticles("test.alpha", 3)
			upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{Override: tc.override}})
			c := e.session()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			for _, cmd := range cachedRefreshCmds {
				code, line := c.Cmd("%s", cmd)
				readList(c, code)
				if code != 403 {
					t.Errorf("%s with the upstream refusing OVER: %q, want 403", cmd, line)
				}
			}
			if code, line := c.Cmd("DATE"); code != 111 {
				t.Errorf("DATE after the 403s: %q", line)
			}
			c.Cmd("QUIT")

			upstreamWith(t, e, fakeserver.Options{})
			c = e.session()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("working upstream: GROUP: %q", line)
			}
			wantNumbers(t, "working upstream: XOVER 1-3", xover(t, c, 1, 3), 1, 2, 3)
			if code, line := c.Cmd("STAT 2"); code != 223 {
				t.Errorf("working upstream: STAT 2: %q, want 223", line)
			}
			c.Cmd("QUIT")
		})
	}
}

// TestCachedRefreshRecovers: the upstream refuses only the first OVER of
// the connection (502). The command that hit it gets the records from the
// second OVER, and the next command finds them too.
func TestCachedRefreshRecovers(t *testing.T) {
	for _, first := range []string{"XOVER", "STAT"} {
		t.Run(first, func(t *testing.T) {
			e := newEnv(t)
			e.addArticles("test.alpha", 3)
			upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
				Override:      map[string][]string{"OVER": {"502 upstream denied"}},
				OverrideTimes: map[string]int{"OVER": 1},
			}})
			c := e.session()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			stat := func() {
				t.Helper()
				if code, line := c.Cmd("STAT 2"); code != 223 {
					t.Errorf("STAT 2: %q, want 223", line)
				}
			}
			if first == "STAT" {
				stat()
				wantNumbers(t, "XOVER 1-3", xover(t, c, 1, 3), 1, 2, 3)
			} else {
				wantNumbers(t, "XOVER 1-3", xover(t, c, 1, 3), 1, 2, 3)
				stat()
			}
			c.Cmd("QUIT")
			if n := e.Upstream.Count("OVER"); n < 2 {
				t.Errorf("upstream got %d OVER, want the refused one and another", n)
			}
		})
	}
}

// TestCachedRefreshUnexpectedException: the first chunk fetch of each
// session throws std::bad_alloc (the test build's NEWSCACHE_TEST_FRESHEN_FAULT
// hook), which freshen's catch(...) took as success. XOVER and STAT get the
// records from the upstream instead.
func TestCachedRefreshUnexpectedException(t *testing.T) {
	if os.Getenv("NEWSCACHE_FRESHEN_FAULT_TEST") != "1" {
		t.Skip("requires a NEWSCACHE_TEST_FRESHEN_FAULT build (NEWSCACHE_FRESHEN_FAULT_TEST=1)")
	}
	e := newEnv(t)
	e.addArticles("test.alpha", 3)
	t.Setenv("NEWSCACHE_TEST_FRESHEN_BAD_ALLOC", "1")
	const marker = "UNEXPECTED EXCEPTION WHILE UPDATING OVERVIEW DATABASE"

	c := e.session()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	wantNumbers(t, "XOVER 1-3", xover(t, c, 1, 3), 1, 2, 3)
	c.Cmd("QUIT")
	// the hook did throw (a build without it would pass the above)
	if mode, err := logMode(e.Bin); err == nil && mode == "file" {
		e.waitLog(marker)
	}

	// a new process, the hook throws again: STAT's lookup
	c = e.session()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if code, line := c.Cmd("STAT 2"); code != 223 {
		t.Errorf("STAT 2: %q, want 223", line)
	}
	c.Cmd("QUIT")
}
