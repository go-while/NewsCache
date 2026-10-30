package e2e

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
	"github.com/go-while/nntp-testkit/proc"
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

// TestCapabilitiesAdvertiseImplementedListVariants: the LIST capability
// names every implemented standard LIST variant, including legacy EXTENSIONS.
func TestCapabilitiesAdvertiseImplementedListVariants(t *testing.T) {
	e := newEnv(t)
	c := e.session()
	var advertised []string
	for _, line := range capabilities(t, c) {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.EqualFold(fields[0], "LIST") {
			for _, name := range fields[1:] {
				advertised = append(advertised, strings.ToUpper(name))
			}
		}
	}
	have := make(map[string]bool, len(advertised))
	for _, name := range advertised {
		have[name] = true
	}
	for _, name := range []string{
		"ACTIVE", "ACTIVE.TIMES", "EXTENSIONS", "HEADERS", "NEWSGROUPS", "OVERVIEW.FMT",
	} {
		if !have[name] {
			t.Errorf("CAPABILITIES LIST line lacks %s: %q", name, advertised)
		}
	}
}

// TestReaderRefusesTransitCommands: reader connections neither advertise nor
// accept the transit-only IHAVE, CHECK, TAKETHIS and MODE STREAM commands.
func TestReaderRefusesTransitCommands(t *testing.T) {
	e := newEnv(t)
	c := e.session()

	caps := map[string]bool{}
	for _, line := range capabilities(t, c) {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			caps[strings.ToUpper(fields[0])] = true
		}
	}
	for _, name := range []string{"IHAVE", "CHECK", "TAKETHIS", "STREAMING"} {
		if caps[name] {
			t.Errorf("reader CAPABILITIES advertises %s", name)
		}
	}

	const id = "<transit-reader@upstream.test>"
	for _, tc := range []struct {
		command string
		want    int
	}{
		{"IHAVE " + id, 500},
		{"CHECK " + id, 500},
		{"TAKETHIS " + id, 500},
		{"MODE STREAM", 501},
	} {
		if code, line := c.Cmd("%s", tc.command); code != tc.want {
			t.Errorf("%s: %q, want %d refusal", tc.command, line, tc.want)
		}
	}

	if lines := e.Upstream.Lines(); len(lines) != 0 {
		t.Errorf("reader transit commands reached the upstream: %q", lines)
	}
	if code, line := c.Cmd("STAT %s", id); code != 430 {
		t.Errorf("STAT %s after refused transit commands: %q, want 430 (not stored)", id, line)
	}
	if posted := e.Store.Posted(); len(posted) != 0 {
		t.Errorf("reader transit commands posted %d articles upstream", len(posted))
	}
	if code, line := c.Cmd("QUIT"); code != 205 {
		t.Errorf("QUIT: %q, want 205", line)
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

// TestListOverviewFmtMetadataNames: RFC 3977 names the overview metadata
// fields :bytes and :lines, not the internal Bytes:/Lines: header names.
func TestListOverviewFmtMetadataNames(t *testing.T) {
	e := newEnv(t)
	c := e.session()
	code, line := c.Cmd("LIST OVERVIEW.FMT")
	if code != 215 {
		t.Fatalf("LIST OVERVIEW.FMT: %q, want 215", line)
	}
	got := append([]string{line}, c.ReadDotLines()...)
	want := []string{
		"215 Order of fields in overview database",
		"Subject:",
		"From:",
		"Date:",
		"Message-ID:",
		"References:",
		":bytes",
		":lines",
		"Xref:full",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("LIST OVERVIEW.FMT response:\n  got  %q\n  want %q", got, want)
	}
}

// TestListActiveTimesLongPattern: a LIST ACTIVE.TIMES pattern longer than a
// newsgroup name (513 bytes) is only a pattern that matches nothing; the
// session answers and goes on. It was copied into a fixed buffer of the
// group filter unchecked: with glibc's checks the session aborted ("buffer
// overflow detected"), elsewhere it wrote past the buffer.
func TestListActiveTimesLongPattern(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	c := e.session()
	// (under 1000 bytes: longer command lines are refused before the
	// command runs)
	for _, pat := range []string{
		strings.Repeat("x", 600),
		strings.Repeat("a", 900) + ".*,test.*",
		"test.*,!" + strings.Repeat("b", 900),
	} {
		code, line := c.Cmd("LIST ACTIVE.TIMES %s", pat)
		if code != 215 {
			t.Fatalf("LIST ACTIVE.TIMES <%d bytes>: %q, want 215", len(pat), line)
		}
		c.ReadDotLines()
	}
	// the session is still alive and the pattern logic still works
	code, line := c.Cmd("LIST ACTIVE.TIMES test.*")
	if code != 215 {
		t.Fatalf("LIST ACTIVE.TIMES test.*: %q, want 215", line)
	}
	if lines := c.ReadDotLines(); len(lines) != 1 || !strings.HasPrefix(lines[0], "test.alpha ") {
		t.Errorf("LIST ACTIVE.TIMES test.*: %q, want the line of test.alpha", lines)
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

// TestReaderReplyCodeGaps covers the reply-code cases not already exercised
// by TestStat, TestHdr and TestOverNoCurrentArticle.
func TestReaderReplyCodeGaps(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.Store.AddGroup("test.empty", "no articles")
	c := e.session()

	for _, cmd := range []string{"NEXT", "LAST"} {
		if code, line := c.Cmd("%s", cmd); code != 412 {
			t.Errorf("%s without a group: %q, want 412", cmd, line)
		}
	}
	if code, line := c.Cmd("GROUP test.empty"); code != 211 {
		t.Fatalf("GROUP test.empty: %q, want 211", line)
	}
	for _, cmd := range []string{"NEXT", "LAST"} {
		if code, line := c.Cmd("%s", cmd); code != 420 {
			t.Errorf("%s without a current article: %q, want 420", cmd, line)
		}
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP test.alpha: %q, want 211", line)
	}
	for _, cmd := range []string{"ARTICLE abc", "ARTICLE -1", "ARTICLE 1x"} {
		if code, line := c.Cmd("%s", cmd); code != 501 {
			t.Errorf("%s: %q, want 501 syntax error", cmd, line)
		}
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

// TestCachedArticleOddXrefAndEmptyPath: the spool preserves an article with
// an Xref containing only its host and an empty Path header. HEAD and ARTICLE
// served by a later process must match the first ARTICLE response exactly.
func TestCachedArticleOddXrefAndEmptyPath(t *testing.T) {
	const (
		group = "test.header-cache"
		id    = "<header-cache@upstream.test>"
	)
	e := newEnv(t)
	e.Store.AddGroup(group, "odd article headers")
	e.Store.AddArticle(group, fakeserver.Article{
		Number:    1,
		MessageID: id,
		Headers: []string{
			"Xref: upstream.test",
			"Path:",
			"From: poster@upstream.test",
			"Newsgroups: " + group,
			"Subject: following header remains intact",
			"X-After: preserve this header",
		},
		Body: []string{"cached body", ".dot-stuffed body"},
	})

	first := e.session()
	if code, line := first.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q, want 211", line)
	}
	if code, line := first.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("first ARTICLE 1: %q, want 220", line)
	}
	firstArticle := first.RawUntilDot()
	for _, header := range [][]byte{
		[]byte("Xref: upstream.test\r\n"),
		[]byte("Path:\r\n"),
		[]byte("Subject: following header remains intact\r\n"),
		[]byte("X-After: preserve this header\r\n"),
	} {
		if !bytes.Contains(firstArticle, header) {
			t.Fatalf("first ARTICLE lacks %q: %q", header, firstArticle)
		}
	}
	if got := e.Upstream.Count("ARTICLE"); got != 1 {
		t.Fatalf("upstream ARTICLE count after first response = %d, want 1", got)
	}
	if code, line := first.Cmd("QUIT"); code != 205 {
		t.Fatalf("first QUIT: %q, want 205", line)
	}

	// A second process reads the same spool, not the upstream article.
	cached := e.session()
	if code, line := cached.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP from spool: %q, want 211", line)
	}
	if code, line := cached.Cmd("HEAD 1"); code != 221 {
		t.Fatalf("cached HEAD 1: %q, want 221", line)
	}
	cachedHead := cached.RawUntilDot()
	separator := bytes.Index(firstArticle, []byte("\r\n\r\n"))
	if separator < 0 {
		t.Fatalf("first ARTICLE has no header/body separator: %q", firstArticle)
	}
	wantHead := append(append([]byte(nil), firstArticle[:separator+2]...), []byte(".\r\n")...)
	if !bytes.Equal(cachedHead, wantHead) {
		t.Errorf("cached HEAD differs from first ARTICLE headers:\n got %q\nwant %q", cachedHead, wantHead)
	}

	if code, line := cached.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("cached ARTICLE 1: %q, want 220", line)
	}
	cachedArticle := cached.RawUntilDot()
	if !bytes.Equal(cachedArticle, firstArticle) {
		t.Errorf("cached ARTICLE differs from first response:\n got %q\nwant %q", cachedArticle, firstArticle)
	}
	if got := e.Upstream.Count("ARTICLE"); got != 1 {
		t.Errorf("upstream ARTICLE count after cached reads = %d, want 1", got)
	}
	if code, line := cached.Cmd("QUIT"); code != 205 {
		t.Errorf("cached QUIT: %q, want 205", line)
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
	// DATE with an argument is a syntax error.
	if code, line := c.Cmd("DATE x"); code != 501 {
		t.Errorf("DATE x: %q, want 501", line)
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

// TestNewgroupsTime: NEWGROUPS takes the given date and time as UTC with
// GMT, and as local time (DST as on that date) without. The server runs in
// US Eastern time (a POSIX TZ rule, no tzdata needed). The 231 response
// follows RFC 3977 and does not echo the parsed timestamp.
func TestNewgroupsTime(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.writeConf()
	o := e.procOpts("-i")
	o.Env = append(o.Env, "TZ=EST5EDT,M3.2.0,M11.1.0")
	p := proc.StartStdio(t, o)
	c := client.New(t, p.Conn())
	if _, err := c.ReadGreeting(); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{
		"20260101 000000 GMT",
		"20260101 000000", // EST, UTC-5
		"20260701 120000", // EDT, UTC-4
		"260701 160000 GMT",
	} {
		code, line := c.Cmd("NEWGROUPS %s", args)
		if code != 231 {
			t.Fatalf("NEWGROUPS %s: %q, want 231", args, line)
		}
		c.ReadDotLines()
		if line != "231 list of new groups follows" {
			t.Errorf("NEWGROUPS %s: %q, want the RFC 3977 reply line", args, line)
		}
	}
}

// TestNewgroupsDateFormsAndCenturyRule checks both date widths and the
// RFC 3977 two-digit-year rule: YY is in this century through the current
// year, and in the previous century when YY is greater.
func TestNewgroupsDateFormsAndCenturyRule(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	c := e.session()
	if code, line := c.Cmd("LIST ACTIVE test.alpha"); code != 215 {
		t.Fatalf("LIST ACTIVE: %q, want 215", line)
	}
	active := c.ReadDotLines()
	if len(active) != 1 {
		t.Fatalf("LIST ACTIVE test.alpha: %q, want one group", active)
	}
	year := time.Now().UTC().Year()
	current := fmt.Sprintf("%02d0101", year%100)
	full := fmt.Sprintf("%04d0101", year)
	for _, date := range []string{current, full} {
		code, line := c.Cmd("NEWGROUPS %s 000000 GMT", date)
		if code != 231 {
			t.Fatalf("NEWGROUPS %s: %q, want 231", date, line)
		}
		if got := c.ReadDotLines(); len(got) != 1 || got[0] != active[0] {
			t.Errorf("NEWGROUPS %s: %q, want the cached group %q", date, got, active)
		}
	}
	if year%100 < 99 {
		previousCentury := fmt.Sprintf("%02d0101", year%100+1)
		code, line := c.Cmd("NEWGROUPS %s 000000 GMT", previousCentury)
		if code != 231 {
			t.Fatalf("NEWGROUPS %s: %q, want 231", previousCentury, line)
		}
		if got := c.ReadDotLines(); len(got) != 1 || got[0] != active[0] {
			t.Errorf("NEWGROUPS %s resolved to the wrong century: header %q, lines %q",
				previousCentury, line, got)
		}
	}
}

// NEWGROUPS must reject calendar or clock values that the C library would
// otherwise silently normalize. RFC 3977 permits second 60 for a leap second.
func TestNewgroupsRejectsInvalidDateTime(t *testing.T) {
	e := newEnv(t)
	c := e.session()
	for _, args := range []string{
		"20260230 000000 GMT",
		"20261301 000000 GMT",
		"20260132 000000 GMT",
		"20260101 240000 GMT",
		"20260101 006000 GMT",
		"20260101 000061 GMT",
		"00000101 000000 GMT",
		"20260101 000000 UTC",
	} {
		code, line := c.Cmd("NEWGROUPS %s", args)
		if code == 231 {
			c.ReadDotLines()
		}
		if code != 501 {
			t.Errorf("NEWGROUPS %s: %q, want 501", args, line)
		}
	}
	if code, line := c.Cmd("NEWGROUPS 20260101 235960 GMT"); code != 231 {
		t.Errorf("NEWGROUPS with leap second: %q, want 231", line)
	} else {
		c.ReadDotLines()
	}
}

// A timestamp written in UTC is compared as UTC with GMT and as the local
// server time without it. The threshold is chosen between those instants.
func TestNewgroupsGMTIsIndependentOfServerTimezone(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.writeConf()
	o := e.procOpts("-i")
	o.Env = append(o.Env, "TZ=EST5EDT,M3.2.0,M11.1.0")
	p := proc.StartStdio(t, o)
	c := client.New(t, p.Conn())
	if _, err := c.ReadGreeting(); err != nil {
		t.Fatal(err)
	}
	if code, line := c.Cmd("LIST ACTIVE test.alpha"); code != 215 {
		t.Fatalf("LIST ACTIVE: %q, want 215", line)
	}
	c.ReadDotLines()
	threshold := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	args := threshold.Format("20060102 150405")
	if code, line := c.Cmd("NEWGROUPS %s GMT", args); code != 231 {
		t.Fatalf("NEWGROUPS %s GMT: %q, want 231", args, line)
	} else if got := c.ReadDotLines(); len(got) != 1 || !strings.HasPrefix(got[0], "test.alpha ") {
		t.Errorf("GMT threshold omitted the cached group: %q", got)
	}
	if code, line := c.Cmd("NEWGROUPS %s", args); code != 231 {
		t.Fatalf("NEWGROUPS %s in local time: %q, want 231", args, line)
	} else if got := c.ReadDotLines(); len(got) != 0 {
		t.Errorf("local-time threshold unexpectedly included the cached group: %q", got)
	}
}

// TestNewgroupsRejectsExtraArgumentsAndUsesRFCReplyLine: NEWGROUPS accepts
// only date, time, and optional GMT, and its 231 text has no timestamp suffix.
func TestNewgroupsRejectsExtraArgumentsAndUsesRFCReplyLine(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	c := e.session()
	code, line := c.Cmd("NEWGROUPS 20000101 000000 GMT")
	if code != 231 {
		t.Fatalf("NEWGROUPS: %q, want 231", line)
	}
	if line != "231 list of new groups follows" {
		t.Errorf("NEWGROUPS: %q, want exact 231 reply line", line)
	}
	c.ReadDotLines()
	code, line = c.Cmd("NEWGROUPS 20000101 000000 GMT extra")
	if code != 501 || line != "501 Syntax: NEWGROUPS date time [GMT]" {
		t.Errorf("NEWGROUPS with an extra argument: %q, want 501 syntax", line)
	}
	if code == 231 {
		c.ReadDotLines()
	}
}

// TestPostMessageIDLongHostname: generated IDs preserve the configured host
// when they fit, and use its MD5 when the complete ID would exceed 250 bytes.
func TestPostMessageIDLongHostname(t *testing.T) {
	post := func(t *testing.T, host string) string {
		t.Helper()
		e := newEnv(t)
		e.addArticles("test.alpha", 1)
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
		mid := posted[0].MessageID
		for _, h := range posted[0].Headers {
			if strings.HasPrefix(strings.ToLower(h), "message-id:") {
				mid = strings.TrimSpace(h[len("message-id:"):])
			}
		}
		if len(mid) > 250 || !strings.HasPrefix(mid, "<newscache$") ||
			!strings.HasSuffix(mid, ">") || strings.Count(mid, "$") != 3 {
			t.Errorf("generated Message-ID %q (%d bytes): want <newscache$time$pid$counter@host>, at most 250 bytes", mid, len(mid))
		}
		return mid
	}

	for _, host := range []string{"news.example.test", strings.Repeat("a", 50) + "." +
		strings.Repeat("b", 50) + "." + strings.Repeat("c", 50) + "." + strings.Repeat("d", 47)} {
		t.Run("fits-"+fmt.Sprint(len(host)), func(t *testing.T) {
			mid := post(t, host)
			if !strings.HasSuffix(mid, "@"+host+">") {
				t.Errorf("fitting hostname changed: %q", mid)
			}
		})
	}
	// Derive the size of the generated left side from a real session, then
	// exercise the 250/251-byte boundary. The PID and clock may change the
	// left-side width, so check the actual candidate length for each post.
	probe := post(t, "probe.test")
	at := strings.LastIndexByte(probe, '@')
	if at < 0 {
		t.Fatalf("generated Message-ID lacks @: %q", probe)
	}
	limit := 250 - (at + 2) // prefix through @ and the final >
	hostOfLength := func(length int) string {
		var host string
		for len(host) < length {
			if host != "" {
				host += "."
			}
			n := min(60, length-len(host))
			host += strings.Repeat("b", n)
		}
		return host
	}
	seen250, seen251 := false, false
	for n := limit - 2; n <= limit+2; n++ {
		host := hostOfLength(n)
		mid := post(t, host)
		at := strings.LastIndexByte(mid, '@')
		if at < 0 {
			t.Fatalf("generated Message-ID lacks @: %q", mid)
		}
		candidateLen := at + 2 + len(host)
		seen250 = seen250 || candidateLen == 250
		seen251 = seen251 || candidateLen == 251
		suffix := "@" + host + ">"
		if candidateLen > 250 {
			digest := md5.Sum([]byte(host))
			suffix = fmt.Sprintf("@%x>", digest)
		}
		if !strings.HasSuffix(mid, suffix) {
			t.Errorf("candidate length %d: generated ID %q, want suffix %q", candidateLen, mid, suffix)
		}
	}
	if !seen250 || !seen251 {
		t.Errorf("boundary fixtures did not exercise both 250 and 251-byte candidates")
	}
	long := strings.Repeat("h", 60) + "." + strings.Repeat("o", 60) + "." +
		strings.Repeat("s", 60) + "." + strings.Repeat("t", 60) + ".test"
	other := "x" + long[1:]
	if len(long) != 248 || len(other) != 248 {
		t.Fatal("long hostname fixture must be 248 bytes")
	}
	var suffixes []string
	for _, host := range []string{long, long, other} {
		mid := post(t, host) // separate sessions, including two for the same host
		digest := md5.Sum([]byte(host))
		want := fmt.Sprintf("@%x>", digest)
		if !strings.HasSuffix(mid, want) {
			t.Errorf("generated Message-ID %q: want hostname hash suffix %s", mid, want)
		}
		at := strings.LastIndexByte(mid, '@')
		if at < 0 {
			t.Fatalf("generated Message-ID lacks @: %q", mid)
		}
		suffixes = append(suffixes, mid[at:])
	}
	if suffixes[0] != suffixes[1] || suffixes[0] == suffixes[2] {
		t.Errorf("hostname suffixes %q: same host must match and different hosts must differ", suffixes)
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

// TestXhdrLongNameMatch: find_field cut a header name to 511 characters,
// so a longer one matched a header that only starts with those, and
// getfield skipped the whole name: past the end of the article when that
// header is the last one (a heap over-read; AddressSanitizer reports it).
func TestXhdrLongNameMatch(t *testing.T) {
	e := newEnv(t)
	e.Store.AddGroup("test.alpha", "x")
	id := "<longname.1@upstream.test>"
	name := strings.Repeat("A", 600)
	e.Store.AddArticle("test.alpha", fakeserver.Article{Number: 1, MessageID: id,
		Headers: []string{"From: poster@upstream.test", "Message-ID: " + id,
			"Subject: s", "Newsgroups: test.alpha", name + ": v"},
		Body: []string{"b"}})
	c := e.session()
	for _, tc := range []struct{ name, want string }{
		{strings.Repeat("A", 900), id + " (none)"},
		{name, id + " v"},
	} {
		code, line, err := c.TryCmd("XHDR %s %s", tc.name, id)
		if err != nil || code != 221 {
			t.Errorf("XHDR <%d characters> %s: %d %q %v, want 221", len(tc.name), id, code, line, err)
			continue
		}
		if got := c.ReadDotLines(); len(got) != 1 || got[0] != tc.want {
			t.Errorf("XHDR <%d characters> %s: %q, want [%q]", len(tc.name), id, got, tc.want)
		}
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE: %q", line)
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

// TestOverRangeResponses distinguishes RFC 3977 OVER from the older aliases.
func TestOverRangeResponses(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 3)
	if !e.Store.RemoveArticle("test.alpha", 2) {
		t.Fatal("remove article 2")
	}
	e.Store.AddGroup("test.empty", "empty group")
	c := e.session()
	if code, line := c.Cmd("OVER 1-3"); code != 412 {
		t.Fatalf("OVER without group: %q, want 412", line)
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q, want 211", line)
	}
	for _, tc := range []struct {
		cmd  string
		code int
		want []int64
	}{
		{"OVER 1-3", 224, []int64{1, 3}},
		{"OVER 2-2", 423, nil},
		{"OVER 3-1", 423, nil},
		{"OVER 4-", 423, nil},
		{"OVER", 224, []int64{1}},
		// The historic aliases retain their existing empty-list replies.
		{"XOVER 2-2", 224, nil},
		{"XHDR subject 2-2", 221, nil},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			code, line := c.Cmd("%s", tc.cmd)
			var lines []string
			if code == 224 || code == 221 {
				lines = c.ReadDotLines()
			}
			if code != tc.code {
				t.Errorf("%s: %q, want %d (data %q)", tc.cmd, line, tc.code, lines)
				return
			}
			if code == 224 || code == 221 {
				wantNumbers(t, tc.cmd, xoverNumbers(t, lines), tc.want...)
			}
		})
	}
	if code, line := c.Cmd("GROUP test.empty"); code != 211 {
		t.Fatalf("GROUP empty: %q, want 211", line)
	}
	if code, line := c.Cmd("OVER"); code != 420 {
		t.Errorf("OVER without current article: %q, want 420", line)
	}
	// No dot terminator may follow a single-line 423 or 420.
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE after OVER: %q, want 111", line)
	}
}

// TestOverviewAfterArticleRead: an article read by a client replaces its
// overview record in the group database, and the record is then made
// from the article. It lost :lines when the article has no Lines header
// (most don't; RFC 5536 made it obsolete), and :bytes counted the dots
// of dot-stuffing. The records must stay what the news server sent.
func TestOverviewAfterArticleRead(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 3) // bodies of 3 lines, one starting with a dot
	e.Store.AddArticle("test.alpha", groupArticle("test.alpha", 4, bigBody()))
	overview := func(c *client.Conn) []string {
		t.Helper()
		if code, line := c.Cmd("XOVER 1-4"); code != 224 {
			t.Fatalf("XOVER: %q", line)
		}
		return c.ReadDotLines()
	}
	c := e.session()
	c.Cmd("GROUP test.alpha")
	before := overview(c)
	for _, cmd := range []string{"ARTICLE 1", "HEAD 2", "BODY 3", "ARTICLE 4"} {
		if code, line := c.Cmd("%s", cmd); code < 220 || code > 222 {
			t.Fatalf("%s: %q", cmd, line)
		}
		c.ReadDotLines()
	}
	after := overview(c)
	if strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("overview after reading the articles:\n%s\nwant (as before):\n%s",
			strings.Join(after, "\n"), strings.Join(before, "\n"))
	}
	for _, hc := range []struct{ cmd, want string }{
		{"XHDR lines 1-4", "1 3|2 3|3 3|4 220"},
		{"HDR :lines 1-4", "1 3|2 3|3 3|4 220"},
	} {
		if code, line := c.Cmd("%s", hc.cmd); code != 221 && code != 225 {
			t.Errorf("%s: %q", hc.cmd, line)
		} else if got := strings.Join(c.ReadDotLines(), "|"); got != hc.want {
			t.Errorf("%s: %q, want %q", hc.cmd, got, hc.want)
		}
	}
	// :bytes by message-ID comes from the article itself
	f := strings.Split(before[0], "\t")
	if code, line := c.Cmd("HDR :bytes <test-alpha.1@upstream.test>"); code != 225 {
		t.Errorf("HDR :bytes <id>: %q", line)
	} else if got := c.ReadDotLines(); len(got) != 1 || got[0] != "0 "+f[6] {
		t.Errorf("HDR :bytes <id>: %q, want \"0 %s\" (the news server's :bytes)", got, f[6])
	}
	c.Cmd("QUIT")

	c = e.session()
	c.Cmd("GROUP test.alpha")
	if again := overview(c); strings.Join(again, "\n") != strings.Join(before, "\n") {
		t.Errorf("overview in a new session:\n%s", strings.Join(again, "\n"))
	}
	c.Cmd("QUIT")
}

// TestOverviewLinesHeader: the overview record made from an article read
// by a client took Lines from the poster's Lines header when there was
// one, the news server's record counts the body (RFC 3977 :lines): the
// same article showed two line counts, depending on where its record
// came from. XHDR lines by number did the same.
func TestOverviewLinesHeader(t *testing.T) {
	e := newEnv(t)
	e.Store.AddGroup("test.alpha", "x")
	id := "<lines.1@upstream.test>"
	e.Store.AddArticle("test.alpha", fakeserver.Article{Number: 1, MessageID: id,
		Headers: []string{"From: poster@upstream.test", "Message-ID: " + id,
			"Subject: s", "Newsgroups: test.alpha", "Lines: 99",
			"Xref: upstream.test test.alpha:1"},
		Body: []string{"one", "two"}})
	c := e.session()
	c.Cmd("GROUP test.alpha")
	xover := func() []string {
		t.Helper()
		if code, line := c.Cmd("XOVER 1"); code != 224 {
			t.Fatalf("XOVER 1: %q", line)
		}
		return c.ReadDotLines()
	}
	before := xover()
	if code, line := c.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1: %q", line)
	}
	c.ReadDotLines()
	if after := xover(); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("XOVER 1 after ARTICLE 1: %q, want (as before) %q", after, before)
	}
	for _, cmd := range []string{"XHDR lines 1", "HDR :lines 1"} {
		if code, line := c.Cmd("%s", cmd); code != 221 && code != 225 {
			t.Errorf("%s: %q", cmd, line)
		} else if got := c.ReadDotLines(); len(got) != 1 || got[0] != "1 2" {
			t.Errorf("%s: %q, want [\"1 2\"]", cmd, got)
		}
	}
}

