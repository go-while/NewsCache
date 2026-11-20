package e2e

import (
	"crypto/tls"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// A news server, or a load balancer in front of it, that accepts a
// connection, greets and closes it while NewsCache sets it up (in reply to
// MODE READER, STARTTLS, AUTHINFO, COMPRESS DEFLATE or LIST OVERVIEW.FMT),
// or whose reply to one of them takes longer than a status line's deadline:
// connect() connects again, up to Retries times, as after a greeting that
// didn't come. issue() threw ResponseError for the missing reply instead,
// before connect()'s check of the connection ran, so the client's command
// failed on one dropped set-up (and inside a command's own reconnect, the
// ResponseError went past issue()'s retries too). A refused login or
// STARTTLS still fails at once.

const setupRetries = 2

var setupServerLines = fmt.Sprintf("    Retries %d", setupRetries)

// setupServer is an upstream that closes the connection with nothing sent
// when it reads the client line at (lower case, e.g. "mode reader"): on the
// next left connections that send it, or on every one while left is
// negative. With drip it drips that command's status line instead
// (statusDrip) and then closes; with cut it writes cut (no line end) before
// it closes. closeGroup and closePost close the connection, the next n
// times, at "group test.beta" and before the reply to a posted article.
type setupServer struct {
	at         string
	cut        string
	drip       bool
	tlsConf    *tls.Config // STARTTLS with it; nil: 502
	left       atomic.Int32
	closeGroup atomic.Int32
	closePost  atomic.Int32
	*fakeserver.Server
}

// takeOne uses up one of n (negative: never used up); false when n is 0.
func takeOne(n *atomic.Int32) bool {
	for {
		v := n.Load()
		switch {
		case v < 0:
			return true
		case v == 0:
			return false
		case n.CompareAndSwap(v, v-1):
			return true
		}
	}
}

// setupStatus is the status line of the normal reply to a set-up command.
var setupStatus = map[string]string{
	"mode reader":       "200 reader ready",
	"list overview.fmt": "215 order of fields follows",
}

func (s *setupServer) serve(c *fakeserver.Conn) {
	for {
		line, err := c.ReadLine()
		if err != nil {
			return
		}
		low := strings.ToLower(line)
		if low == s.at && takeOne(&s.left) {
			switch {
			case s.drip:
				statusDrip(c, setupStatus[low]+", this status line comes a byte at a time")
			case s.cut != "":
				c.Raw().Write([]byte(s.cut))
			}
			return
		}
		switch {
		case low == "mode reader":
			err = c.WriteLines(setupStatus[low])
		case low == "starttls":
			if s.tlsConf == nil {
				err = c.WriteLines("502 STARTTLS not available")
				break
			}
			if err = c.WriteLines("382 continue with TLS negotiation"); err == nil {
				err = c.StartTLS(s.tlsConf)
			}
		case low == "authinfo user tester":
			err = c.WriteLines("381 password required")
		case low == "authinfo pass secret":
			err = c.WriteLines("281 authentication accepted")
		case strings.HasPrefix(low, "authinfo "):
			err = c.WriteLines("481 authentication failed")
		case low == "compress deflate":
			if err = c.WriteLines("206 compression active"); err == nil {
				err = c.StartCompress()
			}
		case low == "list overview.fmt":
			err = c.WriteLines(setupStatus[low], "Subject:", "From:", "Date:",
				"Message-ID:", "References:", ":bytes", ":lines", ".")
		case low == "group test.alpha":
			err = c.WriteLines("211 5 1 5 test.alpha")
		case low == "group test.beta":
			if takeOne(&s.closeGroup) {
				return
			}
			err = c.WriteLines("211 5 1 5 test.beta")
		case strings.HasPrefix(low, "group "):
			err = c.WriteLines("411 no such group")
		case strings.HasPrefix(low, "stat "):
			err = c.WriteLines("430 no such article")
		case low == "post":
			if c.WriteLines("340 send it") != nil {
				return
			}
			if _, err := c.ReadDotBlock(); err != nil {
				return
			}
			if takeOne(&s.closePost) {
				return
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

// setupEnv points a new env at a setupServer (s is filled in and started
// here; set its fields other than the counters before). mode: "plain",
// "compress" (Options compress), "starttls" (Options starttls; the server
// does STARTTLS when tlsOK, else answers 502) or "auth" (User and Password,
// which the server accepts).
func setupEnv(t *testing.T, s *setupServer, mode string, tlsOK bool) *env {
	t.Helper()
	e := newEnv(t)
	e.ServerLines = setupServerLines
	switch mode {
	case "plain":
	case "compress":
		requireZlib(t, e.Bin)
		e.ServerOptions = "compress"
	case "starttls":
		requireTLS(t, e.Bin)
		ca := newTestCA(t, e.Dir)
		if tlsOK {
			s.tlsConf = &tls.Config{Certificates: []tls.Certificate{
				ca.serverCert(t, []string{"localhost"}, validFrom, validUntil)}}
		}
		e.ServerHost = "localhost"
		e.ServerOptions = "starttls"
		e.ServerLines += "\n    TLSCAFile " + ca.file
	case "auth":
		e.ServerLines += "\n    User tester\n    Password secret"
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	s.Server = fakeserver.New(t, fakeserver.Options{Deadline: 2 * time.Minute, Handler: s.serve})
	e.Upstream = s.Server
	return e
}

// TestUpstreamSetupClosedOnce: the news server closes the first connection
// at one of the set-up commands, with nothing sent. The client's GROUP
// failed (403); now it gets the group from a second connection. The
// "compress cut 206" case sends a 206 without its line end before it
// closes: that is no reply, and the connection is made again (no
// compression is taken as negotiated on a closed connection).
func TestUpstreamSetupClosedOnce(t *testing.T) {
	for _, tc := range []struct {
		name, mode, at, cut string
	}{
		{"mode reader", "plain", "mode reader", ""},
		{"list overview.fmt", "plain", "list overview.fmt", ""},
		{"compress", "compress", "compress deflate", ""},
		{"compress cut 206", "compress", "compress deflate", "206 compression active"},
		{"starttls", "starttls", "starttls", ""},
		{"authinfo user", "auth", "authinfo user tester", ""},
		{"authinfo pass", "auth", "authinfo pass secret", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &setupServer{at: tc.at, cut: tc.cut}
			s.left.Store(1)
			e := setupEnv(t, s, tc.mode, true)
			p, c := e.sessionProcess()
			c.Deadline = 30 * time.Second
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Errorf("GROUP with the first connection closed at %q: %q, want 211 from a new connection", tc.at, line)
			}
			if n := s.Accepted(); n != 2 {
				t.Errorf("%d connections to the news server, want 2 (the closed one and one more)", n)
			}
			if n := s.Count("GROUP"); n != 1 {
				t.Errorf("GROUP reached the news server %d times, want once", n)
			}
			quitSession(t, p, c)
		})
	}
}

// TestUpstreamSetupClosedAlways: a news server that closes every connection
// at MODE READER: the client gets an error after the first connection and
// Retries more, not more, and the session goes on; once the server answers
// again, the next GROUP goes out on one new connection.
func TestUpstreamSetupClosedAlways(t *testing.T) {
	s := &setupServer{at: "mode reader"}
	s.left.Store(-1)
	e := setupEnv(t, s, "plain", false)
	p, c := e.sessionProcess()
	c.Deadline = 30 * time.Second
	if code, line := c.Cmd("GROUP test.alpha"); code < 400 {
		t.Errorf("GROUP with every connection closed at MODE READER: %q, want an error", line)
	}
	if n := s.Accepted(); n != setupRetries+1 {
		t.Errorf("%d connections to the news server, want %d (the first and Retries more)", n, setupRetries+1)
	}
	if n := s.Count("GROUP"); n != 0 {
		t.Errorf("GROUP reached the news server %d times, want never", n)
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Fatalf("DATE after the failed GROUP: %q, want 111", line)
	}
	s.left.Store(0)
	accepted := s.Accepted()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP once the server answers again: %q, want 211", line)
	}
	if n := s.Accepted() - accepted; n != 1 {
		t.Errorf("GROUP once the server answers again made %d new connections, want 1", n)
	}
	quitSession(t, p, c)
}

// TestUpstreamReconnectSetupClosed: the connection breaks during a command
// (here the server closes it at GROUP test.beta), and the new connection
// is closed at MODE READER. The ResponseError of that set-up went past
// issue()'s own retries (they take only a SystemError), and the client got
// an error at once. "once": the next connection works, and the client gets
// the group. "always": every new connection is closed; the client gets an
// error after at most Retries reconnects, each with up to Retries more
// connection attempts of its own.
func TestUpstreamReconnectSetupClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		left int32
	}{
		{"once", 1},
		{"always", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &setupServer{at: "mode reader"}
			e := setupEnv(t, s, "plain", false)
			p, c := e.sessionProcess()
			c.Deadline = 30 * time.Second
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP test.alpha: %q", line)
			}
			s.closeGroup.Store(1)
			s.left.Store(tc.left)
			code, line := c.Cmd("GROUP test.beta")
			n := s.Accepted()
			if tc.left > 0 {
				if code != 211 {
					t.Errorf("GROUP test.beta with the new connection closed once at MODE READER: %q, want 211", line)
				}
				if n != 3 {
					t.Errorf("%d connections to the news server, want 3 (the first, the one closed at MODE READER, one more)", n)
				}
			} else {
				if code < 400 {
					t.Errorf("GROUP test.beta with every new connection closed: %q, want an error", line)
				}
				// (the first connection, then Retries reconnects of
				// up to Retries+1 attempts each)
				if most := 1 + setupRetries*(setupRetries+1); n < 1+setupRetries || n > most {
					t.Errorf("%d connections to the news server, want %d to %d", n, 1+setupRetries, most)
				}
				if code, line := c.Cmd("DATE"); code != 111 {
					t.Fatalf("DATE after the failed GROUP: %q, want 111", line)
				}
			}
			quitSession(t, p, c)
		})
	}
}

