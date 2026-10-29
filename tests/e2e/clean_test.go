package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// TestNewscachecleanTryModeStats: -s -t used to skip remove_elements entirely,
// leaving every statistic at zero even though the spool contained a group DB
// and cached articles. Try mode must report what it found without purging it.
func TestNewscachecleanTryModeStats(t *testing.T) {
	e := newEnv(t)
	clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to %s", e.Bin)
	}

	e.Store.AddGroup("test.stats", "test group for newscacheclean statistics")
	for i := int64(1); i <= 2; i++ {
		e.Store.AddArticle("test.stats", groupArticle("test.stats", i, bigBody()))
	}
	c := e.session()
	if code, line := c.Cmd("GROUP test.stats"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	for i := 1; i <= 2; i++ {
		if code, line := c.Cmd("ARTICLE %d", i); code != 220 {
			t.Fatalf("ARTICLE %d: %q", i, line)
		}
		c.ReadDotLines()
	}
	c.Cmd("QUIT")

	spoolFiles := func() []string {
		t.Helper()
		var files []string
		if err := filepath.Walk(e.Spool, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				rel, err := filepath.Rel(e.Spool, path)
				if err != nil {
					return err
				}
				files = append(files, rel)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := spoolFiles()

	out, err := exec.Command(clean, "-c", e.Conf, "-s", "-t").CombinedOutput()
	if err != nil {
		t.Fatalf("newscacheclean -s -t: %v\n%s", err, out)
	}
	for _, want := range []string{
		"DbCnt: 1\n", "DirCnt: 1\n", "ArtCnt: 2\n", "PurgedDbCnt: 0\n", "PurgedArtCnt: 0\n",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("newscacheclean -s -t output %q does not contain %q", out, want)
		}
	}
	if after := spoolFiles(); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("newscacheclean -t changed spool files: %q -> %q", before, after)
	}

	// Statistics for the normal purge path must still count the same spool
	// contents once; the default SpoolSize keeps this run non-destructive.
	out, err = exec.Command(clean, "-c", e.Conf, "-s").CombinedOutput()
	if err != nil {
		t.Fatalf("newscacheclean -s: %v\n%s", err, out)
	}
	for _, want := range []string{
		"DbCnt: 1\n", "DirCnt: 1\n", "ArtCnt: 2\n", "PurgedDbCnt: 0\n", "PurgedArtCnt: 0\n",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("newscacheclean -s output %q does not contain %q", out, want)
		}
	}
	if after := spoolFiles(); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("newscacheclean -s changed spool files: %q -> %q", before, after)
	}
}

// TestNewscachecleanExpiryHidesArticle: after newscacheclean evicts a cached
// group, a later reader must not serve its expired article or list it in OVER.
func TestNewscachecleanExpiryHidesArticle(t *testing.T) {
	const group = "test.expired"
	e := newEnv(t)
	ids := e.addArticles(group, 1)
	reader := e.session()
	if code, line := reader.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q, want 211", line)
	}
	if code, line := reader.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1 before expiry: %q, want 220", line)
	}
	reader.ReadDotLines()
	if code, line := reader.Cmd("QUIT"); code != 205 {
		t.Fatalf("QUIT before expiry: %q, want 205", line)
	}
	db := filepath.Join(e.Spool, "test", "expired", ".db")
	f, err := os.OpenFile(db, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open cached group database before cleanup: %v", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		t.Fatalf("sync cached group database before cleanup: %v", err)
	}
	st, err := f.Stat()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("stat/close cached group database before cleanup: %v", err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Blocks <= 0 {
		t.Fatalf("cached group database has no observable allocated blocks after sync: size=%d stat=%+v", st.Size(), st.Sys())
	}
	t.Logf("cached group database: size=%d, allocated 512-byte blocks=%d", st.Size(), sys.Blocks)

	if !e.Store.RemoveArticle(group, 1) {
		t.Fatal("failed to remove article from upstream store")
	}
	config, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	// A zero-block limit also triggers cleanup on compressed filesystems
	// where the database can occupy only one or two 512-byte blocks.
	tinySpool := strings.Replace(string(config), "SpoolSize 102400\n", "SpoolSize 0\n", 1)
	if tinySpool == string(config) {
		t.Fatal("could not set a small SpoolSize for the cleanup")
	}
	if err := os.WriteFile(e.Conf, []byte(tinySpool), 0o644); err != nil {
		t.Fatal(err)
	}
	clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to %s", e.Bin)
	}
	out, err := exec.Command(clean, "-c", e.Conf, "-s").CombinedOutput()
	if err != nil {
		t.Fatalf("newscacheclean: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PurgedDbCnt: 1\n") {
		t.Fatalf("newscacheclean did not report purging the group database:\n%s", out)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("group database after cleanup: want absent, got %v", err)
	}

	// session rewrites the config with its normal SpoolSize; the purge itself
	// has already happened, and the upstream no longer has article 1.
	c := e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP after expiry: %q, want 211", line)
	}
	if code, line := c.Cmd("OVER 1-1"); code != 423 {
		if code == 224 {
			c.ReadDotLines()
		}
		t.Errorf("OVER 1-1 after expiry: %q, want 423", line)
	}
	for _, command := range []string{"ARTICLE 1", "HEAD 1", "STAT 1"} {
		if code, line := c.Cmd("%s", command); code != 423 {
			t.Errorf("%s after expiry: %q, want 423", command, line)
		}
	}
	for _, command := range []string{"ARTICLE " + ids[0], "HEAD " + ids[0], "STAT " + ids[0]} {
		if code, line := c.Cmd("%s", command); code != 430 {
			t.Errorf("%s after expiry: %q, want 430", command, line)
		}
	}
	if code, line := c.Cmd("QUIT"); code != 205 {
		t.Errorf("QUIT: %q, want 205", line)
	}
}

