package e2e

// Regression tests for bugs found while writing the suite; see PLAN-tests.md
// for the original findings.

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// NC-1 (fixed): the access-denied reply ended in a bare LF.
func TestDenyReplyCRLF(t *testing.T) {
	e := newEnv(t)
	e.AccessList = accessEntry("10.0.0.0/8") // 127.0.0.1 falls to Default: deny
	e.standalone()
	c := e.dialRaw()
	if !strings.HasPrefix(c.Greeting, "502 ") {
		t.Fatalf("greeting %q, want 502", c.Greeting)
	}
}

// NC-3 (fixed): an access entry for exactly one host (/32 or
// 255.255.255.255) never matched, because matchaddress returned false after
// comparing all bytes. A plain address now means exactly that host too.
func TestAccessExactHost(t *testing.T) {
	cases := []struct {
		pattern string
		allow   bool
	}{
		{"127.0.0.1/32", true},
		{"127.0.0.1/255.255.255.255", true},
		{"127.0.0.1", true},
		{"127.0.0.0/8", true},
		{"127.0.0.2/32", false},
		{"127.0.0.2", false},
		{"127.0.0.0/31", true},
		{"127.0.0.2/31", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			e := newEnv(t)
			e.AccessList = accessEntry(tc.pattern)
			e.standalone()
			c := e.dialLenient()
			want := "502 "
			if tc.allow {
				want = "200 "
			}
			if !strings.HasPrefix(c.Greeting, want) {
				t.Fatalf("client 127.0.0.1, entry %s: greeting %q, want %s", tc.pattern, c.Greeting, want)
			}
		})
	}
}

// NC-5 (fixed): an upstream article without an Xref header made ARTICLE fail
// with 423 (CServer::filter_xref threw NoSuchFieldError).
func TestArticleWithoutXref(t *testing.T) {
	e := newEnv(t)
	ids := e.addArticlesXref("test.noxref", 2, false)
	c := e.session()
	c.Cmd("GROUP test.noxref")
	if code, line := c.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1: %q, want 220", line)
	}
	c.ReadDotLines()
	if code, line := c.Cmd("ARTICLE %s", ids[1]); code != 220 {
		t.Fatalf("ARTICLE <id>: %q, want 220", line)
	}
	c.ReadDotLines()
	if code, line := c.Cmd("XOVER 1-2"); code != 224 {
		t.Fatalf("XOVER: %q, want 224", line)
	}
	if ov := c.ReadDotLines(); len(ov) != 2 {
		t.Fatalf("XOVER: %d lines, want 2: %q", len(ov), ov)
	}
	c.Cmd("QUIT")

	// served from the spool by a new process
	before := e.Upstream.Count("ARTICLE")
	c2 := e.session()
	c2.Cmd("GROUP test.noxref")
	if code, line := c2.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1 from cache: %q, want 220", line)
	}
	c2.ReadDotLines()
	if n := e.Upstream.Count("ARTICLE"); n != before {
		t.Errorf("upstream ARTICLE count %d -> %d, want served from cache", before, n)
	}
}

// NC-6 (fixed): the log file timestamp had the month off by one (tm_mon is
// 0-based).
func TestLogTimestampMonth(t *testing.T) {
	e := newEnv(t)
	p, c := e.sessionProcess()
	quitSession(t, p, c)
	log := e.logFile()
	want := time.Now().Format("2006/01/")
	if !strings.Contains(log, want) {
		t.Fatalf("log has no timestamp with %q:\n%s", want, firstLines(log, 5))
	}
}

// Smoke test for accepting after a child exits. It cannot deterministically
// reproduce the SIGCHLD-versus-accept errno race.
func TestAcceptAfterChildExit(t *testing.T) {
	e := newEnv(t)
	e.standalone()
	for i := 0; i < 5; i++ {
		c := e.dial()
		c.Cmd("QUIT")
		if err := c.Conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.R.ReadByte(); err != io.EOF {
			t.Fatalf("waiting for child connection to close: %v", err)
		}
		c.Close()
	}
}

