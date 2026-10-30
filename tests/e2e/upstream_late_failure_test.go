package e2e

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// The first chunk has already produced a 224 and a record when the second
// upstream OVER fails. No normal dot terminator may follow that partial list.
func TestUpstreamOverFailsAfterFirstChunk(t *testing.T) {
	for _, command := range []string{"OVER", "XOVER"} {
		t.Run(command, func(t *testing.T) { testUpstreamOverFailsAfterFirstChunk(t, command) })
	}
}

func testUpstreamOverFailsAfterFirstChunk(t *testing.T, command string) {
	t.Helper()
	e := newEnv(t)
	e.ServerOptions = "not-cached"
	e.Store.AddGroup("test.alpha", "two overview chunks")
	for _, n := range []int64{9999, 10000} {
		e.Store.AddArticle("test.alpha", groupArticle("test.alpha", n, []string{"body"}))
	}
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		Override:     map[string][]string{"OVER": {"502 upstream denied"}},
		OverrideFrom: map[string]int{"OVER": 2},
	}})
	e.standalone()
	c := e.dial()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if err := c.Conn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(c.Conn, "%s 9999-10000\r\n", command); err != nil {
		t.Fatal(err)
	}
	status, err := c.R.ReadString('\n')
	if err != nil || !strings.HasPrefix(status, "224 ") {
		t.Fatalf("expected first chunk's status: %q, %v", status, err)
	}
	row, err := c.R.ReadString('\n')
	if err != nil || !strings.HasPrefix(row, "9999\t") {
		t.Fatalf("expected first chunk's record: %q, %v", row, err)
	}
	for {
		line, err := c.R.ReadString('\n')
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatalf("partial reply remained open: %v", err)
			}
			break
		}
		if line == ".\r\n" {
			t.Fatal("upstream failure after the first chunk ended as success")
		}
	}
}

// A LISTGROUP cut after its first number has already started the client
// reply, so it must close rather than claiming the list was complete.
func TestUpstreamListgroupFailsAfterFirstRecord(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 2)
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		DropInMultiline: map[string]int{"LISTGROUP": 2},
	}})
	e.standalone()
	c := e.dial()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if err := c.Conn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(c.Conn, "LISTGROUP test.alpha 1-2\r\n"); err != nil {
		t.Fatal(err)
	}
	status, err := c.R.ReadString('\n')
	if err != nil || !strings.HasPrefix(status, "211 ") {
		t.Fatalf("LISTGROUP status: %q, %v", status, err)
	}
	row, err := c.R.ReadString('\n')
	if err != nil || row != "1\r\n" {
		t.Fatalf("first number: %q, %v", row, err)
	}
	for {
		line, err := c.R.ReadString('\n')
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatalf("partial reply remained open: %v", err)
			}
			break
		}
		if line == ".\r\n" {
			t.Fatal("interrupted LISTGROUP ended as success")
		}
	}
}
