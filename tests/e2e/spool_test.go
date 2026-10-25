package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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

// TestNewscachecleanNiceClean: newscacheclean lowers its priority by
// NiceClean, as newscache.conf(5) says. It used NiceServer instead, so
// NiceClean had no effect. The test holds the lock of a group's database,
// so newscacheclean waits for it, and reads its nice value meanwhile.
func TestNewscachecleanNiceClean(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/<pid>/stat")
	}
	e := newEnv(t)
	e.addArticles("test.a", 1)
	c := e.session()
	c.Cmd("GROUP test.a")
	c.Cmd("XOVER 1-1")
	c.ReadDotLines()
	c.Cmd("QUIT")
	conf, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(conf), "NiceServer 0", "NiceServer 2", 1)
	s = strings.Replace(s, "NiceClean 0", "NiceClean 7", 1)
	if err := os.WriteFile(e.Conf, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(filepath.Join(e.Spool, "test", "a", ".db"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lk := syscall.Flock_t{Type: syscall.F_WRLCK}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLKW, &lk); err != nil {
		t.Fatal(err)
	}
	clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to %s", e.Bin)
	}
	cmd := exec.Command(clean, "-c", e.Conf)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// field 19 of /proc/<pid>/stat, after the command name in parentheses
	niceOf := func(pid int) int {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			t.Fatal(err)
		}
		f := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+1:]))
		n, err := strconv.Atoi(f[16])
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	base := niceOf(os.Getpid())
	want := base + 7
	if want > 19 {
		want = 19
	}
	got := base
	for deadline := time.Now().Add(10 * time.Second); got != want && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		got = niceOf(cmd.Process.Pid)
	}
	lk.Type = syscall.F_UNLCK
	syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lk)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("newscacheclean: %v\n%s", err, out.String())
	}
	if got != want {
		t.Errorf("newscacheclean ran at nice %d, want %d (ours %d + NiceClean 7; NiceServer is 2)", got, want, base)
	}
}

// TestGroupSpoolFails403: when the current group's database can't be
// opened (here its .db is a directory), every command that works on the
// current group answers 403, RFC 3977's reply for an internal fault. They
// answered "412 operation failed", but 412 means that no group is
// selected. GROUP itself doesn't open the database and still answers 211.
func TestGroupSpoolFails403(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 3)
	if err := os.MkdirAll(filepath.Join(e.Spool, "test", "alpha", ".db"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := e.session()
	for _, cmd := range []string{"ARTICLE 1", "HEAD 1", "BODY 1", "STAT 1", "STAT", "NEXT", "LAST",
		"XOVER 1-3", "OVER 1-3", "XHDR Subject 1-3", "XPAT Subject 1-3 *", "HDR Subject 1-3",
		"LISTGROUP", "LISTGROUP test.alpha"} {
		if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
			t.Fatalf("GROUP before %s: %q", cmd, line)
		}
		code, line := c.Cmd("%s", cmd)
		if code/10 == 22 || code == 211 && strings.HasPrefix(cmd, "LISTGROUP") {
			if code != 223 {
				c.ReadDotLines()
			}
		}
		if code != 403 {
			t.Errorf("%s with the group's database failing: %q, want 403", cmd, line)
		}
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Fatalf("DATE: %q, want 111", line)
	}
}
