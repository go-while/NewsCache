package e2e

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// An incomplete upstream reply must not become an empty successful range.
// A first-chunk failure must return 403 and leave the session usable.
func TestUpstreamCutOffRangeFails(t *testing.T) {
	for _, tc := range []struct {
		name, command, upstream, options string
		fallback                         bool
	}{
		{"over", "OVER 1-1", "OVER", "not-cached", false},
		{"hdr", "HDR Subject 1-1", "OVER", "not-cached", false},
		{"xover", "XOVER 1-1", "OVER", "not-cached", false},
		{"xhdr", "XHDR Subject 1-1", "OVER", "not-cached", false},
		{"xpat", "XPAT Subject 1-1 *", "OVER", "not-cached", false},
		{"listgroup-cached", "LISTGROUP test.alpha 1-1", "LISTGROUP", "", false},
		{"listgroup-not-cached", "LISTGROUP test.alpha 1-1", "LISTGROUP", "not-cached", false},
		{"fallback-cached", "LISTGROUP test.alpha 1-1", "OVER", "", true},
		{"fallback-not-cached", "LISTGROUP test.alpha 1-1", "OVER", "not-cached", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.addArticles("test.alpha", 1)
			e.ServerOptions = tc.options
			faults := fakeserver.Faults{DropInMultiline: map[string]int{tc.upstream: 1}}
			if tc.fallback {
				faults.Override = map[string][]string{"LISTGROUP": {"500 unsupported"}}
			}
			upstreamWith(t, e, fakeserver.Options{Faults: faults})
			e.standalone()
			c := e.dial()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			if err := c.Conn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintf(c.Conn, "%s\r\n", tc.command); err != nil {
				t.Fatal(err)
			}
			status, err := c.R.ReadString('\n')
			if err != nil {
				t.Fatalf("status: %q: %v", status, err)
			}
			if !strings.HasPrefix(status, "403 ") {
				t.Fatalf("upstream failure returned %q, want 403", status)
			}
			if code, line := c.Cmd("DATE"); code != 111 {
				t.Errorf("DATE after 403: %q", line)
			}
		})
	}
}

// An access refusal is not evidence that LISTGROUP is unsupported and must
// not be hidden by falling back to OVER.
func TestUpstreamListgroupDeniedFails(t *testing.T) {
	for _, options := range []string{"", "not-cached"} {
		t.Run(firstNonEmpty(options, "cached"), func(t *testing.T) {
			e := newEnv(t)
			e.ServerOptions = options
			e.addArticles("test.alpha", 1)
			upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
				Override: map[string][]string{"LISTGROUP": {"502 upstream denied"}},
			}})
			e.standalone()
			c := e.dial()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			if err := c.Conn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(c.Conn, "LISTGROUP test.alpha 1-1\r\n"); err != nil {
				t.Fatal(err)
			}
			status, err := c.R.ReadString('\n')
			if err != nil || !strings.HasPrefix(status, "403 ") {
				t.Fatalf("LISTGROUP status: %q, %v", status, err)
			}
			if code, line := c.Cmd("DATE"); code != 111 {
				t.Errorf("DATE after 403: %q", line)
			}
			if n := e.Upstream.Count("OVER"); n != 0 {
				t.Errorf("upstream 502 caused %d OVER fallbacks", n)
			}
		})
	}
}

// Deferring success must not change the existing wire reply for an empty
// range: legacy commands return an empty list, OVER and HDR return 423.
func TestRangeEmptyRepliesUnchanged(t *testing.T) {
	for _, options := range []string{"", "not-cached"} {
		t.Run(firstNonEmpty(options, "cached"), func(t *testing.T) {
			e := newEnv(t)
			e.ServerOptions = options
			e.addArticles("test.alpha", 1)
			c := e.session()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			for _, tc := range []struct {
				command string
				code    int
				list    bool
			}{
				{"XOVER 2-2", 224, true},
				{"XHDR Subject 2-2", 221, true},
				{"XPAT Subject 2-2 *", 221, true},
				{"LISTGROUP test.alpha 2-2", 211, true},
				{"OVER 2-2", 423, false},
				{"HDR Subject 2-2", 423, false},
			} {
				code, line := c.Cmd("%s", tc.command)
				if code != tc.code {
					t.Fatalf("%s: %q, want %d", tc.command, line, tc.code)
				}
				if tc.list {
					if lines := c.ReadDotLines(); len(lines) != 0 {
						t.Errorf("%s: got %q, want an empty list", tc.command, lines)
					}
				}
			}
		})
	}
}
