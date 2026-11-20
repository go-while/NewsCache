package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
)

// The connection to the news server breaks after a posted article, before
// the server's reply (240/441) arrives. NewsCache connects again and sends
// the article again. It asked the server with STAT whether it had the
// article only once, before the first copy: when that copy had arrived, the
// second one got 441 (duplicate), and the client was told that its article
// was refused (441 invalid article), although the server had it;
// updatenews moved such a spooled article to .badArticles. Now a STAT goes
// out before each resend too: 223, the server has it, and nothing is sent
// again; 430, the article is sent again; a STAT that fails says nothing
// about the article, and the post is queued in the spool (the client gets
// 240, updatenews sends it later, with a STAT first). An article without a
// Message-ID (dontgenmsgid) can't be asked for: it is sent again, so it
// arrives at least once.

const postRetryRetries = 2

// postRetryServer is an upstream that keeps the articles posted to it by
// Message-ID, answers STAT from them, and refuses a second copy with 441,
// as INN does. closePost closes the connection, the next n times, after an
// article and before its reply; the article is kept unless dropPost is set
// (it didn't arrive). When it closes there, closeStat is set to
// statAfterLost: the server then closes the connection at that many STATs
// (negative: at every one).
type postRetryServer struct {
	mu            sync.Mutex
	stored        map[string]bool
	posts         map[string]int // complete articles received, by Message-ID ("" for none)
	dropPost      bool
	statAfterLost int32
	closePost     atomic.Int32
	closeStat     atomic.Int32
	*fakeserver.Server
}

// articleID returns the Message-ID of an article read with ReadDotBlock, ""
// when it has none.
func articleID(block []string) string {
	for _, l := range block {
		if l == "" {
			break
		}
		if len(l) > len("message-id:") && strings.EqualFold(l[:len("message-id:")], "message-id:") {
			return strings.TrimSpace(l[len("message-id:"):])
		}
	}
	return ""
}

