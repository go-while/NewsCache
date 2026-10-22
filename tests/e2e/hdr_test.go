package e2e

import (
	"strings"
	"testing"
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
	return c.ReadDotLines()
}

// checkCaps reports the lines of want that caps lacks and of unwanted
// that it has.
func checkCaps(t *testing.T, what string, caps []string, want, unwanted []string) {
	t.Helper()
	have := map[string]bool{}
	for _, l := range caps {
		have[l] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("%s: CAPABILITIES lacks %q: %q", what, w, caps)
		}
	}
	for _, u := range unwanted {
		if have[u] {
			t.Errorf("%s: CAPABILITIES has %q: %q", what, u, caps)
		}
	}
}

// TestCapabilities: CAPABILITIES listed READER, POST and (with any
// Authentication) AUTHINFO USER for every client, also after a login and
// to clients without those rights, and neither HDR nor OVER.
func TestCapabilities(t *testing.T) {
	reading := []string{"VERSION 2", "READER", "HDR", "OVER",
		"LIST ACTIVE ACTIVE.TIMES HEADERS NEWSGROUPS OVERVIEW.FMT"}

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
}

// hdrLines strips the trailing blank that an empty value leaves.
func hdrLines(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(out, "|")
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
		{"HDR references 1-2", 225, "1|2"},
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
		{"HDR x-no-such " + ids[1], 225, "0"},
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
