package e2e

import (
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
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
// refuse answers the first uses of at, one reply each, in place of the
// normal reply (counted across connections). After a 400 there the server
// closes the connection, unless keepOpen; with closeAll it closes after
// every one of them. With lateBlock, the first reply of refuse is followed
// two seconds later by these lines (a block that comes after NewsCache has
// sent its next command). With needReader it is a mode-switching server: a
// GROUP on a connection without an accepted MODE READER gets "401
// MODE-READER" (RFC 3977 3.2.1) and is counted in groupNoReader.
type setupServer struct {
	at            string
	cut           string
	drip          bool
	tlsConf       *tls.Config // STARTTLS with it; nil: 502
	refuse        []string
	keepOpen      bool
	closeAll      bool
	lateBlock     []string
	needReader    bool
	left          atomic.Int32
	closeGroup    atomic.Int32
	closePost     atomic.Int32
	refused       atomic.Int32
	groupNoReader atomic.Int32
	mu            sync.Mutex
	conns         map[int][]string // each connection's lines (lower case), by Conn.ID
	*fakeserver.Server
}

// record notes a line of connection id.
func (s *setupServer) record(id int, low string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns == nil {
		s.conns = make(map[int][]string)
	}
	s.conns[id] = append(s.conns[id], low)
}

// sent tells whether connection id sent the line low.
func (s *setupServer) sent(id int, low string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.conns[id] {
		if l == low {
			return true
		}
	}
	return false
}

// takeRefusal returns the next reply of refuse and its index; false when
// they are used up.
func (s *setupServer) takeRefusal() (string, int, bool) {
	if len(s.refuse) == 0 {
		return "", 0, false
	}
	i := int(s.refused.Add(1)) - 1
	if i >= len(s.refuse) {
		return "", 0, false
	}
	return s.refuse[i], i, true
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
	reader := false
	for {
		line, err := c.ReadLine()
		if err != nil {
			return
		}
		low := strings.ToLower(line)
		s.record(c.ID(), low)
		if low == s.at {
			if r, i, ok := s.takeRefusal(); ok {
				if c.WriteLines(r) != nil {
					return
				}
				if i == 0 && s.lateBlock != nil {
					if !c.Sleep(2 * time.Second) || c.WriteLines(s.lateBlock...) != nil {
						return
					}
				}
				if s.closeAll || (strings.HasPrefix(r, "400") && !s.keepOpen) {
					return
				}
				continue
			}
		}
		if s.needReader && !reader && strings.HasPrefix(low, "group ") {
			s.groupNoReader.Add(1)
			if c.WriteLines("401 MODE-READER") != nil {
				return
			}
			continue
		}
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
			reader = true
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
// error after Retries reconnects, each with Retries more connection
// attempts of its own.
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
				// Retries+1 attempts each; ConnectBackoff is 0)
				if want := 1 + setupRetries*(setupRetries+1); n != want {
					t.Errorf("%d connections to the news server, want %d (the first, then %d reconnects of %d attempts each)", n, want, setupRetries, setupRetries+1)
				}
				if code, line := c.Cmd("DATE"); code != 111 {
					t.Fatalf("DATE after the failed GROUP: %q, want 111", line)
				}
			}
			quitSession(t, p, c)
		})
	}
}