// TestNextLastNotCachedGap: NEXT and LAST skip numbers without an article.
// In a group that isn't cached (Options not-cached), the overview of a
// missing number is a NULL pointer, which NEXT and LAST dereferenced: the
// session crashed (SIGSEGV) at the first gap.
func TestNextLastNotCachedGap(t *testing.T) {
	const group = "test.gap"
	e := newEnv(t)
	e.Store.AddGroup(group, "articles 1, 2 and 5")
	for _, n := range []int64{1, 2, 5} {
		e.Store.AddArticle(group, groupArticle(group, n, []string{"body"}))
	}
	e.ServerOptions = "not-cached"
	c := e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if code, line := c.Cmd("NEXT"); code != 223 || !strings.HasPrefix(line, "223 2 ") {
		t.Fatalf("NEXT from 1: %q, want 223 2", line)
	}
	if code, line := c.Cmd("NEXT"); code != 223 || !strings.HasPrefix(line, "223 5 ") {
		t.Fatalf("NEXT from 2 over the gap: %q, want 223 5", line)
	}
	if code, line := c.Cmd("NEXT"); code != 421 {
		t.Errorf("NEXT from 5: %q, want 421", line)
	}
	if code, line := c.Cmd("LAST"); code != 223 || !strings.HasPrefix(line, "223 2 ") {
		t.Fatalf("LAST from 5 over the gap: %q, want 223 2", line)
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE after NEXT/LAST: %q, want 111 (session alive)", line)
	}
}

