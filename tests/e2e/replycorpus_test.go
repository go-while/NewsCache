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