// TestUpstreamReconnectGroupCutLine: on a new connection connect() selects
// the client's group again. The news server sends "211 9 3 11 test.alpha"
// there without its line end and closes. issue() handed that part of a
// line on (a set-up command got its reply even from a broken connection),
// and selectgroup() stored its numbers in the active database; a client's
// GROUP within GroupTimeout is answered from there, without the news
// server, so it showed 9 3 11 instead of the server's 5 1 5. Now the cut
// line is no reply: that set-up fails and nothing is stored. Retries 1, so
// the connection with the cut line is the last attempt (a later GROUP on a
// new connection would store the right numbers over it).
func TestUpstreamReconnectGroupCutLine(t *testing.T) {
	s := &setupServer{at: "group test.alpha", cut: "211 9 3 11 test.alpha"}
	e := setupEnv(t, s, "plain", false)
	e.ServerLines = "    Retries 1"
	e.GroupTimeout = 3600
	p, c := e.sessionProcess()
	c.Deadline = 30 * time.Second
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 || !strings.HasPrefix(line, "211 5 1 5 ") {
		t.Fatalf("GROUP test.alpha: %q, want 211 5 1 5", line)
	}
	// the connection breaks at GROUP test.beta; the next one is cut at
	// its GROUP test.alpha
	s.closeGroup.Store(1)
	s.left.Store(1)
	if code, line := c.Cmd("GROUP test.beta"); code < 400 {
		t.Fatalf("GROUP test.beta with the news server failing: %q, want an error", line)
	}
	if n := s.Accepted(); n != 2 {
		t.Fatalf("%d connections to the news server, want 2 (the first, the one with the cut line)", n)
	}
	accepted := s.Accepted()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 || !strings.HasPrefix(line, "211 5 1 5 ") {
		t.Errorf("GROUP test.alpha from the cache: %q, want 211 5 1 5 (the numbers of the cut line were stored)", line)
	}
	if n := s.Accepted() - accepted; n != 0 {
		t.Errorf("GROUP test.alpha within GroupTimeout made %d new connections, want none (answered from the cache)", n)
	}
	quitSession(t, p, c)
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

// TestUpstreamSetupRefusedNotTurnedOff: the news server refuses MODE READER
// or LIST OVERVIEW.FMT once with a reply that doesn't say the command is
// unsupported (403, 502; 480 without a login configured; with a login,
// MODE READER refused again after it). NewsCache turned the command off
// for the rest of the process: no later connection sent it again. Now a
// refused MODE READER gives up that connection (the client's command
// fails: a server that needs MODE READER refuses reader commands without
// it), and a refused LIST OVERVIEW.FMT leaves the standard overview format
// for that connection only. Every later connection sends the command
// again. The server closes the connection at GROUP test.beta once, so that
// a new one is made.
func TestUpstreamSetupRefusedNotTurnedOff(t *testing.T) {
	for _, tc := range []struct {
		name, mode, at string
		refuse         []string
		goesOn         bool // the first connection is used without the command
	}{
		{"mode reader 403", "plain", "mode reader", []string{"403 not now"}, false},
		{"mode reader 502", "plain", "mode reader", []string{"502 reading service unavailable"}, false},
		{"mode reader 480", "plain", "mode reader", []string{"480 authentication required"}, false},
		{"mode reader after login 403", "auth", "mode reader", []string{"480 log in first", "403 not now"}, false},
		{"list overview.fmt 403", "plain", "list overview.fmt", []string{"403 not now"}, true},
		{"list overview.fmt 480", "plain", "list overview.fmt", []string{"480 authentication required"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &setupServer{at: tc.at, refuse: tc.refuse}
			e := setupEnv(t, s, tc.mode, false)
			p, c := e.sessionProcess()
			c.Deadline = 30 * time.Second
			code, line := c.Cmd("GROUP test.alpha")
			switch {
			case tc.goesOn && code != 211:
				t.Errorf("GROUP with %q refused (%q): %q, want 211 (the standard overview format for that connection)", tc.at, tc.refuse, line)
			case !tc.goesOn && code < 400:
				t.Errorf("GROUP with %q refused (%q): %q, want an error (that connection given up)", tc.at, tc.refuse, line)
			}
			s.closeGroup.Store(1)
			if code, line := c.Cmd("GROUP test.beta"); code != 211 {
				t.Errorf("GROUP test.beta: %q, want 211 from a new connection", line)
			}
			n := s.Accepted()
			if n < 2 {
				t.Fatalf("%d connections to the news server, want a new one after the refusal", n)
			}
			for id := 2; id <= n; id++ {
				if !s.sent(id, tc.at) {
					t.Errorf("connection %d of %d sent no %q: the refusal on the first one turned it off", id, n, tc.at)
				}
			}
			quitSession(t, p, c)
		})
	}
}

// TestUpstreamSetupReply400Reconnects: the server answers MODE READER (also
// the one sent again after a login) or LIST OVERVIEW.FMT with 400, or with
// 503 ("503 timeout", old INN), and closes the connection. NewsCache took
// it as "not supported" and turned the command off for the rest of the
// process: the next connection went without it. It now counts as a closed
// connection: a new one is made (Retries), and it sends the command again.
// (For LIST OVERVIEW.FMT a 400 counts as a close only when the close is
// seen right after it; else the standard format is used and the next
// command finds the connection closed: the same two connections either
// way. A 503 counts as a close only when the close is seen right after it,
// as in issue(); on loopback it comes with the reply.)
func TestUpstreamSetupReply400Reconnects(t *testing.T) {
	for _, tc := range []struct {
		name, mode, at string
		refuse         []string
		closeAll       bool // the server closes after a 503 too
	}{
		{"mode reader 400", "plain", "mode reader", []string{"400 service unavailable"}, false},
		{"mode reader after login 400", "auth", "mode reader", []string{"480 log in first", "400 service unavailable"}, false},
		{"list overview.fmt 400", "plain", "list overview.fmt", []string{"400 service unavailable"}, false},
		{"mode reader 503 closed", "plain", "mode reader", []string{"503 timeout"}, true},
		{"list overview.fmt 503 closed", "plain", "list overview.fmt", []string{"503 timeout"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &setupServer{at: tc.at, refuse: tc.refuse, closeAll: tc.closeAll}
			e := setupEnv(t, s, tc.mode, false)
			p, c := e.sessionProcess()
			c.Deadline = 30 * time.Second
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Errorf("GROUP with %q answered %q and closed on the first connection: %q, want 211 from a new connection", tc.at, tc.refuse, line)
			}
			if n := s.Accepted(); n != 2 {
				t.Errorf("%d connections to the news server, want 2 (the closed one and one more)", n)
			}
			if !s.sent(2, tc.at) {
				t.Errorf("the new connection sent no %q: the reply before the close turned it off", tc.at)
			}
			if n := s.Count("GROUP"); n != 1 {
				t.Errorf("GROUP reached the news server %d times, want once", n)
			}
			quitSession(t, p, c)
		})
	}
}

