package e2e

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
	"github.com/go-while/nntp-testkit/proc"
)

// corpusSteps are the client commands that make NewsCache send the
// corpus case's command to its news server ("" is the greeting).
// CAPABILITIES, DATE, HDR and the other commands NewsCache never sends
// upstream have no entry; their cases are skipped.
var corpusSteps = map[string][]string{
	"":          {"GROUP test.alpha", "ARTICLE 1"},
	"MODE":      {"GROUP test.alpha", "ARTICLE 1"},
	"AUTHINFO":  {"GROUP test.alpha", "ARTICLE 1"},
	"COMPRESS":  {"GROUP test.alpha", "ARTICLE 1"},
	"QUIT":      {"GROUP test.alpha", "ARTICLE 1"},
	"GROUP":     {"GROUP test.alpha", "ARTICLE 1"},
	"LISTGROUP": {"GROUP test.alpha", "LISTGROUP"},
	"OVER":      {"GROUP test.alpha", "OVER 1-3"},
	"ARTICLE":   {"GROUP test.alpha", "ARTICLE 1", "ARTICLE 2"},
	"LIST":      {"LIST ACTIVE", "GROUP test.alpha", "OVER 1-3"},
	"STAT":      {"POST"},
	"POST":      {"POST"},
}

// corpusSetup: commands of the connection set-up, sent again on every new
// connection.
var corpusSetup = map[string]bool{"MODE": true, "AUTHINFO": true, "COMPRESS": true}

// corpusBlock: whether code starts a multi-line reply to cmd.
func corpusBlock(cmd string, code int) bool {
	verb := strings.ToUpper(strings.Fields(cmd)[0])
	switch {
	case code == 220 && verb == "ARTICLE", code == 215 && verb == "LIST",
		code == 211 && verb == "LISTGROUP", code == 224 && verb == "OVER":
		return true
	}
	return false
}

const corpusPost = "From: tester <tester@example.net>\r\n" +
	"Newsgroups: test.alpha\r\n" +
	"Subject: corpus\r\n" +
	"Message-ID: <corpus.1@example.net>\r\n" +
	"\r\n" +
	"body\r\n"