// TestOverviewHeaderTab: the overview record made from an article read by
// a client kept a TAB in a header as it was (RFC 3977 8.3.2: it becomes a
// space). "Subject: one<TAB>two" gave the record a tenth field, the
// fields after it moved, and STAT 1 answered with the Date as the
// message-ID.
func TestOverviewHeaderTab(t *testing.T) {
	e := newEnv(t)
	e.Store.AddGroup("test.alpha", "x")
	id := "<tab.1@upstream.test>"
	e.Store.AddArticle("test.alpha", fakeserver.Article{Number: 1, MessageID: id,
		Headers: []string{"From: poster@upstream.test", "Message-ID: " + id,
			"Subject: one\ttwo", "Newsgroups: test.alpha",
			"Xref: upstream.test test.alpha:1"},
		Body: []string{"body line"}})
	c := e.session()
	c.Cmd("GROUP test.alpha")
	if code, line := c.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1: %q", line)
	}
	c.ReadDotLines()
	if code, line := c.Cmd("XOVER 1"); code != 224 {
		t.Errorf("XOVER 1: %q", line)
	} else if ov := c.ReadDotLines(); len(ov) != 1 || strings.Count(ov[0], "\t") != 8 ||
		strings.Split(ov[0], "\t")[1] != "one two" || strings.Split(ov[0], "\t")[4] != id {
		t.Errorf("XOVER 1 after ARTICLE 1: %q, want 9 fields, Subject \"one two\"", ov)
	}
	if code, line := c.Cmd("STAT 1"); code != 223 || !strings.Contains(line, id) {
		t.Errorf("STAT 1: %q, want 223 1 %s", line, id)
	}
}