// TestUpstreamSetupRefusalNotRetried: a refusal in the set-up is an answer,
// not a broken connection: a refused login or a refused STARTTLS (Options
// starttls: no plain text instead) fails the command on the first
// connection, without connecting again.
func TestUpstreamSetupRefusalNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
	}{
		{"login refused", "auth"},
		{"starttls refused", "starttls"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &setupServer{}
			e := setupEnv(t, s, tc.mode, false)
			if tc.mode == "auth" {
				e.ServerLines = setupServerLines + "\n    User tester\n    Password wrong"
			}
			p, c := e.sessionProcess()
			c.Deadline = 30 * time.Second
			if code, line := c.Cmd("GROUP test.alpha"); code < 400 {
				t.Errorf("GROUP with the %s: %q, want an error", tc.name, line)
			}
			if n := s.Accepted(); n != 1 {
				t.Errorf("%d connections to the news server, want 1 (a refusal isn't retried)", n)
			}
			if n := s.Count("GROUP"); n != 0 {
				t.Errorf("GROUP reached the news server %d times, want never", n)
			}
			quitSession(t, p, c)
		})
	}
}

// TestUpstreamRetriesZeroReconnects: with Retries 0, a connection that
// broke during a command (the server closed it at GROUP, or before its
// reply to a posted article) stayed in place: is_connected() was true, so
// every later command of the session was sent on it and failed, without a
// new connection. Now the next command connects again.
func TestUpstreamRetriesZeroReconnects(t *testing.T) {
	for _, tc := range []string{"group", "post"} {
		t.Run(tc, func(t *testing.T) {
			t.Parallel()
			s := &setupServer{}
			e := setupEnv(t, s, "plain", false)
			e.ServerLines = "    Retries 0"
			p, c := e.sessionProcess()
			c.Deadline = 30 * time.Second
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP test.alpha: %q", line)
			}
			switch tc {
			case "group":
				s.closeGroup.Store(1)
				if code, line := c.Cmd("GROUP test.beta"); code < 400 {
					t.Fatalf("GROUP test.beta with the connection closed at it: %q, want an error (Retries 0)", line)
				}
			case "post":
				s.closePost.Store(1)
				if code, line := c.Cmd("POST"); code != 340 {
					t.Fatalf("POST: %q", line)
				}
				if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\n" +
					"Subject: lost reply\r\nMessage-ID: <retries0.post@client.test>\r\n\r\nhello\r\n.\r\n"); err != nil {
					t.Fatal(err)
				}
				code, line, err := c.ReadStatus()
				if err != nil {
					t.Fatalf("posting: %v", err)
				}
				if code != 240 {
					t.Fatalf("posting with the connection closed before the reply: %q, want 240 (queued in the spool)", line)
				}
			}
			accepted := s.Accepted()
			if code, line := c.Cmd("GROUP test.beta"); code != 211 {
				t.Errorf("GROUP test.beta after the broken connection: %q, want 211 from a new connection", line)
			}
			if n := s.Accepted() - accepted; n != 1 {
				t.Errorf("GROUP test.beta after the broken connection made %d new connections, want 1", n)
			}
			quitSession(t, p, c)
		})
	}
}