// TestUpstreamReplyCorpus runs every case of nntp-testkit's reply corpus
// whose command NewsCache sends to its news server: the server answers
// that command (the first time on a connection) with the case's wrong or
// malformed reply. Whatever the case, NewsCache must answer each client
// command with a well-formed reply in time, get the right reply from the
// server for the next command (stray lines of the case read as that reply
// put the connection out of step: an article the server has came back as
// 430), stay in sync with the client (DATE, a local command, still gets
// its 111) and end the session cleanly on QUIT.
func TestUpstreamReplyCorpus(t *testing.T) {
	n := 0
	defer func() {
		if n == 0 {
			t.Fatal("no corpus case for a command NewsCache sends")
		}
	}()
	for _, rc := range fakeserver.ReplyCorpus() {
		steps, ok := corpusSteps[rc.Cmd]
		if !ok {
			continue
		}
		n++
		t.Run(rc.Name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.addArticles("test.alpha", 3)
			o := fakeserver.Options{Deadline: 20 * time.Second}
			// a block that never ends (NeedsTimeout) ends at the read
			// timeout, which is 300 seconds by default
			e.ServerLines = "    ReadTimeout 5"
			switch rc.Cmd {
			case "COMPRESS":
				requireZlib(t, e.Bin)
				o.StoreOpts.Compress = true
				e.ServerOptions = "compress"
			case "AUTHINFO":
				// NewsCache logs in right after connecting; the fake
				// server doesn't ask for it, so a reply the case
				// replaces doesn't leave it waiting for a login
				e.ServerLines += "\n    User tester\n    Password secret"
			}
			rc.Apply(&o)
			// Only the first reply on a connection is the case's, so the
			// follow-up below shows whether NewsCache recovers: stray
			// lines of the case that it didn't read would be taken for
			// the next reply on that connection.
			if !rc.IsGreeting() && !rc.Final {
				o.Faults.OverrideTimes = map[string]int{rc.Cmd: 1}
			}
			upstreamWith(t, e, o)
			e.writeConf()
			p := proc.StartStdio(t, e.procOpts("-i"))
			c := client.NewConn(p.Conn())
			c.Deadline = 30 * time.Second
			if _, err := c.ReadGreeting(); err != nil {
				t.Fatalf("greeting: %v", err)
			}
			var log []string
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("steps: %s", strings.Join(log, " ; "))
				}
			})
			for _, s := range steps {
				var code int
				var line string
				var err error
				if s == "POST" {
					code, line, _, err = c.TryPost([]byte(corpusPost))
				} else {
					code, line, err = c.TryCmd("%s", s)
				}
				if err != nil {
					var ne net.Error
					if errors.As(err, &ne) && ne.Timeout() {
						t.Fatalf("%s: no reply in %v (hang)", s, c.Deadline)
					}
					t.Fatalf("%s: %v (line %q)", s, err, line)
				}
				entry := fmt.Sprintf("%s -> %q", s, line)
				if corpusBlock(s, code) {
					lines, err := c.TryReadDotLines()
					if err != nil {
						t.Fatalf("%s: block: %v", s, err)
					}
					entry += fmt.Sprintf(" +%d lines %q", len(lines), lines)
				}
				log = append(log, entry)
			}
			// Then a command whose reply no step has cached, so NewsCache
			// asks the news server, which answers it normally: an
			// article, or for the ARTICLE cases the overview. A reply of
			// a set-up command (MODE, AUTHINFO, COMPRESS) comes again on
			// every new connection; there the server may stay unusable
			// (403), but a reply that is there must be the right one.
			if !rc.IsGreeting() {
				cmd, want, wantCode := "ARTICLE <test-alpha.3@upstream.test>", "body of article 3", 220
				if rc.Cmd == "ARTICLE" {
					cmd, want, wantCode = "OVER 1-3", "<test-alpha.3@upstream.test>", 224
				}
				code, line, err := c.TryCmd("%s", cmd)
				if err != nil {
					t.Fatalf("follow-up %s: %v", cmd, err)
				}
				switch {
				case code == wantCode:
					lines, err := c.TryReadDotLines()
					if err != nil {
						t.Fatalf("follow-up %s block: %v", cmd, err)
					}
					if !strings.Contains(strings.Join(lines, "\n"), want) {
						t.Fatalf("follow-up %s: wrong reply %q", cmd, lines)
					}
				case code == 403 && corpusSetup[rc.Cmd]:
				default:
					t.Fatalf("follow-up %s: %q (out of step or no recovery)", cmd, line)
				}
			}
			if code, line, err := c.TryCmd("DATE"); err != nil || code != 111 {
				t.Fatalf("DATE after the case: %q, %v (out of sync)", line, err)
			}
			if code, line, err := c.TryCmd("QUIT"); err != nil || code != 205 {
				t.Fatalf("QUIT: %q, %v", line, err)
			}
			if err := p.Wait(); err != nil {
				t.Fatalf("session exit: %v\n%s", err, p.Stderr())
			}
		})
	}
}