// TestCommandLineTooLong: a command line longer than NewsCache's buffer
// (1024 bytes) gets 501 (RFC 3977 3.2.1), and the session goes on with
// the next line. It closed the connection without a reply.
func TestCommandLineTooLong(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	c := e.session()
	for _, n := range []int{1100, 5000} {
		if code, line := c.Cmd("XPAT Subject 1- %s", strings.Repeat("x", n)); code != 501 {
			t.Fatalf("XPAT with a %d-byte pattern: %q, want 501", n, line)
		}
		if code, line := c.Cmd("DATE"); code != 111 {
			t.Fatalf("DATE after the long line: %q, want 111", line)
		}
	}
	// the long line and the next one in one write: only the long one
	// is skipped
	if err := c.Send("GROUP " + strings.Repeat("y", 3000) + "\r\nGROUP test.alpha\r\n"); err != nil {
		t.Fatal(err)
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 501 {
		t.Fatalf("long GROUP line: %q, %v; want 501", line, err)
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 211 {
		t.Fatalf("GROUP test.alpha after it: %q, %v; want 211", line, err)
	}
	// 256 arguments: also too long, also 501
	if code, line := c.Cmd("XPAT Subject 1-%s", strings.Repeat(" a", 254)); code != 501 {
		t.Fatalf("XPAT with 256 words: %q, want 501", line)
	}
}

// TestOverNoCurrentArticle: OVER, XOVER, XHDR and HDR without a range
// need a current article (RFC 3977 8.3.2, 8.5.2): in an empty group there
// is none, and the answer is 420. OVER, XOVER and XHDR answered an empty
// list for article 0.
func TestOverNoCurrentArticle(t *testing.T) {
	e := newEnv(t)
	e.Store.AddGroup("test.empty", "a group without articles")
	c := e.session()
	if code, line := c.Cmd("GROUP test.empty"); code != 211 {
		t.Fatalf("GROUP test.empty: %q, want 211", line)
	}
	for _, cmd := range []string{"OVER", "XOVER", "XHDR Subject", "HDR Subject"} {
		code, line := c.Cmd("%s", cmd)
		if code == 224 || code == 221 || code == 225 {
			c.ReadDotLines()
		}
		if code != 420 {
			t.Errorf("%s without a current article: %q, want 420", cmd, line)
		}
	}
}

// TestNewgroupsFormat: NEWGROUPS lists the new groups in the format of
// LIST ACTIVE, "group high low status" (RFC 3977 7.3.2, also RFC 977).
// NewsCache printed the LIST ACTIVE.TIMES format, "group time creator".
func TestNewgroupsFormat(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 3)
	c := e.session()
	if code, line := c.Cmd("LIST ACTIVE test.alpha"); code != 215 {
		t.Fatalf("LIST ACTIVE: %q", line)
	}
	active := c.ReadDotLines()
	code, line := c.Cmd("NEWGROUPS 20000101 000000 GMT")
	if code != 231 {
		t.Fatalf("NEWGROUPS: %q, want 231", line)
	}
	lines := c.ReadDotLines()
	found := false
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) != 4 || strings.Trim(f[1]+f[2], "0123456789") != "" {
			t.Errorf("NEWGROUPS line %q, want \"group high low status\"", l)
			continue
		}
		if f[0] == "test.alpha" {
			found = true
			if len(active) != 1 || l != active[0] {
				t.Errorf("NEWGROUPS line %q, LIST ACTIVE %q: want the same", l, active)
			}
		}
	}
	if !found {
		t.Errorf("NEWGROUPS since 2000 doesn't list test.alpha: %q", lines)
	}
}
