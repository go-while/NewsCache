package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const noPending = 100 * time.Millisecond

func TestGreetingAndQuit(t *testing.T) {
	e := newEnv(t)
	c := e.session()
	if !strings.HasPrefix(c.Greeting, "200 ") {
		t.Fatalf("greeting %q, want 200", c.Greeting)
	}
	c.Cmd("QUIT")
	if err := c.ExpectNoPending(noPending); err != nil {
		t.Fatal(err)
	}
}

func TestModeReader(t *testing.T) {
	e := newEnv(t)
	c := e.session()
	code, line := c.Cmd("MODE READER")
	if code != 200 && code != 201 {
		t.Fatalf("MODE READER: %q", line)
	}
}

func TestListActive(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 3)
	e.addArticles("test.beta", 1)
	c := e.session()
	code, line := c.Cmd("LIST")
	if code != 215 {
		t.Fatalf("LIST: %q", line)
	}
	lines := c.ReadDotLines()
	got := map[string]string{}
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) == 4 {
			got[f[0]] = strings.Join(f[1:], " ")
		}
	}
	for _, g := range []string{"test.alpha", "test.beta"} {
		if _, ok := got[g]; !ok {
			t.Errorf("LIST misses %s; got %q", g, lines)
		}
	}
}

func TestGroupArticleHeadBody(t *testing.T) {
	e := newEnv(t)
	ids := e.addArticles("test.alpha", 3)
	c := e.session()

	code, line := c.Cmd("GROUP test.alpha")
	if code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if f := strings.Fields(line); len(f) < 5 || f[1] != "3" || f[2] != "1" || f[3] != "3" {
		t.Errorf("GROUP: %q, want 211 3 1 3 test.alpha", line)
	}

	code, line = c.Cmd("ARTICLE 2")
	if code != 220 || !strings.Contains(line, ids[1]) {
		t.Fatalf("ARTICLE 2: %q", line)
	}
	art := c.ReadDotLines()
	if !containsLine(art, "Subject: article 2 in test.alpha") {
		t.Errorf("ARTICLE 2: subject missing: %q", art)
	}
	if art[len(art)-1] != ".starts with a dot" {
		t.Errorf("ARTICLE 2: last body line %q, want dot line un-stuffed", art[len(art)-1])
	}

	code, line = c.Cmd("HEAD %s", ids[0])
	if code != 221 {
		t.Fatalf("HEAD <id>: %q", line)
	}
	head := c.ReadDotLines()
	if containsLine(head, "") {
		t.Errorf("HEAD contains the blank separator line: %q", head)
	}

	code, line = c.Cmd("BODY 3")
	if code != 222 {
		t.Fatalf("BODY 3: %q", line)
	}
	body := c.ReadDotLines()
	want := []string{"body of article 3", "second line", ".starts with a dot"}
	if strings.Join(body, "\n") != strings.Join(want, "\n") {
		t.Errorf("BODY 3: %q, want %q", body, want)
	}
	if err := c.ExpectNoPending(noPending); err != nil {
		t.Fatal(err)
	}
}

func TestXover(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 5)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	code, line := c.Cmd("XOVER 1-5")
	if code != 224 {
		t.Fatalf("XOVER: %q", line)
	}
	ov := c.ReadDotLines()
	if len(ov) != 5 {
		t.Fatalf("XOVER: %d lines, want 5: %q", len(ov), ov)
	}
	for i, l := range ov {
		f := strings.Split(l, "\t")
		if len(f) < 8 {
			t.Errorf("XOVER line %d has %d fields: %q", i+1, len(f), l)
		}
	}
}

// TestXoverRange: a range must start with a number (RFC 3977 "n", "n-",
// "n-m"). "XOVER -5" used an uninitialized first number. An open range
// "n-" runs to the last article, for XOVER and XHDR.
func TestXoverRange(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 5)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	for _, cmd := range []string{"XOVER -5", "XOVER -", "XOVER x", "XOVER 1x", "XHDR subject -3"} {
		if code, line := c.Cmd("%s", cmd); code != 501 {
			if code/100 == 2 {
				c.ReadDotLines()
			}
			t.Errorf("%s: %q, want 501", cmd, line)
		}
	}
	if code, line := c.Cmd("XOVER 3-"); code != 224 {
		t.Fatalf("XOVER 3-: %q, want 224", line)
	}
	if ov := c.ReadDotLines(); len(ov) != 3 {
		t.Errorf("XOVER 3-: %d lines, want 3: %q", len(ov), ov)
	}
	code, line := c.Cmd("XHDR subject 4-")
	if code != 221 {
		t.Fatalf("XHDR subject 4-: %q, want 221", line)
	}
	if strings.Contains(line, "--") {
		t.Errorf("XHDR subject 4-: %q shows a negative last number", line)
	}
	if hd := c.ReadDotLines(); len(hd) != 2 {
		t.Errorf("XHDR subject 4-: %d lines, want 2: %q", len(hd), hd)
	}
}