// TestNewscachecleanWhileArticleIsFetched: newscacheclean can run while a
// live NewsCache session is fetching an article into a group's spool
// directory. The directory and article must still be usable after both
// processes finish.
func TestNewscachecleanWhileArticleIsFetched(t *testing.T) {
	const group = "test.clean-race"
	e := newEnv(t)
	clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to %s", e.Bin)
	}
	e.Store.AddGroup(group, "test group "+group)
	e.Store.AddArticle(group, groupArticle(group, 1, []string{"small body"}))
	e.Store.AddArticle(group, groupArticle(group, 2, bigBody()))
	reader := e.session()
	if code, line := reader.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if code, line := reader.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1: %q", line)
	}
	reader.ReadDotLines()
	reader.Cmd("QUIT")

	// The delayed upstream reply keeps the session in the middle of writing
	// an uncached article while newscacheclean scans the existing group.
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		Delay: map[string]time.Duration{"ARTICLE": 1500 * time.Millisecond},
	}})
	writer := e.session()
	if code, line := writer.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP before fetch: %q", line)
	}
	if err := writer.Send("ARTICLE 2\r\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		seen := false
		for _, line := range e.Upstream.Lines() {
			if strings.EqualFold(line, "ARTICLE 2") {
				seen = true
				break
			}
		}
		if seen {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("upstream did not receive ARTICLE 2: %q", e.Upstream.Lines())
		}
		time.Sleep(10 * time.Millisecond)
	}

	var out strings.Builder
	cmd := exec.Command(clean, "-c", e.Conf)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if code, line, err := writer.ReadStatus(); err != nil || code != 220 {
		t.Fatalf("ARTICLE 2 while cleaning: %q %v", line, err)
	}
	writer.ReadDotLines()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("newscacheclean: %v\n%s", err, out.String())
	}
	if code, line := writer.Cmd("QUIT"); code != 205 {
		t.Errorf("QUIT: %q, want 205", line)
	}

	// This article is large enough to live in its own spool file. Its
	// presence, followed by a cache hit in a fresh process, confirms the
	// directory survived the concurrent cleanup.
	artFile := filepath.Join(e.Spool, "test", "clean-race", ".art2")
	if _, err := os.Stat(artFile); err != nil {
		t.Fatalf("article file after concurrent cleanup: %v", err)
	}
	articlesBefore := e.Upstream.Count("ARTICLE")
	reader = e.session()
	if code, line := reader.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP after cleanup: %q", line)
	}
	if code, line := reader.Cmd("ARTICLE 2"); code != 220 {
		t.Fatalf("ARTICLE 2 after cleanup: %q", line)
	}
	reader.ReadDotLines()
	if got := e.Upstream.Count("ARTICLE"); got != articlesBefore {
		t.Errorf("ARTICLE 2 was fetched again after cleanup (%d -> %d upstream ARTICLE commands)", articlesBefore, got)
	}
	reader.Cmd("QUIT")
}
