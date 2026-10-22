package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// bigBody is larger than the 15360 bytes NewsCache stores inside the group
// database, so the article goes to its own file <group dir>/.art<number>.
func bigBody() []string {
	line := strings.Repeat("x", 99)
	body := make([]string, 0, 220)
	for i := 0; i < 220; i++ {
		body = append(body, line)
	}
	return body
}

func groupArticle(group string, n int64, body []string) fakeserver.Article {
	return fakeserver.Article{
		Number:    n,
		MessageID: fmt.Sprintf("<%s.%d@upstream.test>", strings.ReplaceAll(group, ".", "-"), n),
		Headers: []string{
			"From: poster@upstream.test",
			fmt.Sprintf("Subject: article %d in %s", n, group),
			"Newsgroups: " + group,
			fmt.Sprintf("Xref: upstream.test %s:%d", group, n),
		},
		Body: body,
	}
}

// TestExpiredBigArticleFileRemoved: when the upstream server no longer has an
// article that NewsCache stored in its own .art file, refreshing the group
// must delete that file. The file name was built as ".art<index>d", so the
// file stayed in the spool for good.
func TestExpiredBigArticleFileRemoved(t *testing.T) {
	const group = "test.big"
	e := newEnv(t)
	e.Store.AddGroup(group, "big articles")
	e.Store.AddArticle(group, groupArticle(group, 1, []string{"small"}))
	e.Store.AddArticle(group, groupArticle(group, 2, bigBody()))
	e.Store.AddArticle(group, groupArticle(group, 3, []string{"small"}))

	c := e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if code, line := c.Cmd("ARTICLE 2"); code != 220 {
		t.Fatalf("ARTICLE 2: %q", line)
	}
	c.ReadDotLines()
	c.Cmd("QUIT")

	artFile := filepath.Join(e.Spool, "test", "big", ".art2")
	if _, err := os.Stat(artFile); err != nil {
		t.Fatalf("the big article was not stored in its own file: %v", err)
	}

	// A new upstream that no longer has article 2.
	st := fakeserver.NewStore()
	st.AddGroup(group, "big articles")
	st.AddArticle(group, groupArticle(group, 1, []string{"small"}))
	st.AddArticle(group, groupArticle(group, 3, []string{"small"}))
	e.Store = st
	e.Upstream = fakeserver.New(t, fakeserver.Options{Store: st})

	c = e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP after the expiry: %q", line)
	}
	if code, line := c.Cmd("XOVER 1-3"); code != 224 {
		t.Fatalf("XOVER: %q", line)
	}
	c.ReadDotLines()
	c.Cmd("QUIT")

	if _, err := os.Stat(artFile); err == nil {
		left, _ := filepath.Glob(filepath.Join(e.Spool, "test", "big", ".art*"))
		t.Errorf("%s is still in the spool after article 2 expired upstream (files: %q)", artFile, left)
	}
}