// TestUpstreamSetupReply400Always: a news server that answers MODE READER
// with 400 and closes on every connection for a while: the client gets an
// error after the first connection and Retries more, and once the server
// answers again, the next connection sends MODE READER again. NewsCache
// turned it off at the first 400 and used the server without it. (Not for
// LIST OVERVIEW.FMT: there a 400 counts as a close only when the close is
// seen right after it, so the number of connections depends on timing.)
func TestUpstreamSetupReply400Always(t *testing.T) {
	const at = "mode reader"
	refuse := make([]string, setupRetries+1)
	for i := range refuse {
		refuse[i] = "400 service unavailable"
	}
	s := &setupServer{at: at, refuse: refuse}
	e := setupEnv(t, s, "plain", false)
	p, c := e.sessionProcess()
	c.Deadline = 30 * time.Second
	if code, line := c.Cmd("GROUP test.alpha"); code < 400 {
		t.Errorf("GROUP with a 400 to %q on every connection: %q, want an error", at, line)
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
	accepted := s.Accepted()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP once the server answers again: %q, want 211", line)
	}
	if n := s.Accepted() - accepted; n != 1 {
		t.Errorf("GROUP once the server answers again made %d new connections, want 1", n)
	}
	if !s.sent(s.Accepted(), at) {
		t.Errorf("the connection after the 400s sent no %q: they turned it off", at)
	}
	quitSession(t, p, c)
}

// TestUpstreamOverviewFmt400Open: a 400 to LIST OVERVIEW.FMT on a
// connection the server keeps open. LIST OVERVIEW.FMT is optional: the
// connection is used with the standard overview format, and the next
// connection asks for the format again. NewsCache turned it off for the
// rest of the process.
func TestUpstreamOverviewFmt400Open(t *testing.T) {
	const at = "list overview.fmt"
	s := &setupServer{at: at, refuse: []string{"400 not now"}, keepOpen: true}
	e := setupEnv(t, s, "plain", false)
	p, c := e.sessionProcess()
	c.Deadline = 30 * time.Second
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP with a 400 to %q on an open connection: %q, want 211", at, line)
	}
	if n := s.Accepted(); n != 1 {
		t.Errorf("%d connections to the news server, want 1 (the 400 isn't a close)", n)
	}
	s.closeGroup.Store(1)
	if code, line := c.Cmd("GROUP test.beta"); code != 211 {
		t.Errorf("GROUP test.beta: %q, want 211 from a new connection", line)
	}
	n := s.Accepted()
	if n < 2 {
		t.Fatalf("%d connections to the news server, want a new one", n)
	}
	for id := 2; id <= n; id++ {
		if !s.sent(id, at) {
			t.Errorf("connection %d of %d sent no %q: the 400 turned it off", id, n, at)
		}
	}
	quitSession(t, p, c)
}

