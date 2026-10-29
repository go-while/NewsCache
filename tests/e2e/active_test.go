package e2e

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/proc"
)

// A dead secondary upstream must not make every client re-download the
// healthy server's active list. In the short-limit build, also prove that
// the retry actually happens after the shortened retry interval.
func TestIncompleteActiveRetry(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	_, port, err := net.SplitHostPort(freeAddr(t))
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
