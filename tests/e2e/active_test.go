package e2e

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
	"github.com/go-while/nntp-testkit/proc"
)

// A dead secondary upstream must not make every client re-download the
// healthy server's active list. In the short-limit build, also prove that
// the retry actually happens after the shortened retry interval.
func TestIncompleteActiveRetry(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	_, port, err := net.SplitHostPort(deadAddr(t))
	if err != nil {
		t.Fatal(err)
	}
	e.Extra = "Timeouts 300 3600"
	e.ExtraServers = fmt.Sprintf(`  Server 127.0.0.1 %s {
    Read test.dead.*
    PostTo test.dead.*
    Retries 0
    ConnectTimeout 1
  }`, port)
	c := e.session()
	list := func() {
		t.Helper()
		if code, line := c.Cmd("LIST ACTIVE"); code != 215 {
			t.Fatalf("LIST ACTIVE: %q", line)
		}
		if lines := c.ReadDotLines(); !containsPrefix(lines, "test.alpha ") {
			t.Fatalf("LIST ACTIVE missing healthy server's group: %q", lines)
		}
	}
	activeRequests := func() int {
		n := 0
		for _, line := range e.Upstream.Lines() {
			if strings.HasPrefix(strings.ToLower(line), "list active") {
				n++
			}
		}
		return n
	}
	list()
	initial := activeRequests()
	if initial == 0 {
		t.Fatal("healthy upstream was not asked for LIST ACTIVE")
	}
	list()
	if got := activeRequests(); got != initial {
		t.Fatalf("healthy upstream refetched immediately: %d then %d", initial, got)
	}
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") == "1" {
		time.Sleep(2300 * time.Millisecond)
		list()
		if got := activeRequests(); got <= initial {
			t.Fatalf("incomplete active list was not retried: %d then %d", initial, got)
		}
	}
}

// TestLongGroupName: the upstream offers a group whose name is longer than
// NewsCache's limit (511 characters). GROUP for it is refused with 411, the
// session goes on, and the other groups are still listed.
func TestLongGroupName(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	parts := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		parts = append(parts, strings.Repeat(string(rune('a'+i)), 200))
	}
	long := strings.Join(parts, ".")
	e.Store.AddGroup(long, "a group with a 602-character name")
	e.Store.AddArticle(long, groupArticle(long, 1, []string{"body"}))

	c := e.session()
	if code, line := c.Cmd("GROUP %s", long); code != 411 {
		t.Errorf("GROUP <%d characters>: %.70q, want 411", len(long), line)
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Fatalf("DATE after GROUP <long>: %q", line)
	}
	code, line := c.Cmd("LIST")
	if code != 215 {
		t.Fatalf("LIST: %q", line)
	}
	if lines := c.ReadDotLines(); !containsPrefix(lines, "test.alpha ") {
		t.Errorf("LIST lacks test.alpha when the upstream also has a group with a long name (%d lines)", len(lines))
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP test.alpha: %q", line)
	}
}

func containsPrefix(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// LIST ACTIVE.TIMES and NEWGROUPS report the time NewsCache first stored a
// group locally; no upstream ACTIVE.TIMES lookup is used to backdate it.
func TestActiveTimesUseNewsCacheFirstSeenTime(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	c := e.session()
	before := time.Now().Unix()
	if code, line := c.Cmd("LIST ACTIVE test.alpha"); code != 215 {
		t.Fatalf("LIST ACTIVE: %q, want 215", line)
	}
	active := c.ReadDotLines()
	after := time.Now().Unix()
	code, line := c.Cmd("LIST ACTIVE.TIMES test.alpha")
	if code != 215 {
		t.Fatalf("LIST ACTIVE.TIMES: %q, want 215", line)
	}
	times := c.ReadDotLines()
	if len(times) != 1 {
		t.Fatalf("LIST ACTIVE.TIMES: %q, want one group", times)
	}
	fields := strings.Fields(times[0])
	if len(fields) != 3 || fields[0] != "test.alpha" || fields[2] != "news" {
		t.Fatalf("LIST ACTIVE.TIMES line %q, want name time creator", times[0])
	}
	created, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		t.Fatalf("LIST ACTIVE.TIMES timestamp %q: %v", fields[1], err)
	}
	if created < before-1 || created > after+1 {
		t.Errorf("cached first-seen time %d is outside [%d, %d]", created, before-1, after+1)
	}
	if len(active) != 1 || active[0] != "test.alpha 1 1 y" {
		t.Fatalf("LIST ACTIVE: %q, want test.alpha 1 1 y", active)
	}
	code, line = c.Cmd("NEWGROUPS %s GMT", time.Unix(before-2, 0).UTC().Format("20060102 150405"))
	if code != 231 {
		t.Fatalf("NEWGROUPS before first sighting: %q, want 231", line)
	}
	if got := c.ReadDotLines(); len(got) != 1 || got[0] != active[0] {
		t.Errorf("NEWGROUPS and LIST ACTIVE disagree: %q vs %q", got, active)
	}
	for _, upstreamLine := range e.Upstream.Lines() {
		if strings.Contains(strings.ToLower(upstreamLine), "active.times") {
			t.Errorf("NewsCache requested upstream creation times: %q", upstreamLine)
		}
	}
}

