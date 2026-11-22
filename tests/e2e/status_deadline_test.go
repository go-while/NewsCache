package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
)

// A status line of the news server (the greeting, the reply to a command,
// the reply to a posted article) has a deadline of its own: ReadTimeout +
// STATUS_LINE_SECONDS (2 s in the short-limit build). ReadTimeout limits
// each wait for data, not the line: a server that sent the line a byte at
// a time, each byte within ReadTimeout, held the session for the whole line
// (up to 16384 bytes, so up to 16384 times ReadTimeout). The upstream here
// drips one of its status lines with statusDripGap between the bytes, well
// below ReadTimeout (the margin keeps a loaded test host from running into
// ReadTimeout instead), so no single read times out, but the line takes far
// longer than its deadline.

const (
	statusReadTimeout = 4                       // ReadTimeout of the upstream, seconds
	statusLineSeconds = 2                       // STATUS_LINE_SECONDS of the short-limit build
	statusRetries     = 1                       // Retries of the upstream
	statusDripGap     = 1000 * time.Millisecond // a quarter of ReadTimeout

	// one status line's deadline
	statusDeadline = (statusReadTimeout + statusLineSeconds) * time.Second
	// The longest a command may take: each attempt (the first and
	// Retries more) up to its deadline, plus the second connect() waits
	// before it connects again and the connection set-up, plus slack.
	statusBound = (statusRetries+1)*(statusDeadline+time.Second) + 5*time.Second
	// The client's patience: longer than statusBound, much shorter than
	// one dripped line takes (each of them is over 40 bytes, 60 s), so
	// without the deadline the command is still waiting when it ends.
	statusClientDeadline = 40 * time.Second
)

var statusServerLines = fmt.Sprintf("    ReadTimeout %d\n    Retries %d", statusReadTimeout, statusRetries)

// The dripped lines. The numbers in the dripped 211 differ from those of
// the normal reply, so that a part of it taken for a later reply shows.
const (
	dripGreeting = "200 fake news server ready, this greeting comes a byte at a time"
	dripGroup    = "211 9 3 11 test.alpha group selected, a byte at a time"
	dripPosted   = "240 article received and posted, a byte at a time"
	normalGroup  = "211 5 1 5 test.alpha"
)

func skipUnlessShortReplyLimits(t *testing.T) {
	t.Helper()
	if os.Getenv("NEWSCACHE_SHORT_REPLY_LIMITS") != "1" {
		t.Skip("requires a short-limits build (-DNEWSCACHE_TEST_SHORT_REPLY_LIMITS) and NEWSCACHE_SHORT_REPLY_LIMITS=1")
	}
}

// statusDripServer is an upstream that drips its greeting, its reply to
// GROUP or its reply to a posted article while the matching flag is set.
// With compress it takes COMPRESS DEFLATE.
type statusDripServer struct {
	greeting, group, post atomic.Bool
	*fakeserver.Server
}

func newStatusDripServer(t *testing.T) *statusDripServer {
	t.Helper()
	s := &statusDripServer{}
	s.Server = fakeserver.New(t, fakeserver.Options{NoGreeting: true,
		Deadline: 2 * time.Minute, Handler: s.serve})
	return s
}

// statusDrip writes line and its CRLF a byte at a time, statusDripGap
// apart; false once the connection (or the server) is gone.
func statusDrip(c *fakeserver.Conn, line string) bool {
	b := []byte(line + "\r\n")
	for i := range b {
		if i > 0 && !c.Sleep(statusDripGap) {
			return false
		}
		if _, err := c.Raw().Write(b[i : i+1]); err != nil {
			return false
		}
	}
	return true
}

func (s *statusDripServer) serve(c *fakeserver.Conn) {
	if s.greeting.Load() {
		if !statusDrip(c, dripGreeting) {
			return
		}
	} else if c.WriteLines("200 fake news server ready") != nil {
		return
	}
	for {
		line, err := c.ReadLine()
		if err != nil {
			return
		}
		low := strings.ToLower(line)
		switch {
		case low == "mode reader":
			err = c.WriteLines("200 reader")
		case low == "compress deflate":
			if err = c.WriteLines("206 compression active"); err == nil {
				err = c.StartCompress()
			}
		case low == "list overview.fmt":
			err = c.WriteLines("215 order", "Subject:", "From:", "Date:",
				"Message-ID:", "References:", ":bytes", ":lines", ".")
		case strings.HasPrefix(low, "group "):
			if s.group.Load() {
				if !statusDrip(c, dripGroup) {
					return
				}
				continue
			}
			err = c.WriteLines(normalGroup)
		case strings.HasPrefix(low, "stat "):
			err = c.WriteLines("430 no such article")
		case low == "post":
			if c.WriteLines("340 send it") != nil {
				return
			}
			if _, err := c.ReadDotBlock(); err != nil {
				return
			}
			if s.post.Load() {
				if !statusDrip(c, dripPosted) {
					return
				}
				continue
			}
			err = c.WriteLines("240 article posted")
		case low == "quit":
			c.WriteLines("205 bye")
			return
		default:
			err = c.WriteLines("500 unknown command")
		}
		if err != nil {
			return
		}
	}
}