// TestUpstreamUnexpectedBlockLater: the news server answers a command with
// a positive reply the command doesn't expect (a 215 or 222 status with a
// block) and sends the block's lines only after a pause, once NewsCache
// has sent its next command. They aren't in the buffer then, so the check
// for leftover bytes before a command (TestUpstreamReplyCorpus) can't see
// them, and they were read as the next command's reply: an article the
// server has came back as 430, or an internal command got the stray line.
// NewsCache closes the connection on such a reply now: the next line the
// server gets opens a new connection (MODE READER).
func TestUpstreamUnexpectedBlockLater(t *testing.T) {
	block := func(status string) []string { return []string{status, "stray.line 1 1 y", "."} }
	const msgid3, body3 = "<test-alpha.3@upstream.test>", "body of article 3"
	for _, tc := range []struct {
		name     string
		cmd      string // the command whose reply is replaced and split
		line     string // OverrideLine key; "" replaces cmd's first reply
		reply    []string
		steps    []string
		follow   string
		want     string
		wantCode int
	}{
		{"group-215", "GROUP", "", block("215 list follows"),
			[]string{"GROUP test.alpha"}, "ARTICLE " + msgid3, body3, 220},
		{"article-222", "ARTICLE", "", block("222 1 <x@upstream.test> body follows"),
			[]string{"GROUP test.alpha", "ARTICLE 1"}, "OVER 1-3", msgid3, 224},
		{"over-215", "OVER", "", block("215 list follows"),
			[]string{"GROUP test.alpha", "OVER 1-3"}, "ARTICLE " + msgid3, body3, 220},
		{"listgroup-215", "LISTGROUP", "", block("215 list follows"),
			[]string{"GROUP test.alpha", "LISTGROUP"}, "ARTICLE " + msgid3, body3, 220},
		{"article-id-222", "ARTICLE", "ARTICLE <test-alpha.1@upstream.test>",
			block("222 0 <test-alpha.1@upstream.test> body follows"),
			[]string{"ARTICLE <test-alpha.1@upstream.test>"}, "ARTICLE " + msgid3, body3, 220},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.addArticles("test.alpha", 3)
			e.ServerLines = "    ReadTimeout 10"
			o := fakeserver.Options{Deadline: 20 * time.Second}
			// the status line now, the block's lines two seconds later
			o.Faults.Split = map[string]fakeserver.Split{
				tc.cmd: {At: []int{len(tc.reply[0]) + 2}, Gap: 2 * time.Second},
			}
			if tc.line != "" {
				o.Faults.OverrideLine = map[string][]string{tc.line: tc.reply}
			} else {
				o.Faults.Override = map[string][]string{tc.cmd: tc.reply}
				o.Faults.OverrideTimes = map[string]int{tc.cmd: 1}
			}
			upstreamWith(t, e, o)
			e.writeConf()
			p := proc.StartStdio(t, e.procOpts("-i"))
			c := client.NewConn(p.Conn())
			c.Deadline = 30 * time.Second
			if _, err := c.ReadGreeting(); err != nil {
				t.Fatalf("greeting: %v", err)
			}
			for _, s := range tc.steps {
				code, line, err := c.TryCmd("%s", s)
				if err != nil {
					t.Fatalf("%s: %v (line %q)", s, err, line)
				}
				if corpusBlock(s, code) {
					if _, err := c.TryReadDotLines(); err != nil {
						t.Fatalf("%s: block: %v", s, err)
					}
				}
			}
			code, line, err := c.TryCmd("%s", tc.follow)
			if err != nil {
				t.Fatalf("follow-up %s: %v", tc.follow, err)
			}
			switch {
			case code == tc.wantCode:
				lines, err := c.TryReadDotLines()
				if err != nil {
					t.Fatalf("follow-up %s block: %v", tc.follow, err)
				}
				if !strings.Contains(strings.Join(lines, "\n"), tc.want) {
					t.Fatalf("follow-up %s: wrong reply %q (out of step)", tc.follow, lines)
				}
			default:
				t.Fatalf("follow-up %s: %q (out of step)", tc.follow, line)
			}
			// Nothing more on that connection: the next line the server
			// got opens a new one. (Before, the next command went out on
			// it and read the late block's first line as its reply; the
			// rest was then in the buffer and the connection was closed,
			// so the client's follow-up could still come out right.)
			ups := e.Upstream.Lines()
			for i, l := range ups {
				u := strings.ToUpper(l)
				if tc.line != "" && strings.EqualFold(l, tc.line) ||
					tc.line == "" && (u == tc.cmd || strings.HasPrefix(u, tc.cmd+" ")) {
					if i+1 < len(ups) && !strings.EqualFold(ups[i+1], "mode reader") {
						t.Errorf("after %q the connection went on with %q; got %q", l, ups[i+1], ups)
					}
					break
				}
			}
			if code, line, err := c.TryCmd("DATE"); err != nil || code != 111 {
				t.Fatalf("DATE: %q, %v (out of sync)", line, err)
			}
			if code, line, err := c.TryCmd("QUIT"); err != nil || code != 205 {
				t.Fatalf("QUIT: %q, %v", line, err)
			}
			if err := p.Wait(); err != nil {
				t.Fatalf("session exit: %v\n%s", err, p.Stderr())
			}
		})
	}
}
