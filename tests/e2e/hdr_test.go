package e2e

import (
	"sort"
	"strings"
	"testing"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// HDR and LIST HEADERS (RFC 3977 8.5, 8.6), and CAPABILITIES (5.2)
// showing what the client may do now.

// capabilities returns the CAPABILITIES lines of c.
func capabilities(t *testing.T, c interface {
	Cmd(format string, args ...any) (int, string)
	ReadDotLines() []string
}) []string {
	t.Helper()
	if code, line := c.Cmd("CAPABILITIES"); code != 101 {
		t.Fatalf("CAPABILITIES: %q, want 101", line)
	}
	caps := c.ReadDotLines()
	if len(caps) == 0 || strings.ToUpper(strings.Join(strings.Fields(caps[0]), " ")) != "VERSION 2" {
		t.Errorf("first CAPABILITIES line must be VERSION 2: %q", caps)
	}
	return caps
}

// checkCaps compares keywords and their arguments without depending on the
// order of arguments. It also checks the keywords in LIST HEADERS replies.
func checkCaps(t *testing.T, what string, caps []string, want, unwanted []string) {
	t.Helper()
	canonical := func(line string) string {
		words := strings.Fields(strings.ToUpper(line))
		if len(words) > 1 {
			sort.Strings(words[1:])
		}
		return strings.Join(words, " ")
	}
	have := map[string]bool{}
	for _, l := range caps {
		have[canonical(l)] = true
	}
	for _, w := range want {
		if !have[canonical(w)] {
			t.Errorf("%s: CAPABILITIES lacks %q: %q", what, w, caps)
		}
	}
	for _, u := range unwanted {
		if have[canonical(u)] {
			t.Errorf("%s: CAPABILITIES has %q: %q", what, u, caps)
		}
	}
}

// TestCapabilities: CAPABILITIES listed READER, POST and (with any
// Authentication) AUTHINFO USER for every client, also after a login and
// to clients without those rights, and neither HDR nor OVER. Where a login
// is required, it listed the reading commands and POST before the login.
func TestCapabilities(t *testing.T) {
	reading := []string{"VERSION 2", "READER", "HDR", "OVER",
		"LIST ACTIVE ACTIVE.TIMES EXTENSIONS HEADERS NEWSGROUPS OVERVIEW.FMT"}

	t.Run("read post", func(t *testing.T) {
		e := newEnv(t)
		c := e.session()
		checkCaps(t, "read post", capabilities(t, c),
			append(reading, "POST"), []string{"AUTHINFO USER"})
	})

	t.Run("read only", func(t *testing.T) {
		e := newEnv(t)
		e.AccessList = `AccessList {
  Client stdin {
    allow read
    List *
    Read *
    PostTo *
    Authentication none
  }
  Default {
    allow none
  }
}`
		c := e.session()
		checkCaps(t, "read only", capabilities(t, c), reading, []string{"POST", "AUTHINFO USER"})
	})

	t.Run("optional login", func(t *testing.T) {
		e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:read,post")
		c := e.session()
		checkCaps(t, "before login", capabilities(t, c),
			append(reading, "POST", "AUTHINFO USER"), nil)
		login(t, c)
		checkCaps(t, "after login", capabilities(t, c),
			append(reading, "POST"), []string{"AUTHINFO USER"})
	})

	t.Run("login only", func(t *testing.T) {
		e := loginOnlyEnv(t, "tester:"+md5Hash+":*:*:*:read")
		c := e.session()
		checkCaps(t, "before login", capabilities(t, c),
			[]string{"VERSION 2", "AUTHINFO USER"}, []string{"READER", "HDR", "OVER", "POST"})
		login(t, c)
		checkCaps(t, "after login", capabilities(t, c),
			reading, []string{"POST", "AUTHINFO USER"})
	})

	// "Authentication unix" without fields: every command but AUTHINFO,
	// CAPABILITIES, MODE READER, HELP and QUIT gets 480 before the login,
	// so READER, HDR, OVER, LIST and POST aren't listed then (RFC 3977
	// 3.3.1, RFC 4643 2.2); they were.
	t.Run("login required", func(t *testing.T) {
		e := newEnv(t)
		e.addArticles("test.alpha", 1)
		e.AccessList = `AccessList {
  Client stdin {
    allow read post
    List *
    Read *
    PostTo *
    Authentication unix
  }
  Default {
    allow none
  }
}`
		c := unixSession(t, e)
		checkCaps(t, "before login", capabilities(t, c),
			[]string{"VERSION 2", "AUTHINFO USER"},
			append(reading[1:], "POST"))
		if code, line := c.Cmd("AUTHINFO USER %s", unixUser); code != 381 {
			t.Fatalf("AUTHINFO USER: %q, want 381", line)
		}
		if code, line := c.Cmd("AUTHINFO PASS secret"); code != 281 {
			t.Fatalf("AUTHINFO PASS: %q, want 281", line)
		}
		checkCaps(t, "after login", capabilities(t, c),
			append(reading, "POST"), []string{"AUTHINFO USER"})
		if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
			t.Errorf("GROUP after login: %q, want 211", line)
		}
	})
}

