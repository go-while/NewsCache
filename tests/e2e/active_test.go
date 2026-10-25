package e2e

import (
	"strings"
	"testing"
)

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