func TestNoSuchArticle(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	if code, line := c.Cmd("ARTICLE 99"); code != 423 {
		t.Errorf("ARTICLE 99: %q, want 423", line)
	}
	if code, line := c.Cmd("ARTICLE <missing@upstream.test>"); code != 430 {
		t.Errorf("ARTICLE <missing>: %q, want 430", line)
	}
	if code, line := c.Cmd("GROUP no.such.group"); code != 411 {
		t.Errorf("GROUP no.such.group: %q, want 411", line)
	}
}

// TestStat: STAT n answers 223 with the article's message-ID and makes n
// the current article; a number with no article gets 423 (it got
// "223 n  status", without a message-ID). RFC 3977 6.2.4.
func TestStat(t *testing.T) {
	const group = "test.gap"
	e := newEnv(t)
	e.Store.AddGroup(group, "articles 1, 2 and 4")
	for _, n := range []int64{1, 2, 4} {
		e.Store.AddArticle(group, groupArticle(group, n, []string{fmt.Sprintf("body %d", n)}))
	}
	e.Store.AddGroup("test.empty", "no articles")
	id := func(n int) string { return fmt.Sprintf("<test-gap.%d@upstream.test>", n) }

	c := e.session()
	if code, line := c.Cmd("STAT 1"); code != 412 {
		t.Errorf("STAT 1 without a group: %q, want 412", line)
	}
	c.Cmd("GROUP %s", group)
	for _, tc := range []struct {
		cmd  string
		want string
	}{
		{"STAT 2", "223 2 " + id(2)},
		{"STAT 3", "423 "},  // gap
		{"STAT 99", "423 "}, // after the last article
		{"STAT", "223 2 " + id(2)},
		{"NEXT", "223 4 " + id(4)},
		{"STAT 1", "223 1 " + id(1)},
		{"NEXT", "223 2 " + id(2)}, // STAT 1 set the current article
		{"STAT " + id(4), "223 0 " + id(4)},
		{"STAT <missing@upstream.test>", "430 "},
		{"STAT 1x", "501 "},
	} {
		if _, line := c.Cmd("%s", tc.cmd); !strings.HasPrefix(line, tc.want) {
			t.Errorf("%s: %q, want %q", tc.cmd, line, tc.want)
		}
	}
	// a failed STAT keeps the current article
	c.Cmd("STAT 3")
	if _, line := c.Cmd("STAT"); !strings.HasPrefix(line, "223 2 "+id(2)) {
		t.Errorf("STAT after STAT 3: %q, want 223 2 %s", line, id(2))
	}
	c.Cmd("GROUP test.empty")
	if code, line := c.Cmd("STAT"); code != 420 {
		t.Errorf("STAT in an empty group: %q, want 420", line)
	}
}

// TestArticleCached: once fetched by number, an article is served from the
// spool; a second NewsCache process on the same spool does not ask upstream.
func TestArticleCached(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 2)

	c := e.session()
	c.Cmd("GROUP test.alpha")
	if code, line := c.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1: %q", line)
	}
	c.ReadDotLines()
	c.Cmd("QUIT")
	first := e.Upstream.Count("ARTICLE")
	if first != 1 {
		t.Fatalf("upstream ARTICLE count %d after the first fetch, want 1 (counts %v)", first, e.Upstream.Counts())
	}

	c2 := e.session()
	c2.Cmd("GROUP test.alpha")
	if code, line := c2.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1 from cache: %q", line)
	}
	c2.ReadDotLines()
	if n := e.Upstream.Count("ARTICLE"); n != first {
		t.Errorf("upstream ARTICLE count went from %d to %d; want served from cache (counts %v)", first, n, e.Upstream.Counts())
	}
}

// TestOfflineServesCache: with the upstream gone, cached articles are still
// served.
func TestOfflineServesCache(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 2)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	c.Cmd("ARTICLE 1")
	c.ReadDotLines()
	c.Cmd("QUIT")

	e.Upstream.Close()
	c2 := e.session()
	if code, line := c2.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP with upstream down: %q", line)
	}
	if code, line := c2.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1 with upstream down: %q", line)
	}
	c2.ReadDotLines()
}

