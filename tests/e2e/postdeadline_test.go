package e2e

import (
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

// A client that sends the article after POST slowly, but often enough to
// restart every per-read timeout, still has ClientTimeout in all for the
// whole article. Without that limit the session lasted for ever.
func TestPostArticleDeadline(t *testing.T) {
	for _, mode := range []string{"inetd", "standalone"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			e.ClientTimeout = 3
			e.addArticles("test.alpha", 1)
			var c *client.Conn
			if mode == "standalone" {
				e.standalone()
				c = e.dial()
			} else {
				c = e.session()
			}
			if code, line := c.Cmd("POST"); code != 340 {
				t.Fatalf("POST: %q", line)
			}
			start := time.Now()
			// a byte every half second, for twice the limit and more
			for time.Since(start) < 8*time.Second {
				if err := c.Send("N"); err != nil {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			code, line, err := c.ReadStatus()
			if err != nil || code != 400 {
				t.Fatalf("slow article: %q %v, want 400", line, err)
			}
			if d := time.Since(start); d > time.Duration(e.ClientTimeout+3)*time.Second {
				t.Errorf("session ended after %v, want about ClientTimeout (%ds)", d, e.ClientTimeout)
			}
			if posts := e.Upstream.Articles(); len(posts) != 0 {
				t.Errorf("slow article reached upstream: %q", posts)
			}
		})
	}
}