// TestUpstreamSetupUnexpectedPositive: the server answers MODE READER with
// a positive reply that isn't 200 or 201 ("215 list follows") and sends
// its block two seconds later, after NewsCache has sent LIST OVERVIEW.FMT.
// NewsCache turned MODE READER off and read the block's first line
// ("500 ...") as the reply to LIST OVERVIEW.FMT, which turned that off
// too. Now the connection is closed at the unexpected reply and a new one
// is made: without MODE READER (turned off as before), with LIST
// OVERVIEW.FMT and its own reply, on that one and on later ones.
func TestUpstreamSetupUnexpectedPositive(t *testing.T) {
	s := &setupServer{at: "mode reader", refuse: []string{"215 list follows"},
		lateBlock: []string{"500 stray line of the block", "."}}
	e := setupEnv(t, s, "plain", false)
	p, c := e.sessionProcess()
	c.Deadline = 30 * time.Second
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP with a 215 and a late block to MODE READER: %q, want 211", line)
	}
	s.closeGroup.Store(1)
	if code, line := c.Cmd("GROUP test.beta"); code != 211 {
		t.Errorf("GROUP test.beta: %q, want 211 from a new connection", line)
	}
	n := s.Accepted()
	if n < 2 {
		t.Fatalf("%d connections to the news server, want a new one", n)
	}
	for id := 2; id <= n; id++ {
		if s.sent(id, "mode reader") {
			t.Errorf("connection %d of %d sent MODE READER again after the 215", id, n)
		}
		if !s.sent(id, "list overview.fmt") {
			t.Errorf("connection %d of %d sent no LIST OVERVIEW.FMT: the late block was read as its reply", id, n)
		}
	}
	quitSession(t, p, c)
}

// TestUpstreamSetupUnsupportedTurnedOff (guard, passes before the fix too):
// a reply that says MODE READER or LIST OVERVIEW.FMT is unsupported (500,
// 501, or 503 without closing the connection) still turns the command off:
// later connections don't send it.
func TestUpstreamSetupUnsupportedTurnedOff(t *testing.T) {
	for _, at := range []string{"mode reader", "list overview.fmt"} {
		for _, reply := range []string{"500 unknown command", "501 syntax error", "503 not supported"} {
			t.Run(at+" "+reply[:3], func(t *testing.T) {
				t.Parallel()
				s := &setupServer{at: at, refuse: []string{reply}}
				e := setupEnv(t, s, "plain", false)
				p, c := e.sessionProcess()
				c.Deadline = 30 * time.Second
				if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
					t.Errorf("GROUP with %q answered %q: %q, want 211", at, reply, line)
				}
				s.closeGroup.Store(1)
				if code, line := c.Cmd("GROUP test.beta"); code != 211 {
					t.Errorf("GROUP test.beta: %q, want 211 from a new connection", line)
				}
				n := s.Accepted()
				if n < 2 {
					t.Fatalf("%d connections to the news server, want a new one", n)
				}
				for id := 2; id <= n; id++ {
					if s.sent(id, at) {
						t.Errorf("connection %d of %d sent %q again after %q", id, n, at, reply)
					}
				}
				quitSession(t, p, c)
			})
		}
	}
}

// TestUpstreamModeSwitchingRefusal: a mode-switching news server (RFC 3977
// 3.4.2), which answers reader commands only after MODE READER, refuses
// MODE READER once with 403. NewsCache turned MODE READER off and sent
// GROUP without it, on that connection and on every later one: the server
// refused them all (401 MODE-READER) until NewsCache was restarted. Now
// the client's first command fails with that connection, the next one
// gets the group, and no GROUP reaches the server without MODE READER.
func TestUpstreamModeSwitchingRefusal(t *testing.T) {
	s := &setupServer{at: "mode reader", refuse: []string{"403 not now"}, needReader: true}
	e := setupEnv(t, s, "plain", false)
	p, c := e.sessionProcess()
	c.Deadline = 30 * time.Second
	if code, line := c.Cmd("GROUP test.alpha"); code < 400 {
		t.Errorf("GROUP with MODE READER refused: %q, want an error", line)
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP once MODE READER is accepted: %q, want 211", line)
	}
	if n := s.groupNoReader.Load(); n != 0 {
		t.Errorf("%d GROUP commands reached the server without MODE READER on their connection, want none", n)
	}
	quitSession(t, p, c)
}