// statusDripEnv points a new env at a statusDripServer: directly
// ("plain"), through a TLS proxy (Options ssl) or with Options compress.
func statusDripEnv(t *testing.T, mode string) (*env, *statusDripServer) {
	t.Helper()
	e := newEnv(t)
	s := newStatusDripServer(t)
	e.Upstream = s.Server
	e.ServerLines = statusServerLines
	switch mode {
	case "plain":
	case "tls":
		requireTLS(t, e.Bin)
		ca := newTestCA(t, e.Dir)
		p := newTLSProxy(t, s.Addr(), ca.serverCert(t, []string{"localhost"}, validFrom, validUntil), false, false)
		e.ServerHost = "localhost"
		e.ServerPort = p.port()
		e.ServerOptions = "ssl"
		e.ServerLines += "\n    TLSCAFile " + ca.file
	case "compress":
		requireZlib(t, e.Bin)
		e.ServerOptions = "compress"
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	return e, s
}

// statusDeadlineLogs counts the log lines of status lines given up at
// their deadline; -1 for a syslog build, whose log the test can't read.
func statusDeadlineLogs(t *testing.T, e *env) int {
	t.Helper()
	mode, err := logMode(e.Bin)
	if err != nil {
		t.Fatal(err)
	}
	log, skip, err := readLogFile(mode, filepath.Join(e.LogDir, "newscache.log"))
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		return -1
	}
	return strings.Count(log, "took too long to send a status line")
}

// timedCmd sends a command and fails the test when no reply came within
// the client's deadline: the session waited for the whole dripped line.
func timedCmd(t *testing.T, c *client.Conn, cmd string) (int, string, time.Duration) {
	t.Helper()
	start := time.Now()
	code, line, err := c.TryCmd("%s", cmd)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("%s: no reply after %v (%v): the session waits for the whole dripped status line", cmd, took.Round(time.Second), err)
	}
	t.Logf("%s -> %q after %v", cmd, line, took.Round(10*time.Millisecond))
	return code, line, took
}

// afterStatusFailure checks the session after the failed command: the next
// GROUP, with the upstream answering normally again, goes out on one new
// connection and gets the real reply, nothing of the cut line.
func afterStatusFailure(t *testing.T, s *statusDripServer, c *client.Conn) {
	t.Helper()
	s.greeting.Store(false)
	s.group.Store(false)
	s.post.Store(false)
	accepted, groups := s.Accepted(), s.Count("GROUP")
	code, line, _ := timedCmd(t, c, "GROUP test.alpha")
	if code != 211 || !strings.HasPrefix(line, normalGroup+" ") {
		t.Errorf("GROUP after the failure: %q, want %q... (from a new connection, not the rest of the cut line)", line, normalGroup)
	}
	if n := s.Accepted() - accepted; n != 1 {
		t.Errorf("GROUP after the failure made %d new connections, want 1", n)
	}
	if n := s.Count("GROUP") - groups; n != 1 {
		t.Errorf("GROUP after the failure reached the news server %d times, want once", n)
	}
}

// TestUpstreamDrippedGreeting: a greeting dripped a byte at a time ends
// each connection attempt at its deadline; after Retries more attempts the
// client gets 403 instead of waiting for the greeting. Plain and TLS.
func TestUpstreamDrippedGreeting(t *testing.T) {
	skipUnlessShortReplyLimits(t)
	for _, mode := range []string{"plain", "tls"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			e, s := statusDripEnv(t, mode)
			s.greeting.Store(true)
			p, c := e.sessionProcess()
			c.Deadline = statusClientDeadline
			code, line, took := timedCmd(t, c, "GROUP test.alpha")
			if code != 403 {
				t.Errorf("GROUP with a dripped greeting: %q, want 403", line)
			}
			if took > statusBound {
				t.Errorf("GROUP took %v, want at most %v (%d attempts of %v)", took.Round(time.Second), statusBound, statusRetries+1, statusDeadline)
			}
			if n := s.Accepted(); n != statusRetries+1 {
				t.Errorf("%d connections to the news server, want %d (the first and Retries more)", n, statusRetries+1)
			}
			if n := s.Count("GROUP"); n != 0 {
				t.Errorf("GROUP reached the news server %d times without a greeting", n)
			}
			afterStatusFailure(t, s, c)
			quitSession(t, p, c)
			if n := statusDeadlineLogs(t, e); n >= 0 && n != statusRetries+1 {
				t.Errorf("%d greetings given up at the deadline in the log, want %d", n, statusRetries+1)
			}
		})
	}
}