func TestPostForwarded(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	c := e.session()
	code, line := c.Cmd("POST")
	if code != 340 {
		t.Fatalf("POST: %q", line)
	}
	if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n" +
		"Subject: posted through newscache\r\nMessage-ID: <post1@newscache.test>\r\n\r\n" +
		"hello\r\n..dot line\r\n.\r\n"); err != nil {
		t.Fatal(err)
	}
	code, line, err := c.ReadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if code != 240 {
		t.Fatalf("POST result: %q", line)
	}
	posted := e.Store.Posted()
	if len(posted) != 1 {
		t.Fatalf("upstream got %d posted articles, want 1", len(posted))
	}
	if b := posted[0].Body; len(b) != 2 || b[1] != ".dot line" {
		t.Errorf("posted body %q, want [hello .dot line]", b)
	}
}

func TestUnknownCommand(t *testing.T) {
	e := newEnv(t)
	c := e.session()
	if code, line := c.Cmd("FOOBAR"); code != 500 {
		t.Errorf("FOOBAR: %q, want 500", line)
	}
	if err := c.ExpectNoPending(noPending); err != nil {
		t.Fatal(err)
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// TestDate: DATE answers 111 with the current UTC time as yyyymmddhhmmss.
func TestDate(t *testing.T) {
	e := newEnv(t)
	c := e.session()
	code, line := c.Cmd("DATE")
	if code != 111 {
		t.Fatalf("DATE: %q, want 111", line)
	}
	f := strings.Fields(line)
	if len(f) != 2 {
		t.Fatalf("DATE: %q, want 111 yyyymmddhhmmss", line)
	}
	got, err := time.Parse("20060102150405", f[1])
	if err != nil {
		t.Fatalf("DATE: %q: %v", line, err)
	}
	if d := time.Since(got); d < -time.Minute || d > time.Minute {
		t.Errorf("DATE: %q is %v away from now", line, d)
	}
	// DATE with an argument named the GROUP syntax
	if code, line := c.Cmd("DATE x"); code != 501 || !strings.Contains(strings.ToLower(line), "date") {
		t.Errorf("DATE x: %q, want 501 with the DATE syntax", line)
	}
}

// TestNewgroups: NEWGROUPS with a two-digit year (local time and GMT) and a
// four-digit year answers 231 and a list.
func TestNewgroups(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	c := e.session()
	for _, args := range []string{"260101 000000", "260101 000000 GMT", "20260101 000000 GMT"} {
		code, line := c.Cmd("NEWGROUPS %s", args)
		if code != 231 {
			t.Fatalf("NEWGROUPS %s: %q, want 231", args, line)
		}
		c.ReadDotLines()
	}
	if code, line := c.Cmd("NEWGROUPS 2601 000000"); code != 501 {
		t.Errorf("NEWGROUPS with a bad date: %q, want 501", line)
	}
}

// TestPostMessageIDLongHostname: for a post without a Message-ID, NewsCache
// generates one from its host name. With a long Hostname (255 characters
// are allowed) the whole header must still come through, including its
// closing ">". (The buffers are now sized from the host name limit.)
func TestPostMessageIDLongHostname(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	host := strings.Repeat("h", 60) + "." + strings.Repeat("o", 60) + "." +
		strings.Repeat("s", 60) + "." + strings.Repeat("t", 60) + ".test"
	e.Extra = "Hostname " + host
	c := e.session()
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("POST: %q", line)
	}
	if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n" +
		"Subject: no message-id\r\n\r\nhello\r\n.\r\n"); err != nil {
		t.Fatal(err)
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 240 {
		t.Fatalf("POST result: %q %v", line, err)
	}
	posted := e.Store.Posted()
	if len(posted) != 1 {
		t.Fatalf("upstream got %d posted articles, want 1", len(posted))
	}
	var mid string
	for _, h := range posted[0].Headers {
		if strings.HasPrefix(strings.ToLower(h), "message-id:") {
			mid = strings.TrimSpace(h[len("message-id:"):])
		}
	}
	if mid == "" {
		mid = posted[0].MessageID
	}
	if !strings.HasPrefix(mid, "<") || !strings.HasSuffix(mid, "@"+host+">") {
		t.Errorf("generated Message-ID %q (%d characters), want <...@%s>", mid, len(mid), host[:20]+"...")
	}
}

// TestXpat: XPAT header range|<message-id> pattern (RFC 2980 2.9) gives
// the XHDR lines whose value matches the wildmat pattern; more than one
// pattern argument form one pattern, joined with spaces.
func TestXpat(t *testing.T) {
	e := newEnv(t)
	ids := e.addArticles("test.alpha", 3)
	c := e.session()
	if code, line := c.Cmd("XPAT subject 1-3 *"); code != 412 {
		t.Errorf("XPAT without a group: %q, want 412", line)
	}
	c.Cmd("GROUP test.alpha")
	for _, tc := range []struct {
		cmd  string
		want []string
	}{
		{"XPAT subject 1-3 *2*", []string{"2 article 2 in test.alpha"}},
		{"XPAT Subject 2- article*", []string{"2 article 2 in test.alpha", "3 article 3 in test.alpha"}},
		{"XPAT subject 1-3 article 3 in test.alpha", []string{"3 article 3 in test.alpha"}},
		{"XPAT subject 1-3 *[13]*", []string{"1 article 1 in test.alpha", "3 article 3 in test.alpha"}},
		{"XPAT subject 1-3 *nomatch*", nil},
		{"XPAT from 1 *@upstream.test", []string{"1 poster@upstream.test"}},
		{"XPAT no-such-header 1-3 *", nil},
		{"XPAT subject " + ids[1] + " *2*", []string{ids[1] + " article 2 in test.alpha"}},
		{"XPAT subject " + ids[1] + " *3*", nil},
	} {
		code, line := c.Cmd("%s", tc.cmd)
		if code != 221 {
			t.Errorf("%s: %q, want 221", tc.cmd, line)
			continue
		}
		if got := c.ReadDotLines(); strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: %q, want %q", tc.cmd, got, tc.want)
		}
	}
	for cmd, want := range map[string]int{
		"XPAT subject <missing@upstream.test> *": 430,
		"XPAT subject 1-3":                       501,
		"XPAT subject x *":                       501,
		"XPAT subject -3 *":                      501,
	} {
		if code, line := c.Cmd("%s", cmd); code != want {
			if code == 221 {
				c.ReadDotLines()
			}
			t.Errorf("%s: %q, want %d", cmd, line, want)
		}
	}
	if code, _ := c.Cmd("HELP"); code == 100 {
		if !containsPrefix(c.ReadDotLines(), "  xpat ") {
			t.Errorf("HELP doesn't list xpat")
		}
	}
	if code, line := c.Cmd("LIST EXTENSIONS"); code != 202 {
		t.Errorf("LIST EXTENSIONS: %q", line)
	} else if !containsLine(c.ReadDotLines(), " pat") {
		t.Errorf("LIST EXTENSIONS doesn't list pat")
	}
}