// TestUpstreamSetupDrippedOnce: the first connection's reply to a set-up
// command comes a byte at a time and runs past the status line's deadline
// (ReadTimeout + STATUS_LINE_SECONDS). That ended the client's GROUP with
// 403; now the connection is made again (Retries) and the client gets the
// group. Needs the short-limits build.
func TestUpstreamSetupDrippedOnce(t *testing.T) {
	skipUnlessShortReplyLimits(t)
	for _, at := range []string{"mode reader", "list overview.fmt"} {
		t.Run(at, func(t *testing.T) {
			t.Parallel()
			s := &setupServer{at: at, drip: true}
			s.left.Store(1)
			e := setupEnv(t, s, "plain", false)
			e.ServerLines = statusServerLines
			p, c := e.sessionProcess()
			c.Deadline = statusClientDeadline
			code, line, took := timedCmd(t, c, "GROUP test.alpha")
			if code != 211 {
				t.Errorf("GROUP with the reply to %q dripped on the first connection: %q, want 211 from a new connection", at, line)
			}
			if took > statusBound {
				t.Errorf("GROUP took %v, want at most %v", took.Round(time.Second), statusBound)
			}
			if n := s.Accepted(); n != 2 {
				t.Errorf("%d connections to the news server, want 2 (the dripped one and one more)", n)
			}
			quitSession(t, p, c)
			if n := statusDeadlineLogs(t, e); n >= 0 && n != 1 {
				t.Errorf("%d status lines given up at the deadline in the log, want 1", n)
			}
		})
	}
}