// TestUpstreamDrippedGroupStatus: the reply to GROUP dripped a byte at a
// time ends at its deadline; the command is sent again on a new connection
// (Retries) and then the client gets 403. Plain, TLS and COMPRESS.
func TestUpstreamDrippedGroupStatus(t *testing.T) {
	skipUnlessShortReplyLimits(t)
	for _, mode := range []string{"plain", "tls", "compress"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			e, s := statusDripEnv(t, mode)
			s.group.Store(true)
			p, c := e.sessionProcess()
			c.Deadline = statusClientDeadline
			code, line, took := timedCmd(t, c, "GROUP test.alpha")
			if code != 403 {
				t.Errorf("GROUP with a dripped reply: %q, want 403", line)
			}
			if took > statusBound {
				t.Errorf("GROUP took %v, want at most %v (%d attempts of %v)", took.Round(time.Second), statusBound, statusRetries+1, statusDeadline)
			}
			if n := s.Count("GROUP"); n != statusRetries+1 {
				t.Errorf("GROUP reached the news server %d times, want %d (the first and Retries more)", n, statusRetries+1)
			}
			if n := s.Accepted(); n != statusRetries+1 {
				t.Errorf("%d connections to the news server, want %d (a new one for each attempt)", n, statusRetries+1)
			}
			afterStatusFailure(t, s, c)
			quitSession(t, p, c)
			if n := statusDeadlineLogs(t, e); n >= 0 && n != statusRetries+1 {
				t.Errorf("%d status lines given up at the deadline in the log, want %d", n, statusRetries+1)
			}
		})
	}
}

// TestUpstreamDrippedPostStatus: the news server's reply to the posted
// article dripped a byte at a time ends at its deadline. The client is past
// its 340 and has sent the article. Here every attempt drips its reply, so
// when the retries are used up the post goes to the spool queue (sent again
// by updatenews) and the client gets 240, soon. (A server that kept the
// first copy answers the STAT before the resend with 223, and the client
// gets 240 without a second copy; this fake answers 430 to every STAT.)
// Plain, TLS and COMPRESS.
func TestUpstreamDrippedPostStatus(t *testing.T) {
	skipUnlessShortReplyLimits(t)
	for _, mode := range []string{"plain", "tls", "compress"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			e, s := statusDripEnv(t, mode)
			s.post.Store(true)
			p, c := e.sessionProcess()
			c.Deadline = statusClientDeadline
			if code, line := c.Cmd("POST"); code != 340 {
				t.Fatalf("POST: %q", line)
			}
			start := time.Now()
			if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n" +
				"Subject: dripped reply\r\nMessage-ID: <dripped." + mode + "@client.test>\r\n\r\nhello\r\n.\r\n"); err != nil {
				t.Fatal(err)
			}
			code, line, err := c.ReadStatus()
			took := time.Since(start)
			if err != nil {
				t.Fatalf("posting: no reply after %v (%v): the session waits for the whole dripped status line", took.Round(time.Second), err)
			}
			t.Logf("posting -> %q after %v", line, took.Round(10*time.Millisecond))
			if code != 240 {
				t.Errorf("posting: %q, want 240 (queued in the spool)", line)
			}
			if took > statusBound {
				t.Errorf("posting took %v, want at most %v (%d attempts of %v)", took.Round(time.Second), statusBound, statusRetries+1, statusDeadline)
			}
			if n := len(spooledIn(t, e, ".artSpool")); n != 1 {
				t.Errorf("%d articles queued in the spool, want 1", n)
			}
			if n := s.Count("POST"); n < 1 || n > statusRetries+1 {
				t.Errorf("POST reached the news server %d times, want 1 to %d (Retries)", n, statusRetries+1)
			}
			afterStatusFailure(t, s, c)
			quitSession(t, p, c)
			if n := statusDeadlineLogs(t, e); n >= 0 && (n < 1 || n > statusRetries+1) {
				t.Errorf("%d status lines given up at the deadline in the log, want 1 to %d", n, statusRetries+1)
			}
		})
	}
}

// TestUpstreamDrippedStatusSIGTERM: a session stopped with SIGTERM while
// it reads a dripped status line ends at once, without waiting for the
// line's deadline (63 s here in the normal build), and without a new
// connection to the news server.
func TestUpstreamDrippedStatusSIGTERM(t *testing.T) {
	e, s := statusDripEnv(t, "plain")
	s.group.Store(true)
	p, c := e.sessionProcess()
	if err := c.Send("GROUP test.alpha\r\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the news server has the GROUP", func() bool { return s.Count("GROUP") >= 1 })
	time.Sleep(statusDripGap) // a byte or two of the line came
	accepted := s.Accepted()
	start := time.Now()
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the session still runs 10 s after SIGTERM")
	}
	t.Logf("the session ended %v after SIGTERM", time.Since(start).Round(10*time.Millisecond))
	if n := s.Accepted() - accepted; n != 0 {
		t.Errorf("%d new connections to the news server after SIGTERM, want none", n)
	}
	if n := s.Count("GROUP"); n != 1 {
		t.Errorf("GROUP reached the news server %d times, want once", n)
	}
}