func (s *postRetryServer) serve(c *fakeserver.Conn) {
	for {
		line, err := c.ReadLine()
		if err != nil {
			return
		}
		low := strings.ToLower(line)
		switch {
		case low == "mode reader":
			err = c.WriteLines("200 reader ready")
		case low == "list overview.fmt":
			err = c.WriteLines("215 order of fields follows", "Subject:", "From:", "Date:",
				"Message-ID:", "References:", ":bytes", ":lines", ".")
		case strings.HasPrefix(low, "list active"):
			err = c.WriteLines("215 list of newsgroups follows", "test.alpha 0000000005 0000000001 y", ".")
		case low == "group test.alpha":
			err = c.WriteLines("211 5 1 5 test.alpha")
		case strings.HasPrefix(low, "group "):
			err = c.WriteLines("411 no such group")
		case strings.HasPrefix(low, "stat "):
			if takeOne(&s.closeStat) {
				return
			}
			id := strings.TrimSpace(line[len("stat "):])
			s.mu.Lock()
			has := s.stored[id]
			s.mu.Unlock()
			if has {
				err = c.WriteLines("223 0 " + id)
			} else {
				err = c.WriteLines("430 no such article")
			}
		case low == "post":
			if c.WriteLines("340 send it") != nil {
				return
			}
			block, rerr := c.ReadDotBlock()
			if rerr != nil {
				return
			}
			id := articleID(block)
			lost := takeOne(&s.closePost)
			s.mu.Lock()
			s.posts[id]++
			dup := id != "" && s.stored[id]
			if id != "" && !(lost && s.dropPost) {
				s.stored[id] = true
			}
			s.mu.Unlock()
			if lost {
				s.closeStat.Store(s.statAfterLost)
				return
			}
			if dup {
				err = c.WriteLines("441 435 duplicate article")
			} else {
				err = c.WriteLines("240 article posted")
			}
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

// postCount is how many copies of the article with Message-ID id ("" for
// none) the server received.
func (s *postRetryServer) postCount(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.posts[id]
}

// postRetryEnv points a new env at s (filled in and started here; set
// dropPost and statAfterLost before).
func postRetryEnv(t *testing.T, s *postRetryServer) *env {
	t.Helper()
	e := newEnv(t)
	e.ServerLines = fmt.Sprintf("    Retries %d", postRetryRetries)
	s.stored = make(map[string]bool)
	s.posts = make(map[string]int)
	s.Server = fakeserver.New(t, fakeserver.Options{Deadline: 2 * time.Minute, Handler: s.serve})
	e.Upstream = s.Server
	return e
}

// postRetryArticle posts an article with Message-ID id (none for "") and
// returns the reply to it.
func postRetryArticle(t *testing.T, c *client.Conn, id string) (int, string) {
	t.Helper()
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("POST: %q", line)
	}
	art := "Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\nSubject: lost reply\r\n"
	if id != "" {
		art += "Message-ID: " + id + "\r\n"
	}
	if err := c.Send(art + "\r\nhello\r\n.\r\n"); err != nil {
		t.Fatal(err)
	}
	code, line, err := c.ReadStatus()
	if err != nil {
		t.Fatalf("posting: %v", err)
	}
	return code, line
}

// TestPostLostReplyStat: the connection breaks after the article, before
// the server's reply; NewsCache connects again.
//
//   - "kept": the article arrived. On main the article went out again, the
//     server refused the second copy with 441, and the client got 441
//     (two copies at the server). Now the STAT on the new connection gets
//     223: the client gets 240, the server has one copy, nothing is queued.
//   - "dropped": the article didn't arrive. The STAT gets 430 and the
//     article is sent once more: 240, two copies received, one kept. The
//     240 and the two copies are the same on main (a guard); the second
//     STAT is new, so the STAT count fails on main (1, not 2).
//   - "stat fails": the article arrived, and the server closes every
//     connection at STAT from then on. That says nothing about the
//     article: once the STAT's retries are used up, the post is queued in
//     the spool, and the client gets 240 (as for any post whose server
//     can't be reached). On main the article went out again without a
//     STAT, and the client got 441.
func TestPostLostReplyStat(t *testing.T) {
	for _, tc := range []struct {
		name          string
		dropPost      bool
		statAfterLost int32
		copies        int // articles the server receives
		stats         int // STAT commands, 0: not checked
		spooled       int // articles queued in .artSpool
	}{
		{"kept", false, 0, 1, 2, 0},
		{"dropped", true, 0, 2, 2, 0},
		{"stat fails", false, -1, 1, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &postRetryServer{dropPost: tc.dropPost, statAfterLost: tc.statAfterLost}
			e := postRetryEnv(t, s)
			p, c := e.sessionProcess()
			c.Deadline = 30 * time.Second
			id := "<lost." + strings.ReplaceAll(tc.name, " ", "-") + "@client.test>"
			s.closePost.Store(1)
			code, line := postRetryArticle(t, c, id)
			if code != 240 {
				t.Errorf("posting with the reply to the article lost: %q, want 240", line)
			}
			if n := s.postCount(id); n != tc.copies {
				t.Errorf("the news server received the article %d times, want %d", n, tc.copies)
			}
			if n := s.Count("STAT"); tc.stats > 0 && n != tc.stats {
				t.Errorf("%d STATs to the news server, want %d (one before each copy)", n, tc.stats)
			}
			quitSession(t, p, c)
			if n := len(spooledIn(t, e, ".artSpool")); n != tc.spooled {
				t.Errorf("%d articles queued in the spool, want %d", n, tc.spooled)
			}
			if n := len(spooledIn(t, e, ".badArticles")); n != 0 {
				t.Errorf("%d articles in .badArticles, want none", n)
			}
		})
	}
}

// TestPostSpooledLostReplyStat: updatenews posts a spooled article, the
// reply to it is lost, and the article had arrived. On main the article
// went out again, the server refused the second copy with 441, and
// updatenews moved the article to .badArticles (which nothing posts). Now
// the STAT on the new connection gets 223: the article leaves the spool as
// posted, the server has one copy.
func TestPostSpooledLostReplyStat(t *testing.T) {
	s := &postRetryServer{}
	e := postRetryEnv(t, s)
	bin := updatenewsBin(t, e)
	e.ServerOptions = "offline"
	p, c := e.sessionProcess()
	c.Deadline = 30 * time.Second
	const id = "<lost.spooled@client.test>"
	if code, line := postRetryArticle(t, c, id); code != 240 {
		t.Fatalf("posting to the offline server: %q, want 240 (queued)", line)
	}
	quitSession(t, p, c)
	if n := len(spooledIn(t, e, ".artSpool")); n != 1 {
		t.Fatalf("%d articles queued in the spool, want 1", n)
	}
	e.ServerOptions = ""
	e.writeConf()
	s.closePost.Store(1)
	if out, err := exec.Command(bin, "-c", e.Conf).CombinedOutput(); err != nil {
		t.Fatalf("updatenews: %v\n%s", err, out)
	}
	if n := s.postCount(id); n != 1 {
		t.Errorf("the news server received the article %d times, want once", n)
	}
	if n := len(spooledIn(t, e, ".badArticles")); n != 0 {
		t.Errorf("%d articles in .badArticles after the lost reply, want none (the server has it)", n)
	}
	if n := len(spooledIn(t, e, ".artSpool")); n != 0 {
		t.Errorf("%d articles still queued in the spool, want none (posted)", n)
	}
}

// TestPostLostReplyNoMessageID (a guard, the same on main): with
// dontgenmsgid an article without a Message-ID goes on without one, and
// there is nothing to ask the server with STAT. When the reply to it is
// lost, it is sent again: delivery is at least once, and the server may
// have it twice. NewsCache doesn't make up a Message-ID for it.
func TestPostLostReplyNoMessageID(t *testing.T) {
	s := &postRetryServer{}
	e := postRetryEnv(t, s)
	e.ServerOptions = "dontgenmsgid"
	p, c := e.sessionProcess()
	c.Deadline = 30 * time.Second
	s.closePost.Store(1)
	if code, line := postRetryArticle(t, c, ""); code != 240 {
		t.Errorf("posting without a Message-ID with the reply lost: %q, want 240", line)
	}
	if n := s.postCount(""); n != 2 {
		t.Errorf("the news server received the article without a Message-ID %d times, want 2 (sent again)", n)
	}
	if n := s.Count("STAT"); n != 0 {
		t.Errorf("%d STATs to the news server, want none (no Message-ID to ask for)", n)
	}
	quitSession(t, p, c)
}
