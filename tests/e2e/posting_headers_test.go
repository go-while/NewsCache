package e2e

import (
	"strings"
	"testing"
)

// RFC 5537 2 and 3.1 require preservation of article headers and body in
// transport; the remote injector, not this single-upstream proxy, performs
// injection. The proxy may add its own X-NNTP-Posting-Host in daemon mode.
func TestPostPreservesProtoArticleHeaders(t *testing.T) {
	for _, mode := range []string{"inetd", "standalone"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			e.addArticles("test.alpha", 1)
			var c interface {
				Cmd(string, ...any) (int, string)
				Send(string) error
				ReadStatus() (int, string, error)
			}
			if mode == "standalone" {
				e.standalone()
				c = e.dial()
			} else {
				c = e.session()
			}
			headers := []string{
				"Newsgroups: test.alpha",
				"From: Poster <poster@client.test>",
				"Subject: =?UTF-8?Q?proto_article?=",
				"Date: Tue, 02 Jan 2024 03:04:05 +0000",
				"Message-ID: <preserve@client.test>",
				"Path: reader.example!not-for-mail",
				"Injection-Date: Wed, 03 Jan 2024 04:05:06 +0000",
				"References: <parent@client.test>",
				"X-Test-Trace: first part",
				"\tsecond part",
			}
			if code, line := c.Cmd("POST"); code != 340 {
				t.Fatalf("POST: %q", line)
			}
			if err := c.Send(strings.Join(headers, "\r\n") + "\r\n\r\nbody\r\n..dot\r\n.\r\n"); err != nil {
				t.Fatal(err)
			}
			if code, line, err := c.ReadStatus(); err != nil || code != 240 {
				t.Fatalf("POST result: %q %v", line, err)
			}
			posted := e.Upstream.Articles()
			if len(posted) != 1 {
				t.Fatalf("upstream received %d articles", len(posted))
			}
			var actual []string
			for _, line := range posted[0] {
				if line == "" {
					break
				}
				if !strings.HasPrefix(strings.ToLower(line), "x-nntp-posting-host:") {
					actual = append(actual, line)
				}
			}
			if strings.Join(actual, "\n") != strings.Join(headers, "\n") {
				t.Errorf("forwarded headers = %q, want %q", actual, headers)
			}
			// Articles() records raw wire lines, including NNTP dot-stuffing;
			// the store's Posted() reports the decoded article body.
			if n := len(posted[0]); n < 3 || posted[0][n-3] != "" || posted[0][n-2] != "body" || posted[0][n-1] != "..dot" {
				t.Errorf("forwarded wire article = %q, want body and ..dot", posted[0])
			}
			decoded := e.Store.Posted()
			if len(decoded) != 1 || len(decoded[0].Body) != 2 || decoded[0].Body[0] != "body" || decoded[0].Body[1] != ".dot" {
				t.Errorf("decoded posted body = %v, want [body .dot]", decoded)
			}
			c.Cmd("QUIT")
		})
	}
}

// A proto-article may omit Date, Message-ID and Path, but needs From,
// Newsgroups and Subject (RFC 5537 3.4.1, RFC 5536 3.1).
func TestPostRejectsMissingEssentialHeaders(t *testing.T) {
	for _, tc := range []struct{ name, headers string }{
		{"from", "Newsgroups: test.alpha\r\nSubject: test\r\n"},
		{"subject", "Newsgroups: test.alpha\r\nFrom: poster@client.test\r\n"},
		{"newsgroups", "From: poster@client.test\r\nSubject: test\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.addArticles("test.alpha", 1)
			c := e.session()
			if code, line := c.Cmd("POST"); code != 340 {
				t.Fatalf("POST: %q", line)
			}
			if err := c.Send(tc.headers + "\r\nbody\r\n.\r\n"); err != nil {
				t.Fatal(err)
			}
			if code, line, err := c.ReadStatus(); err != nil || code != 441 {
				t.Errorf("post missing %s: %q %v, want 441", tc.name, line, err)
			}
			if posts := e.Upstream.Articles(); len(posts) != 0 {
				t.Errorf("post missing %s reached upstream: %q", tc.name, posts)
			}
			if code, line := c.Cmd("DATE"); code != 111 {
				t.Errorf("DATE after rejected post: %q", line)
			}
		})
	}
}