// hdrLines keeps the mandatory space between a number and an empty value.
func hdrLines(lines []string) string {
	return strings.Join(lines, "|")
}

func TestHdr(t *testing.T) {
	e := newEnv(t)
	ids := e.addArticles("test.alpha", 3)
	c := e.session()
	if code, line := c.Cmd("HDR subject 1-3"); code != 412 {
		t.Errorf("HDR without a group: %q, want 412", line)
	}
	c.Cmd("GROUP test.alpha")
	for _, tc := range []struct {
		cmd  string
		code int
		want string
	}{
		{"HDR subject 1-3", 225, "1 article 1 in test.alpha|2 article 2 in test.alpha|3 article 3 in test.alpha"},
		{"HDR Subject 2-", 225, "2 article 2 in test.alpha|3 article 3 in test.alpha"},
		{"HDR subject", 225, "1 article 1 in test.alpha"}, // the current article
		{"HDR from 2", 225, "2 poster@upstream.test"},
		{"HDR message-id 3", 225, "3 " + ids[2]},
		// no References: the line is there, with an empty value
		{"HDR references 1-2", 225, "1 |2 "},
		{"HDR subject 5-9", 423, ""},
		// metadata by number; the Lines header is not the line count
		{"HDR :lines 1", 225, "1 3"},
		{"HDR Lines 1", 503, ""},
		{"HDR X-No-Such 1-3", 503, ""},
		{"HDR subject 1x", 501, ""},
		{"HDR", 501, ""},
		// by message-ID every header of the article
		{"HDR subject " + ids[1], 225, "0 article 2 in test.alpha"},
		{"HDR newsgroups " + ids[1], 225, "0 test.alpha"},
		{"HDR x-no-such " + ids[1], 225, "0 "},
		{"HDR :lines " + ids[1], 503, ""},
		{"HDR subject <nonexistent@upstream.test>", 430, ""},
	} {
		code, line := c.Cmd("%s", tc.cmd)
		if code != tc.code {
			t.Errorf("%s: %q, want %d", tc.cmd, line, tc.code)
			if code == 225 {
				c.ReadDotLines()
			}
			continue
		}
		if code == 225 {
			if got := hdrLines(c.ReadDotLines()); got != tc.want {
				t.Errorf("%s: %q, want %q", tc.cmd, got, tc.want)
			}
		}
	}

	// :bytes by number and by message-ID
	for _, cmd := range []string{"HDR :bytes 1", "HDR :bytes " + ids[0]} {
		if code, line := c.Cmd("%s", cmd); code != 225 {
			t.Errorf("%s: %q, want 225", cmd, line)
			continue
		}
		got := c.ReadDotLines()
		if len(got) != 1 || len(strings.Fields(got[0])) != 2 || strings.Fields(got[0])[1] == "0" {
			t.Errorf("%s: %q, want one line \"n size\"", cmd, got)
		}
	}
}

func TestListHeaders(t *testing.T) {
	e := newEnv(t)
	c := e.session()
	for _, tc := range []struct {
		arg       string
		want, not []string
	}{
		{"", []string{"Subject", "From", "Message-ID", ":bytes"}, []string{":", ":lines", "Bytes", "Lines"}},
		{" RANGE", []string{"Subject", "Xref", ":bytes", ":lines"}, []string{":", "Lines"}},
		{" MSGID", []string{":", ":bytes"}, []string{"Subject", ":lines"}},
	} {
		code, line := c.Cmd("LIST HEADERS%s", tc.arg)
		if code != 215 {
			t.Errorf("LIST HEADERS%s: %q, want 215", tc.arg, line)
			continue
		}
		checkCaps(t, "LIST HEADERS"+tc.arg, c.ReadDotLines(), tc.want, tc.not)
	}
	if code, line := c.Cmd("LIST HEADERS foo"); code != 501 {
		t.Errorf("LIST HEADERS foo: %q, want 501", line)
	}
}