// TestLongReadPattern: RServer::active copied each element of a server's
// Read list into a 513-byte stack buffer without a length check, at the
// first LIST ACTIVE <wildmat> to the upstream. A Read element of 2000
// characters overwrote the stack (the session aborted).
func TestLongReadPattern(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.ServerLines = "    Read test.*," + strings.Repeat("x", 2000) + ".*"
	c := e.session()
	code, line := c.Cmd("LIST")
	if code != 215 {
		t.Fatalf("LIST with a Read element of 2000 characters: %q", line)
	}
	if lines := c.ReadDotLines(); !containsPrefix(lines, "test.alpha ") {
		t.Errorf("LIST lacks test.alpha (%d lines)", len(lines))
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP test.alpha: %q", line)
	}
	want := "list active " + strings.Repeat("x", 2000) + ".*"
	if !containsLine(e.Upstream.Lines(), want) {
		t.Errorf("the upstream didn't get the whole LIST ACTIVE command")
	}
}

// TestActiveListGrowsDuringList: a client reads its LIST ACTIVE slowly
// while another session refreshes the active list and new groups make the
// file grow. The list walked the database without a lock, ran into the
// new records past its mapping and took the sound file for damaged: the
// slow client got part of the list ending in "." as if complete, the file
// was moved to .active64.bad, and NEWGROUPS then listed every group as
// new.
func TestActiveListGrowsDuringList(t *testing.T) {
	const n = 20000
	e := newEnv(t)
	e.Extra += "Timeouts 1 3600\n"
	for i := 0; i < n; i++ {
		e.Store.AddGroup(fmt.Sprintf("test.first.group%05d", i), "g")
	}
	s0 := e.session()
	s0.Deadline = 60 * time.Second
	if code, line := s0.Cmd("LIST ACTIVE"); code != 215 {
		t.Fatalf("LIST ACTIVE: %q", line)
	}
	s0.ReadDotLines()
	s0.Cmd("QUIT")
	// NEWGROUPS has second resolution: the second groups come later
	time.Sleep(2 * time.Second)
	since := time.Now().UTC()
	time.Sleep(1100 * time.Millisecond)

	// A asks for the list and doesn't read it yet
	pa := proc.StartStdio(t, e.procOpts("-i"))
	a := client.New(t, pa.Conn())
	a.Deadline = 60 * time.Second
	if _, err := a.ReadGreeting(); err != nil {
		t.Fatal(err)
	}
	if err := a.Send("LIST ACTIVE\r\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)

	// B: the list is due again (Timeouts 1); the new groups grow the file
	for i := 0; i < n; i++ {
		e.Store.AddGroup(fmt.Sprintf("test.second.group%05d", i), "g")
	}
	b := e.session()
	b.Deadline = 60 * time.Second
	if code, line := b.Cmd("LIST ACTIVE"); code != 215 {
		t.Fatalf("B: LIST ACTIVE: %q", line)
	}
	if l := b.ReadDotLines(); len(l) != 2*n {
		t.Errorf("B: LIST ACTIVE: %d groups, want %d", len(l), 2*n)
	}
	b.Cmd("QUIT")

	if code, line, err := a.ReadStatus(); code != 215 {
		t.Fatalf("A: LIST ACTIVE: %d %q %v", code, line, err)
	}
	l, err := a.TryReadDotLines()
	if err != nil {
		t.Fatalf("A: LIST ACTIVE: %v after %d groups", err, len(l))
	}
	if len(l) != n {
		t.Errorf("A: LIST ACTIVE: %d groups, want %d (the list before B's refresh)", len(l), n)
	}
	a.Cmd("QUIT")

	if _, err := os.Stat(filepath.Join(e.Spool, ".active64.bad")); err == nil {
		t.Errorf("the sound active database was moved to .active64.bad")
	}
	c := e.session()
	c.Deadline = 60 * time.Second
	code, line := c.Cmd("NEWGROUPS %s GMT", since.Format("20060102 150405"))
	if code != 231 {
		t.Fatalf("NEWGROUPS: %q", line)
	}
	if l := c.ReadDotLines(); len(l) != n || !containsPrefix(l, "test.second.") ||
		containsPrefix(l, "test.first.") {
		t.Errorf("NEWGROUPS since the first list: %d groups, want the %d added later", len(l), n)
	}
	c.Cmd("QUIT")
}

// A GROUP that refreshes the group's numbers from the news server keeps the
// posting flag the active list gave the group (it was set to 'y').
func TestGroupKeepsActiveFlag(t *testing.T) {
	e := newEnv(t)
	e.Extra = "Timeouts 3600 3600"
	var groups atomic.Int32
	upstreamWith(t, e, fakeserver.Options{NoGreeting: true, Handler: func(c *fakeserver.Conn) {
		c.WriteLines("200 fake ready")
		for {
			line, err := c.ReadLine()
			if err != nil {
				return
			}
			low := strings.ToLower(line)
			switch {
			case strings.HasPrefix(low, "mode"):
				c.WriteLines("200 reader")
			case strings.HasPrefix(low, "list overview.fmt"):
				c.WriteLines("215 order", "Subject:", "From:", "Date:", "Message-ID:", "References:", ":bytes", ":lines", ".")
			case strings.HasPrefix(low, "list active"):
				c.WriteLines("215 list", "test.alpha 3 1 m", "test.beta 3 1 n", ".")
			case strings.HasPrefix(low, "group test.alpha"):
				groups.Add(1)
				c.WriteLines("211 4 1 4 test.alpha")
			case strings.HasPrefix(low, "group test.beta"):
				groups.Add(1)
				c.WriteLines("211 4 1 4 test.beta")
			case strings.HasPrefix(low, "quit"):
				c.WriteLines("205 bye")
				return
			default:
				c.WriteLines("500 what")
			}
		}
	}})
	s := e.session()
	flags := func() map[string]string {
		t.Helper()
		if code, line := s.Cmd("LIST ACTIVE"); code != 215 {
			t.Fatalf("LIST ACTIVE: %q", line)
		}
		got := map[string]string{}
		for _, l := range s.ReadDotLines() {
			if f := strings.Fields(l); len(f) == 4 {
				got[f[0]] = f[1] + " " + f[3]
			}
		}
		return got
	}
	if got := flags(); got["test.alpha"] != "3 m" || got["test.beta"] != "3 n" {
		t.Fatalf("list from the news server: %v", got)
	}
	// GroupTimeout 0: a GROUP a second later asks the news server
	time.Sleep(1100 * time.Millisecond)
	for _, g := range []string{"test.alpha", "test.beta"} {
		if code, line := s.Cmd("GROUP %s", g); code != 211 {
			t.Fatalf("GROUP %s: %q", g, line)
		}
	}
	if n := groups.Load(); n != 2 {
		t.Fatalf("news server got %d GROUP commands, want 2", n)
	}
	if got := flags(); got["test.alpha"] != "4 m" || got["test.beta"] != "4 n" {
		t.Errorf("after GROUP (new last number, flag kept): %v", got)
	}
}

// TestActiveListKeepsNewerGroupNumbers: a LIST ACTIVE that is still being
// read when another session's GROUP stores newer numbers doesn't put the
// list's older ones back. It did: the group's last number went back for
// GroupTimeout (150, then 100), and the next GROUP threw away the
// overview records above 100 that the other session had cached.
func TestActiveListKeepsNewerGroupNumbers(t *testing.T) {
	const group = "test.alpha"
	e := newEnv(t)
	e.GroupTimeout = 60
	var lists, overs atomic.Int32
	release := make(chan struct{})
	upstreamWith(t, e, fakeserver.Options{NoGreeting: true, Handler: func(c *fakeserver.Conn) {
		c.WriteLines("200 fake ready")
		for {
			line, err := c.ReadLine()
			if err != nil {
				return
			}
			low := strings.ToLower(line)
			f := strings.Fields(low)
			switch {
			case strings.HasPrefix(low, "mode"):
				c.WriteLines("200 reader")
			case strings.HasPrefix(low, "list overview.fmt"):
				c.WriteLines("215 order", "Subject:", "From:", "Date:", "Message-ID:", "References:", ":bytes", ":lines", ".")
			case strings.HasPrefix(low, "list active"):
				// the list as it was when asked for; the rest of
				// the reply comes when the test says so
				c.WriteLines("215 list", group+" 100 1 y")
				if lists.Add(1) == 1 {
					<-release
				}
				c.WriteLines(".")
			case strings.HasPrefix(low, "group "+group):
				c.WriteLines("211 150 1 150 " + group)
			case (f[0] == "over" || f[0] == "xover") && len(f) == 2:
				overs.Add(1)
				from, to := 1, 150
				if r := strings.SplitN(f[1], "-", 2); len(r) == 2 {
					from, _ = strconv.Atoi(r[0])
					if r[1] != "" {
						to, _ = strconv.Atoi(r[1])
					}
				}
				out := []string{"224 overview"}
				for n := max(from, 1); n <= min(to, 150); n++ {
					out = append(out, fmt.Sprintf("%d\tsubject %d\ta@b.test\tMon, 2 Nov 2026 10:00:00 +0000\t<%d.%s@fake>\t\t100\t1", n, n, n, group))
				}
				c.WriteLines(append(out, ".")...)
			case strings.HasPrefix(low, "quit"):
				c.WriteLines("205 bye")
				return
			default:
				c.WriteLines("500 what")
			}
		}
	}})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	// A asks for the list; its reply stops after the group's line.
	a := e.session()
	a.Deadline = 60 * time.Second
	if err := a.Send("LIST ACTIVE\r\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the news server receives LIST ACTIVE", func() bool { return lists.Load() == 1 })
	// B's GROUP is asked for in a later second than the list.
	time.Sleep(1100 * time.Millisecond)

	b := e.session()
	if code, line := b.Cmd("GROUP %s", group); code != 211 || !strings.HasPrefix(line, "211 150 1 150 ") {
		t.Fatalf("B: GROUP: %q", line)
	}
	if code, line := b.Cmd("XOVER 101-150"); code != 224 {
		t.Fatalf("B: XOVER 101-150: %q", line)
	}
	if got := b.ReadDotLines(); len(got) != 50 {
		t.Fatalf("B: XOVER 101-150: %d records, want 50", len(got))
	}
	b.Cmd("QUIT")
	fetched := overs.Load()
	if fetched == 0 {
		t.Fatalf("B's XOVER sent no OVER upstream")
	}

	released = true
	close(release)
	if code, line, err := a.ReadStatus(); code != 215 {
		t.Fatalf("A: LIST ACTIVE: %d %q %v", code, line, err)
	}
	if l, err := a.TryReadDotLines(); err != nil {
		t.Fatalf("A: LIST ACTIVE: %v", err)
	} else {
		t.Logf("A: LIST ACTIVE: %q", l)
	}
	a.Cmd("QUIT")

	c := e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 || !strings.HasPrefix(line, "211 150 1 150 ") {
		t.Errorf("C: GROUP after the list: %q, want 211 150 1 150 (B's newer numbers)", line)
	}
	if code, line := c.Cmd("XOVER 101-150"); code != 224 {
		t.Errorf("C: XOVER 101-150: %q", line)
	} else if got := c.ReadDotLines(); len(got) != 50 {
		t.Errorf("C: XOVER 101-150: %d records, want 50", len(got))
	}
	if n := overs.Load(); n != fetched {
		t.Errorf("C's XOVER sent %d OVER upstream, want 0 (B cached the records)", n-fetched)
	}
	c.Cmd("QUIT")
}

// TestActiveListOtherServersGroups: NewsCache asks each server for "LIST
// ACTIVE <pattern>" per pattern of its Read. The filter for that reply
// started with "*" and compared the other servers' patterns with the
// requested one in a way that lost its place after a partial match: with
// one server for "dk.*,de.alt.binaries.*" and one for "de.*", the reply to
// "LIST ACTIVE de.*" (which rightly has de.alt.binaries.x) put
// de.alt.binaries.x into the active list with the second server's numbers,
// although the group is routed to the first.
func TestActiveListOtherServersGroups(t *testing.T) {
	e := newEnv(t)
	e.Store.AddGroup("dk.test", "on the first server")
	e.ServerLines = "    Read dk.*,de.alt.binaries.*"
	st := fakeserver.NewStore()
	st.AddGroup("de.test", "on the second server")
	st.AddGroup("de.alt.binaries.x", "not routed to the second server")
	second := fakeserver.New(t, fakeserver.Options{Store: st})
	e.ExtraServers = serverBlock(second, "de.*")
	c := e.session()
	if code, line := c.Cmd("LIST ACTIVE"); code != 215 {
		t.Fatalf("LIST ACTIVE: %q", line)
	}
	lines := c.ReadDotLines()
	for _, g := range []string{"dk.test ", "de.test "} {
		if !containsPrefix(lines, g) {
			t.Errorf("LIST ACTIVE: no %s: %q", strings.TrimSpace(g), lines)
		}
	}
	if containsPrefix(lines, "de.alt.binaries.x ") {
		t.Errorf("LIST ACTIVE has de.alt.binaries.x from the server it isn't routed to: %q", lines)
	}
	asked := false
	for _, l := range second.Lines() {
		if strings.EqualFold(l, "list active de.*") {
			asked = true
		}
	}
	if !asked {
		t.Fatalf("second server not asked for LIST ACTIVE de.*: %q", second.Lines())
	}
}

// TestActiveListRouting: the active list keeps each group from the server
// GROUP goes to (the longest Read match, the first server on a tie).
// - An exclusion in another server's Read: the second server reads
//   de.sub.* but not de.sub.special.*, so de.sub.special.x goes to the
//   first (de.*). A filter that negated the other servers' patterns made
//   "!!de.sub.special.*" of it, which matched nothing, and the group was
//   missing from the list.
// - Two servers with the same Read: the group goes to the first, and so
//   do its numbers, not the second server's.
func TestActiveListRouting(t *testing.T) {
	t.Run("exclusion in another server's Read", func(t *testing.T) {
		e := newEnv(t)
		e.ServerLines = "    Read de.*"
		e.Store.AddGroup("de.sub.special.x", "routed to the first server")
		st := fakeserver.NewStore()
		st.AddGroup("de.sub.special.x", "excluded from the second server")
		st.AddGroup("de.sub.y", "routed to the second server")
		second := fakeserver.New(t, fakeserver.Options{Store: st})
		e.ExtraServers = serverBlock(second, "de.sub.*,!de.sub.special.*")
		c := e.session()
		if code, line := c.Cmd("LIST ACTIVE"); code != 215 {
			t.Fatalf("LIST ACTIVE: %q", line)
		}
		lines := c.ReadDotLines()
		for _, g := range []string{"de.sub.special.x ", "de.sub.y "} {
			if !containsPrefix(lines, g) {
				t.Errorf("LIST ACTIVE: no %s: %q", strings.TrimSpace(g), lines)
			}
		}
	})
	t.Run("same Read on two servers", func(t *testing.T) {
		e := newEnv(t)
		e.addArticles("test.tie", 1)
		e.ServerLines = "    Read test.*"
		st := fakeserver.NewStore()
		st.AddGroup("test.tie", "the same group on the second server")
		st.AddArticle("test.tie", groupArticle("test.tie", 1, []string{"body"}))
		st.AddArticle("test.tie", groupArticle("test.tie", 2, []string{"body"}))
		second := fakeserver.New(t, fakeserver.Options{Store: st})
		e.ExtraServers = serverBlock(second, "test.*")
		c := e.session()
		if code, line := c.Cmd("LIST ACTIVE"); code != 215 {
			t.Fatalf("LIST ACTIVE: %q", line)
		}
		lines := c.ReadDotLines()
		if !containsPrefix(lines, "test.tie 1 1 ") {
			t.Errorf("LIST ACTIVE: test.tie not with the first server's numbers: %q", lines)
		}
	})
}