func firstLines(s string, n int) string {
	l := strings.SplitN(s, "\n", n+1)
	if len(l) > n {
		l = l[:n]
	}
	return strings.Join(l, "\n")
}

// NC-7 (fixed): on SIGHUP the daemon re-read its config without catching
// parse errors, so a broken config file killed the running daemon. Now it
// keeps the old configuration.
func TestSIGHUPBadConfig(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	p := e.standalone()
	if err := os.WriteFile(e.Conf, []byte("ConfigVersion 5\nNoSuchKeyword 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	mode, err := logMode(e.Bin)
	if err != nil {
		t.Fatal(err)
	}
	// In file-log builds the marker proves the reload was actually handled.
	// Syslog builds retain the connection/liveness smoke check below.
	if mode == "file" {
		e.waitLog("keeping the old configuration")
	}
	c, err := client.TryDial(e.Listen, 2*time.Second)
	if err != nil {
		t.Fatalf("daemon gone after SIGHUP with a broken config: %v", err)
	}
	c = c.WithTB(t)
	defer c.Close()
	// the old configuration (access list, upstream) still works
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP after a failed reload: %q", line)
	}
}

// TestPostHeaderLog: built with --enable-headerlog, a posted article's
// header goes to SpoolDirectory/.headerlog. That build failed to compile
// (ssprintf, a typo for snprintf, since 2008), and newscache -o printed
// the option as --enable_headerlog. Skipped in other builds.
func TestPostHeaderLog(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.writeConf()
	out, err := exec.Command(e.Bin, "-c", e.Conf, "-o").Output()
	if err != nil {
		t.Fatalf("newscache -o: %v", err)
	}
	if !strings.Contains(string(out), "--enable-headerlog") {
		t.Skip("binary built without --enable-headerlog")
	}
	c := e.session()
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("POST: %q", line)
	}
	if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n" +
		"Subject: header log\r\nMessage-ID: <headerlog@newscache.test>\r\n\r\nhello\r\n.\r\n"); err != nil {
		t.Fatal(err)
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 240 {
		t.Fatalf("posting: %q %v", line, err)
	}
	c.Cmd("QUIT")
	b, err := os.ReadFile(filepath.Join(e.Spool, ".headerlog"))
	if err != nil {
		t.Fatalf("no .headerlog: %v", err)
	}
	if !strings.Contains(string(b), "Subject: header log") ||
		strings.Contains(string(b), "hello") {
		t.Errorf(".headerlog should hold the header only:\n%s", b)
	}
}

// TestPostHeaderLogOpenFailure: failure to open .headerlog must be reported,
// without changing the successful NNTP result after the upstream accepted the
// article. The CI header-log build uses a file logger so the error is visible.
func TestPostHeaderLogOpenFailure(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.writeConf()
	out, err := exec.Command(e.Bin, "-c", e.Conf, "-o").Output()
	if err != nil {
		t.Fatalf("newscache -o: %v", err)
	}
	if !strings.Contains(string(out), "--enable-headerlog") {
		t.Skip("binary built without --enable-headerlog")
	}
	headerLog := filepath.Join(e.Spool, ".headerlog")
	if err := os.Mkdir(headerLog, 0o755); err != nil {
		t.Fatal(err)
	}

	c := e.session()
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("POST: %q", line)
	}
	if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n" +
		"Subject: header log\r\nMessage-ID: <headerlog-fail@newscache.test>\r\n\r\nhello\r\n.\r\n"); err != nil {
		t.Fatal(err)
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 240 {
		t.Fatalf("posting: %q %v", line, err)
	}
	c.Cmd("QUIT")
	if log := e.logFile(); !strings.Contains(log, "cannot open "+headerLog+" for header logging") {
		t.Errorf("failed header-log open was not reported:\n%s", log)
	}
}