// TestFoldedHeader: a header folded over two lines (RFC 5322 2.2.3) is
// unfolded by removing the line break only, so "Subject: foo" with the
// continuation line " bar" reads "foo bar", as the upstream's overview
// has it. NewsCache read the stored article as "foobar": HDR, XHDR and
// XPAT by message-ID, and by number once the article was in the spool,
// disagreed with the overview and with the upstream.
func TestFoldedHeader(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	const id = "<folded@upstream.test>"
	e.Store.AddArticle("test.alpha", fakeserver.Article{
		Number:    2,
		MessageID: id,
		Headers: []string{
			"From: poster@upstream.test",
			"Subject: foo",
			" bar",
			"Newsgroups: test.alpha",
			"Xref: upstream.test test.alpha:2",
		},
		Body: []string{"folded subject"},
	})
	c := e.session()
	c.Cmd("GROUP test.alpha")
	// from the overview, as the upstream unfolds it
	if code, line := c.Cmd("HDR subject 2"); code != 225 {
		t.Fatalf("HDR subject 2: %q", line)
	} else if got := hdrLines(c.ReadDotLines()); got != "2 foo bar" {
		t.Errorf("HDR subject 2 from the overview: %q, want %q", got, "2 foo bar")
	}
	// from the article: by message-ID, and by number once it is spooled
	if code, line := c.Cmd("ARTICLE 2"); code != 220 {
		t.Fatalf("ARTICLE 2: %q", line)
	} else if a := c.ReadDotLines(); !containsLine(a, " bar") {
		t.Errorf("ARTICLE 2 doesn't keep the fold: %q", a)
	}
	for _, tc := range []struct{ cmd, want string }{
		{"HDR subject " + id, "0 foo bar"},
		{"XHDR subject " + id, id + " foo bar"},
		{"HDR subject 2", "2 foo bar"},
		{"XHDR subject 2", "2 foo bar"},
		{"XPAT subject " + id + " foo*bar", id + " foo bar"},
		{"XPAT subject 2 foo*bar", "2 foo bar"},
	} {
		code, line := c.Cmd("%s", tc.cmd)
		if code != 225 && code != 221 {
			t.Errorf("%s: %q", tc.cmd, line)
			continue
		}
		if got := hdrLines(c.ReadDotLines()); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

// XHDR and HDR Xref by number, answered from overview records: the
// field name is stripped ("Xref: host g:1" gives "host g:1"; it gave
// ": host g:1"). A record whose Xref field is empty and that ends with a
// tab made the strip read on into the next field and past the end of the
// record: std::length_error, and the session was killed (SIGABRT).
func TestXhdrXrefOverview(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 2)
	upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{
		OverviewFields: func(conn int, group string, f []string) []string {
			if f[0] != "2" {
				return f
			}
			f[len(f)-1] = ""
			return append(f, "")
		},
	}})
	c := e.session()
	c.Strict = false
	c.Cmd("GROUP test.alpha")
	if code, line := c.Cmd("XOVER 1-2"); code != 224 {
		t.Fatalf("XOVER 1-2: %q", line)
	}
	c.ReadDotLines()
	for _, tc := range []struct {
		cmd  string
		code int
	}{{"XHDR Xref 1-2", 221}, {"HDR Xref 1-2", 225}} {
		code, line, err := c.TryCmd("%s", tc.cmd)
		if err != nil {
			t.Fatalf("%s: no reply: %v", tc.cmd, err)
		}
		if code != tc.code {
			t.Errorf("%s: %q, want %d", tc.cmd, line, tc.code)
			continue
		}
		lines := c.ReadDotLines()
		validEmpty := len(lines) == 2 && (lines[1] == "2 " || (tc.code == 221 && lines[1] == "2 (none)"))
		if len(lines) != 2 || lines[0] != "1 upstream.test test.alpha:1" || !validEmpty {
			t.Errorf("%s: %q, want \"1 upstream.test test.alpha:1\" and an empty 2", tc.cmd, lines)
		}
	}
	if code, line, err := c.TryCmd("DATE"); err != nil || code != 111 {
		t.Errorf("DATE after XHDR/HDR Xref: %d %q %v", code, line, err)
	}
}

// TestOverMessageIDNotSupported checks the optional OVER MSGID form is
// refused without contacting the upstream, regardless of group selection.
func TestOverMessageIDNotSupported(t *testing.T) {
	e := newEnv(t)
	ids := e.addArticles("test.alpha", 1)
	c := e.session()
	if caps := capabilities(t, c); !containsLine(caps, "OVER") || containsLine(caps, "OVER MSGID") {
		t.Fatalf("CAPABILITIES: %q, want OVER without MSGID", caps)
	}
	before := len(e.Upstream.Lines())
	if code, line := c.Cmd("OVER %s", ids[0]); code != 503 {
		t.Errorf("OVER by message-id without group: %q, want 503", line)
	}
	if got := len(e.Upstream.Lines()); got != before {
		t.Errorf("OVER by message-id without group contacted upstream: %q", e.Upstream.Lines()[before:])
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q, want 211", line)
	}
	before = len(e.Upstream.Lines())
	if code, line := c.Cmd("OVER %s", ids[0]); code != 503 {
		t.Errorf("OVER by message-id with group: %q, want 503", line)
	}
	if got := len(e.Upstream.Lines()); got != before {
		t.Errorf("OVER by message-id with group contacted upstream: %q", e.Upstream.Lines()[before:])
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE after OVER: %q, want 111", line)
	}
}
