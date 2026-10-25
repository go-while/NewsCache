package e2e

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// Read rights for groups and for articles fetched by message-ID. The
// client may read test.alpha, but not test.secret.

// readEnv returns an env whose inetd client may read only test.alpha. With
// authLines, its entry also takes a login against an auth file with them.
func readEnv(t *testing.T, authLines ...string) (*env, []string, []string) {
	e := newEnv(t)
	alpha := e.addArticles("test.alpha", 2)
	secret := e.addArticles("test.secret", 2)
	auth := "none"
	if len(authLines) > 0 {
		path := filepath.Join(e.Dir, "newscache.auth")
		if err := os.WriteFile(path, []byte(strings.Join(authLines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		auth = "file:" + path
	}
	e.AccessList = `AccessList {
  Client stdin {
    allow read post
    List *
    Read test.alpha
    PostTo test.alpha
    Authentication ` + auth + `
  }
  Default {
    allow none
  }
}`
	return e, alpha, secret
}

// TestListgroupDeniedDoesNotSelect: LISTGROUP for a group the client may
// not read was refused, but only after it had made the group the current
// one, and XOVER, XHDR, ARTICLE n, ... read the current group without
// checking again: "LISTGROUP test.secret" then "XOVER 1-2" showed it.
func TestListgroupDeniedDoesNotSelect(t *testing.T) {
	e, _, _ := readEnv(t)
	c := e.session()
	if code, line := c.Cmd("LISTGROUP test.secret"); code != 502 {
		if code == 211 {
			c.ReadDotLines()
		}
		t.Errorf("LISTGROUP test.secret: %q, want 502", line)
	}
	if code, line := c.Cmd("XOVER 1-2"); code != 412 {
		if code == 224 {
			t.Errorf("XOVER after a refused LISTGROUP shows the group: %q", c.ReadDotLines())
		} else {
			t.Errorf("XOVER after a refused LISTGROUP: %q, want 412", line)
		}
	}
	if code, line := c.Cmd("ARTICLE 1"); code != 412 {
		if code == 220 {
			c.ReadDotLines()
		}
		t.Errorf("ARTICLE 1 after a refused LISTGROUP: %q, want 412", line)
	}

	// With a group selected, a refused LISTGROUP keeps it.
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP test.alpha: %q", line)
	}
	if code, line := c.Cmd("LISTGROUP test.secret"); code != 502 {
		if code == 211 {
			c.ReadDotLines()
		}
		t.Errorf("LISTGROUP test.secret: %q, want 502", line)
	}
	if code, line := c.Cmd("XOVER 1-2"); code != 224 {
		t.Fatalf("XOVER: %q", line)
	}
	for _, l := range c.ReadDotLines() {
		if !strings.Contains(l, "in test.alpha") {
			t.Errorf("XOVER after a refused LISTGROUP: %q, want test.alpha's", l)
		}
	}
}

// TestGroupDenied502: without a way to log in, a group the client may not
// read gets 502 (RFC 4643: 480 only when a login could help).
func TestGroupDenied502(t *testing.T) {
	e, _, _ := readEnv(t)
	c := e.session()
	if code, line := c.Cmd("GROUP test.secret"); code != 502 {
		t.Errorf("GROUP test.secret: %q, want 502", line)
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP test.alpha: %q, want 211", line)
	}
}

// TestArticleByMessageIDReadRights: ARTICLE, HEAD, BODY and STAT with a
// message-ID didn't check the Read list at all, so a client that may read
// only some groups got any article whose message-ID it knew.
func TestArticleByMessageIDReadRights(t *testing.T) {
	e, alpha, secret := readEnv(t)
	// crossposted to a group the client may read: allowed
	e.Store.AddArticle("test.secret", fakeserver.Article{
		Number:    3,
		MessageID: "<cross@upstream.test>",
		Headers: []string{
			"From: poster@upstream.test",
			"Subject: crossposted",
			"Newsgroups: test.secret, test.alpha",
		},
		Body: []string{"crossposted body"},
	})
	c := e.session()
	for _, cmd := range []string{"ARTICLE", "HEAD", "BODY", "STAT"} {
		code, line := c.Cmd("%s %s", cmd, secret[0])
		if code >= 220 && code <= 222 {
			c.ReadDotLines()
		}
		if code != 502 {
			t.Errorf("%s %s: %q, want 502", cmd, secret[0], line)
		}
	}
	for _, id := range []string{alpha[0], "<cross@upstream.test>"} {
		code, line := c.Cmd("ARTICLE %s", id)
		if code != 220 {
			t.Errorf("ARTICLE %s: %q, want 220", id, line)
			continue
		}
		c.ReadDotLines()
	}
	if code, line := c.Cmd("STAT %s", alpha[1]); code != 223 {
		t.Errorf("STAT %s: %q, want 223", alpha[1], line)
	}
}

// TestReadDeniedLogin: while a login could give the right, the answer is
// 480; a login that does gives access, one that doesn't gets 502.
func TestReadDeniedLogin(t *testing.T) {
	e, _, secret := readEnv(t,
		"tester:"+md5Hash+":*:test.secret:test.alpha:read",
		"other:"+md5Hash+":*:test.alpha:test.alpha:read")
	c := e.session()
	if code, line := c.Cmd("GROUP test.secret"); code != 480 {
		t.Errorf("GROUP test.secret before login: %q, want 480", line)
	}
	if code, line := c.Cmd("ARTICLE %s", secret[0]); code != 480 {
		if code == 220 {
			c.ReadDotLines()
		}
		t.Errorf("ARTICLE %s before login: %q, want 480", secret[0], line)
	}
	login(t, c)
	if code, line := c.Cmd("GROUP test.secret"); code != 211 {
		t.Errorf("GROUP test.secret after login: %q, want 211", line)
	}
	if code, line := c.Cmd("ARTICLE %s", secret[0]); code != 220 {
		t.Errorf("ARTICLE %s after login: %q, want 220", secret[0], line)
	} else {
		c.ReadDotLines()
	}

	c2 := e.session()
	if code, line := c2.Cmd("AUTHINFO USER other"); code != 381 {
		t.Fatalf("AUTHINFO USER other: %q", line)
	}
	if code, line := c2.Cmd("AUTHINFO PASS secret"); code != 281 {
		t.Fatalf("AUTHINFO PASS: %q", line)
	}
	if code, line := c2.Cmd("GROUP test.secret"); code != 502 {
		t.Errorf("GROUP test.secret after a login without the right: %q, want 502", line)
	}
}

// TestPostNewsgroupsList: the PostTo check split the Newsgroups header
// wrongly. It took the end of a name as a length, so with three or more
// groups a name was checked with the following ones appended (which
// matched only if PostTo listed them in the same order), and it kept the
// space after a comma.
func TestPostNewsgroupsList(t *testing.T) {
	e := newEnv(t)
	for _, g := range []string{"test.alpha", "test.beta", "test.gamma", "test.secret"} {
		e.addArticles(g, 1)
	}
	e.AccessList = `AccessList {
  Client stdin {
    allow read post
    List *
    Read *
    PostTo test.gamma,test.beta,test.alpha
    Authentication none
  }
  Default {
    allow none
  }
}`
	post := func(n int, groups string) (int, string) {
		c := e.session()
		defer c.Cmd("QUIT")
		if code, line := c.Cmd("POST"); code != 340 {
			t.Fatalf("POST: %q", line)
		}
		if err := c.Send(fmt.Sprintf("Newsgroups: %s\r\nFrom: client@newscache.test\r\n"+
			"Subject: list %d\r\nMessage-ID: <list%d@newscache.test>\r\n\r\nhello\r\n.\r\n", groups, n, n)); err != nil {
			t.Fatal(err)
		}
		code, line, err := c.ReadStatus()
		if err != nil {
			t.Fatal(err)
		}
		return code, line
	}
	if code, line := post(1, "test.alpha,test.beta,test.gamma"); code != 240 {
		t.Errorf("post to three allowed groups: %q, want 240", line)
	}
	if code, line := post(2, "test.alpha, test.beta"); code != 240 {
		t.Errorf("post with a space after the comma: %q, want 240", line)
	}
	if code, line := post(3, "test.alpha,test.secret"); code != 441 {
		t.Errorf("post to a group not in PostTo: %q, want 441", line)
	}
	if n := len(e.Store.Posted()); n != 2 {
		t.Errorf("upstream got %d posted articles, want 2", n)
	}
}

// TestHeaderByMessageIDReadRights: XPAT with a message-ID returned the
// header of any article, also in groups the client may not read (the
// Read check of ARTICLE <id> came later than XPAT). XHDR and HDR with a
// message-ID check it too.
func TestHeaderByMessageIDReadRights(t *testing.T) {
	e, alpha, secret := readEnv(t)
	c := e.session()
	for _, cmd := range []string{
		"XPAT subject " + secret[0] + " *",
		"XHDR subject " + secret[0],
		"HDR subject " + secret[0],
	} {
		code, line := c.Cmd("%s", cmd)
		if code == 221 || code == 225 {
			c.ReadDotLines()
		}
		if code != 502 {
			t.Errorf("%s: %q, want 502", cmd, line)
		}
	}
	for _, tc := range []struct {
		cmd  string
		code int
	}{
		{"XPAT subject " + alpha[0] + " *", 221},
		{"XHDR subject " + alpha[0], 221},
		{"HDR subject " + alpha[0], 225},
	} {
		code, line := c.Cmd("%s", tc.cmd)
		if code != tc.code {
			t.Errorf("%s: %q, want %d", tc.cmd, line, tc.code)
			continue
		}
		if got := c.ReadDotLines(); len(got) != 1 {
			t.Errorf("%s: %q, want one line", tc.cmd, got)
		}
	}
}

// TestPostCutOff: an article whose input ends before the final "." (the
// client disconnects in the middle) is not posted. It went upstream cut
// off: the reading stopped at the end of the input without an error.
func TestPostCutOff(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.standalone()
	conn, err := net.Dial("tcp", e.Listen)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	if l, err := r.ReadString('\n'); err != nil || !strings.HasPrefix(l, "200") {
		t.Fatalf("greeting: %q %v", l, err)
	}
	io.WriteString(conn, "POST\r\n")
	if l, err := r.ReadString('\n'); err != nil || !strings.HasPrefix(l, "340") {
		t.Fatalf("POST: %q %v", l, err)
	}
	io.WriteString(conn, "Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n"+
		"Subject: cut off\r\nMessage-ID: <cutoff@newscache.test>\r\n\r\nfirst line of the body\r\n")
	conn.(*net.TCPConn).CloseWrite()
	if l, err := r.ReadString('\n'); err == nil && !strings.HasPrefix(l, "441") {
		t.Errorf("reply to a cut-off article: %q, want 441 or none", l)
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(e.Store.Posted()); n != 0 {
		t.Errorf("upstream got %d posted articles, want none (the article was cut off)", n)
	}
}

// TestPostSizeLimit: POST takes articles up to MaxPostSize (1 MiB by
// default); a larger one is refused with 441, read to its "." line (the
// session goes on) and not posted. There was no limit: an endless article
// took all the memory.
func TestPostSizeLimit(t *testing.T) {
	t.Run("default", func(t *testing.T) { testPostSizeLimit(t, "", 1100, 900) })
	// MaxPostSize sets the limit (in bytes)
	t.Run("MaxPostSize 100000", func(t *testing.T) { testPostSizeLimit(t, "MaxPostSize 100000", 100, 90) })
}

// testPostSizeLimit posts an article of tooLarge KB, which must get 441,
// and then one of fits KB, which must be posted.
func testPostSizeLimit(t *testing.T, extra string, tooLarge, fits int) {
	e := newEnv(t)
	e.Extra = extra
	e.addArticles("test.alpha", 1)
	c := e.session()
	c.Deadline = 60 * time.Second
	post := func(n, kb int) (int, string) {
		if code, line := c.Cmd("POST"); code != 340 {
			t.Fatalf("POST: %q", line)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\nSubject: size %d\r\n"+
			"Message-ID: <size%d@newscache.test>\r\n\r\n", n, n)
		line := strings.Repeat("x", 1022) + "\r\n"
		for i := 0; i < kb; i++ {
			b.WriteString(line)
		}
		b.WriteString(".\r\n")
		if err := c.Send(b.String()); err != nil {
			t.Fatal(err)
		}
		code, reply, err := c.ReadStatus()
		if err != nil {
			t.Fatal(err)
		}
		return code, reply
	}
	if code, line := post(1, tooLarge); code != 441 {
		t.Errorf("POST of %d KB: %q, want 441", tooLarge, line)
	}
	if code, line := post(2, fits); code != 240 {
		t.Errorf("POST of %d KB: %q, want 240", fits, line)
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Errorf("DATE after the posts: %q, want 111", line)
	}
	if n := len(e.Store.Posted()); n != 1 {
		t.Errorf("upstream got %d posted articles, want 1 (the %d KB one)", n, fits)
	}
}

// TestPostNewsgroupsSeparators: the PostTo check and the choice of the
// upstream servers to post to split the Newsgroups header differently.
// The check split on commas only, and its matcher stopped at white
// space, so "test.alpha test.secret" was checked as test.alpha; the
// posting split on white space too and sent the article to the servers
// of both groups, test.secret's included. The client may post to
// test.alpha and test.beta and not to test.secret, which a second
// upstream (B) serves. Whatever separates an allowed and the denied name,
// the post is refused (441) and reaches neither server. A blank between
// two allowed names is refused too: RFC 5536 separates names with commas
// only, and what the upstream makes of "a b" is its own reading (the
// other way to fix this, splitting on blanks as the posting did, would
// have accepted it).
func TestPostNewsgroupsSeparators(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.addArticles("test.beta", 1)
	st := fakeserver.NewStore()
	st.AddGroup("test.secret", "on B")
	st.AddArticle("test.secret", groupArticle("test.secret", 1, []string{"body on B"}))
	b := fakeserver.New(t, fakeserver.Options{Store: st})
	e.ExtraServers = serverBlock(b, "test.secret")
	e.AccessList = `AccessList {
  Client stdin {
    allow read post
    List *
    Read *
    PostTo test.alpha,test.beta,!test.secret
    Authentication none
  }
  Default {
    allow none
  }
}`
	// ok: posted (240) to upstream A and not B; else 441 and nowhere
	cases := []struct {
		name, groups string
		ok           bool
	}{
		{"two allowed, comma", "test.alpha,test.beta", true},
		{"two allowed, comma space", "test.alpha, test.beta", true},
		{"two allowed, blank only", "test.alpha test.beta", false},
		{"comma", "test.alpha,test.secret", false},
		{"comma space", "test.alpha, test.secret", false},
		{"space comma", "test.alpha ,test.secret", false},
		{"space comma space", "test.alpha , test.secret", false},
		{"trailing space", "test.alpha,test.secret ", false},
		{"comma tab", "test.alpha,\ttest.secret", false},
		{"folded", "test.alpha,\r\n test.secret", false},
		{"space only", "test.alpha test.secret", false},
		{"tab only", "test.alpha\ttest.secret", false},
		{"folded no comma", "test.alpha\r\n test.secret", false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a0, b0 := len(e.Store.Posted()), len(st.Posted())
			c := e.session()
			defer c.Cmd("QUIT")
			if code, line := c.Cmd("POST"); code != 340 {
				t.Fatalf("POST: %q", line)
			}
			if err := c.Send(fmt.Sprintf("Newsgroups: %s\r\nFrom: client@newscache.test\r\n"+
				"Subject: sep %d\r\nMessage-ID: <sep%d@newscache.test>\r\n\r\nhello\r\n.\r\n", tc.groups, i, i)); err != nil {
				t.Fatal(err)
			}
			code, line, err := c.ReadStatus()
			if err != nil {
				t.Fatal(err)
			}
			want, wantA := 441, 0
			if tc.ok {
				want, wantA = 240, 1
			}
			if code != want {
				t.Errorf("Newsgroups %q: %q, want %d", tc.groups, line, want)
			}
			if n := len(e.Store.Posted()) - a0; n != wantA {
				t.Errorf("Newsgroups %q: upstream A got %d article(s), want %d", tc.groups, n, wantA)
			}
			if n := len(st.Posted()) - b0; n != 0 {
				t.Errorf("Newsgroups %q: upstream B (test.secret, denied) got %d article(s)", tc.groups, n)
			}
		})
	}
}