// TestXhdrLongHeaderName: XHDR copied the header name into a 512-byte
// stack buffer, but a command line can have 1024 characters, so a longer
// name overflowed it and killed the session.
func TestXhdrLongHeaderName(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 2)
	c := e.session()
	c.Cmd("GROUP test.alpha")
	long := strings.Repeat("x", 1000)
	for _, cmd := range []string{"XHDR " + long + " 1-2", "XPAT " + long + " 1-2 *"} {
		code, line, err := c.TryCmd("%s", cmd)
		if err != nil || code != 221 {
			t.Fatalf("%.20s...: %d %q %v, want 221", cmd, code, line, err)
		}
		c.ReadDotLines()
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE after XHDR with a long header name: %q", line)
	}
}

// TestXhdrMessageID: XHDR header <message-id> (RFC 2980 2.6) answered
// 501. It needs no group, and its line starts with the message-ID.
func TestXhdrMessageID(t *testing.T) {
	e := newEnv(t)
	ids := e.addArticles("test.alpha", 2)
	c := e.session()
	for _, tc := range []struct {
		cmd  string
		code int
		want string
	}{
		{"XHDR subject " + ids[1], 221, ids[1] + " article 2 in test.alpha"},
		{"XHDR Newsgroups " + ids[0], 221, ids[0] + " test.alpha"},
		{"XHDR x-no-such " + ids[0], 221, ids[0] + " (none)"},
		{"XHDR subject <nonexistent@upstream.test>", 430, ""},
		{"XHDR", 501, ""},
	} {
		code, line := c.Cmd("%s", tc.cmd)
		if code != tc.code {
			t.Errorf("%s: %q, want %d", tc.cmd, line, tc.code)
			if code == 221 {
				c.ReadDotLines()
			}
			continue
		}
		if code == 221 {
			if got := c.ReadDotLines(); len(got) != 1 || got[0] != tc.want {
				t.Errorf("%s: %q, want [%q]", tc.cmd, got, tc.want)
			}
		}
	}
}
