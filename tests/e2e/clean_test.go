package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
	"github.com/go-while/nntp-testkit/proc"
)

// TestNewscachecleanEvictionOrder keeps two groups and evicts the oldest,
// independently of directory order and the filesystem's atime policy.
func TestNewscachecleanEvictionOrder(t *testing.T) {
	t.Run("atime newer", func(t *testing.T) { newscachecleanEvictionOrder(t, true) })
	t.Run("mtime newer", func(t *testing.T) { newscachecleanEvictionOrder(t, false) })
}

func newscachecleanEvictionOrder(t *testing.T, newerAtime bool) {
	// A compressed filesystem can change st_blocks between stat and the
	// cleaner's scan, despite fsync. Prefer the user's tmpfs for this
	// allocation-sensitive fixture; no mount or elevated uid is needed.
	if dir, err := os.MkdirTemp("/dev/shm", "newscache-eviction-"); err == nil {
		t.Cleanup(func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Errorf("remove eviction fixture: %v", err)
			}
		})
		t.Setenv("TMPDIR", dir)
	}
	e := newEnv(t)
	e.Extra = "History off"
	clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to %s", e.Bin)
	}
	// Every article is large enough to have its own .art1. Assign age
	// order after measuring the files, so the KiB budget can be exact.
	groups := []string{"test.clean-a", "test.clean-b", "test.clean-c"}
	articles := make(map[string]string)
	for _, group := range groups {
		e.Store.AddGroup(group, "eviction order")
		e.Store.AddArticle(group, groupArticle(group, 1, bigBody()))
	}
	e.writeConf()
	p := proc.StartStdio(t, e.procOpts("-i"))
	c := client.New(t, p.Conn())
	if _, err := c.ReadGreeting(); err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if code, line := c.Cmd("GROUP %s", group); code != 211 {
			t.Fatalf("GROUP %s: %q", group, line)
		}
		if code, line := c.Cmd("ARTICLE 1"); code != 220 {
			t.Fatalf("ARTICLE in %s: %q", group, line)
		}
		articles[group] = strings.Join(c.ReadDotLines(), "\n")
	}
	if code, line := c.Cmd("QUIT"); code != 205 {
		t.Fatalf("QUIT: %q", line)
	}
	if err := p.Wait(); err != nil {
		t.Fatalf("cache-warming session: %v", err)
	}
	if got := e.Upstream.Count("ARTICLE"); got != len(groups) {
		t.Fatalf("warming the cache fetched %d articles, want %d", got, len(groups))
	}

	// SpoolSize is in KiB, st_blocks in 512-byte units. Rounding up can
	// spare a one-block oldest .db on compressed filesystems. Among three
	// group sizes, two must have equal parity: retain that pair so their
	// sum is even and the budget has no rounding slack at all.
	var blocks map[string]int64
	var keptBlocks int64
	newest := time.Unix(1700000000, 0)
	cold := newest.Add(-24 * time.Hour)
	for attempt := 0; ; attempt++ {
		blocks = make(map[string]int64)
		for i, group := range groups {
			// Stagger only one timestamp and keep the other equally old.
			// Each case requires max(atime, mtime), not just one field.
			stamp := newest.Add(-time.Duration(i) * time.Hour)
			atime, mtime := stamp, cold
			if !newerAtime {
				atime, mtime = mtime, atime
			}
			for _, name := range []string{".db", ".art1"} {
				path := filepath.Join(e.Spool, strings.ReplaceAll(group, ".", "/"), name)
				if err := os.Chtimes(path, atime, mtime); err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(path, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				// Sync after assigning timestamps: metadata writes can also
				// change allocated blocks on a compressed filesystem.
				err = f.Sync()
				st, statErr := f.Stat()
				closeErr := f.Close()
				if err != nil || statErr != nil || closeErr != nil {
					t.Fatalf("sync/stat/close %s: %v, %v, %v", path, err, statErr, closeErr)
				}
				stat, ok := st.Sys().(*syscall.Stat_t)
				if !ok || stat.Blocks <= 0 {
					t.Fatalf("%s has no allocated blocks: %+v", path, st.Sys())
				}
				blocks[group] += stat.Blocks
			}
		}
		keptBlocks = blocks[groups[0]] + blocks[groups[1]]
		if keptBlocks%2 == 0 {
			break
		}
		if attempt == 5 {
			t.Fatalf("cannot construct an exact KiB budget: group blocks %v", blocks)
		}
		if (blocks[groups[0]]+blocks[groups[2]])%2 == 0 {
			groups[1], groups[2] = groups[2], groups[1]
		} else {
			groups[0], groups[2] = groups[2], groups[0]
		}
	}
	kb := keptBlocks / 2
	t.Logf("groups newest to oldest: %v; SpoolSize %d KiB: keep %d blocks, evict the oldest %d blocks", groups, kb, keptBlocks, blocks[groups[2]])
	e.Extra += fmt.Sprintf("\nSpoolSize %d\n", kb)
	e.writeConf()
	out, err := exec.Command(clean, "-c", e.Conf, "-s", "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscacheclean: %v\n%s", err, out)
	}
	// Inspect the very purge that removes the files; a preliminary scan
	// could itself change atimes. Collection always puts a group's .db
	// before its .art1, whereas sorting puts .art1 first (its key is
	// capped at the database's time minus one second). Thus removing the
	// sort cannot pass even if readdir happens to yield oldest first.
	const marker = "---------------- Purge table ----------------"
	parts := strings.Split(string(out), marker)
	if len(parts) != 3 {
		t.Fatalf("missing or malformed purge table:\n%s", out)
	}
	var got, want []string
	for _, row := range strings.Split(strings.TrimSpace(parts[1]), "\n") {
		fields := strings.Fields(row)
		if len(fields) != 3 {
			t.Fatalf("malformed purge table row %q", row)
		}
		got = append(got, strings.Join(fields[:2], " "))
	}
	for i := len(groups) - 1; i >= 0; i-- {
		dir := "./" + strings.ReplaceAll(groups[i], ".", "/")
		stamp := newest.Add(-time.Duration(i) * time.Hour).Unix()
		want = append(want, fmt.Sprintf("%s/.art1 %d", dir, stamp-1), fmt.Sprintf("%s/.db %d", dir, stamp))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("purge table order/timestamps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, want := range []string{"PurgedDbCnt: 1\n", "PurgedArtCnt: 1\n"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("cleanup statistics lack %q:\n%s", want, out)
		}
	}
	for i, group := range groups {
		for _, name := range []string{".db", ".art1"} {
			path := filepath.Join(e.Spool, strings.ReplaceAll(group, ".", "/"), name)
			_, err := os.Stat(path)
			if i == len(groups)-1 {
				if !os.IsNotExist(err) {
					t.Errorf("oldest group file %s was not evicted: %v", path, err)
				}
			} else if err != nil {
				t.Errorf("newer group file %s was not preserved: %v", path, err)
			}
		}
	}

	before := e.Upstream.Count("ARTICLE")
	c = e.session()
	for _, group := range groups[:2] {
		if code, line := c.Cmd("GROUP %s", group); code != 211 {
			t.Fatalf("GROUP %s after cleanup: %q", group, line)
		}
		if code, line := c.Cmd("ARTICLE 1"); code != 220 {
			t.Fatalf("ARTICLE in %s after cleanup: %q", group, line)
		}
		if got := strings.Join(c.ReadDotLines(), "\n"); got != articles[group] {
			t.Errorf("cached article in %s changed after cleanup", group)
		}
	}
	if got := e.Upstream.Count("ARTICLE"); got != before {
		t.Errorf("newer articles were fetched again after cleanup: %d -> %d upstream ARTICLE commands", before, got)
	}
	c.Cmd("QUIT")
}

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

// TestNewscachecleanRange: an article file stored while newscacheclean
// scans the group's directory is not taken for one outside the group's
// range (removed at the end of the run whatever SpoolSize says), nor is a
// file stored again under the name of a stale one the scan found. The
// test build waits right after the cleaner has read the group's range
// (NEWSCACHE_TEST_CLEAN_RACE); a session stores articles 2 and 3 then.
func TestNewscachecleanRange(t *testing.T) {
	if os.Getenv("NEWSCACHE_CLEAN_RACE_TEST") != "1" {
		t.Skip("requires a NEWSCACHE_TEST_CLEAN_RACE build (NEWSCACHE_CLEAN_RACE_TEST=1)")
	}
	const group = "test.clean-range"
	e := newEnv(t)
	clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to %s", e.Bin)
	}
	e.Store.AddGroup(group, "test group "+group)
	e.Store.AddArticle(group, groupArticle(group, 1, []string{"small body"}))
	e.Store.AddArticle(group, groupArticle(group, 2, bigBody()))
	e.Store.AddArticle(group, groupArticle(group, 3, bigBody()))
	reader := e.session()
	if code, line := reader.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	// the index covers 1 only
	if code, line := reader.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1: %q", line)
	}
	reader.ReadDotLines()
	reader.Cmd("QUIT")
	// a file a crash left behind, outside the index: the scan finds it
	// and marks it stale
	stale := filepath.Join(e.Spool, "test", "clean-range", ".art3")
	if err := os.WriteFile(stale, []byte("left behind\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	goR, goW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	cmd := exec.Command(clean, "-c", e.Conf)
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.ExtraFiles = []*os.File{readyW, goR}
	cmd.Env = append(os.Environ(), "NEWSCACHE_TEST_CLEAN_READY_FD=3",
		"NEWSCACHE_TEST_CLEAN_GO_FD=4")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	readyW.Close()
	goR.Close()
	defer goW.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ready := make(chan error, 1)
	go func() {
		b := make([]byte, 1)
		_, err := readyR.Read(b)
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("newscacheclean did not reach its test pause: %v\n%s", err, out.String())
		}
	case err := <-done:
		t.Fatalf("newscacheclean ended before its test pause: %v\n%s", err, out.String())
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("newscacheclean did not reach its test pause")
	}

	// the cleaner has the range [1,1]; articles 2 and 3 come into the
	// spool now (3 replaces the stale file)
	writer := e.session()
	if code, line := writer.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	for _, n := range []int{2, 3} {
		if code, line := writer.Cmd("ARTICLE %d", n); code != 220 {
			t.Fatalf("ARTICLE %d: %q", n, line)
		}
		writer.ReadDotLines()
	}
	writer.Cmd("QUIT")
	if b, err := os.ReadFile(stale); err != nil || strings.Contains(string(b), "left behind") {
		t.Fatalf("article 3 not stored over the stale file: %v", err)
	}
	artFile := filepath.Join(e.Spool, "test", "clean-range", ".art2")
	if _, err := os.Stat(artFile); err != nil {
		t.Fatalf("article file before the cleaner goes on: %v", err)
	}

	if _, err := goW.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("newscacheclean: %v\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatal("newscacheclean did not finish")
	}
	if _, err := os.Stat(artFile); err != nil {
		t.Errorf("article file stored during the scan: %v (SpoolSize was not exceeded)", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("article file stored again over a stale one during the scan: %v", err)
	}
}

// TestNewscachecleanUnlinkReplaced: newscacheclean checks that a file is
// still the one it scanned, then removes it. A session that stores the
// article again in between (NVNewsgroup::setarticle renames .art<n>.tmp
// over .art<n> with the group database's exclusive lock) lost the new
// file. The test build waits between the check and the unlink
// (NEWSCACHE_TEST_CLEAN_RACE); the test stores a new file under the name
// then, as setarticle does, and it must survive.
func TestNewscachecleanUnlinkReplaced(t *testing.T) {
	if os.Getenv("NEWSCACHE_CLEAN_RACE_TEST") != "1" {
		t.Skip("requires a NEWSCACHE_TEST_CLEAN_RACE build (NEWSCACHE_CLEAN_RACE_TEST=1)")
	}
	const group = "test.clean-replaced"
	e := newEnv(t)
	clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to %s", e.Bin)
	}
	e.Store.AddGroup(group, "test group "+group)
	e.Store.AddArticle(group, groupArticle(group, 1, []string{"small body"}))
	reader := e.session()
	if code, line := reader.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if code, line := reader.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1: %q", line)
	}
	reader.ReadDotLines()
	reader.Cmd("QUIT")
	dir := filepath.Join(e.Spool, "test", "clean-replaced")
	// outside the group's range: the cleaner removes it whatever
	// SpoolSize says
	art := filepath.Join(dir, ".art7")
	if err := os.WriteFile(art, []byte("left behind\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	goR, goW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	cmd := exec.Command(clean, "-c", e.Conf)
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.ExtraFiles = []*os.File{readyW, goR}
	cmd.Env = append(os.Environ(), "NEWSCACHE_TEST_CLEAN_UNLINK_READY_FD=3",
		"NEWSCACHE_TEST_CLEAN_UNLINK_GO_FD=4")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	readyW.Close()
	goR.Close()
	defer goW.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ready := make(chan error, 1)
	go func() {
		b := make([]byte, 1)
		_, err := readyR.Read(b)
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("newscacheclean did not reach its test pause: %v\n%s", err, out.String())
		}
	case err := <-done:
		t.Fatalf("newscacheclean ended before its test pause: %v\n%s", err, out.String())
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("newscacheclean did not reach its test pause")
	}

	// store a new .art7 as setarticle does: under the database's
	// exclusive lock, written under a temporary name and renamed
	stored := make(chan error, 1)
	go func() {
		db, err := os.OpenFile(filepath.Join(dir, ".db"), os.O_RDWR, 0)
		if err != nil {
			stored <- err
			return
		}
		defer db.Close()
		lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
		for {
			err = syscall.FcntlFlock(db.Fd(), syscall.F_SETLKW, &lk)
			if err != syscall.EINTR {
				break
			}
		}
		if err != nil {
			stored <- err
			return
		}
		tmp := art + ".tmp"
		if err := os.WriteFile(tmp, []byte("stored again\r\n"), 0o644); err != nil {
			stored <- err
			return
		}
		stored <- os.Rename(tmp, art)
	}()
	// The cleaner holds the lock now and the store waits; without the
	// lock the store is done at once (and the cleaner removes it).
	select {
	case err := <-stored:
		if err != nil {
			t.Fatal(err)
		}
		stored <- nil
	case <-time.After(2 * time.Second):
	}

	if _, err := goW.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("newscacheclean: %v\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatal("newscacheclean did not finish")
	}
	select {
	case err := <-stored:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the store did not get the database's lock after newscacheclean ended")
	}
	b, err := os.ReadFile(art)
	if err != nil {
		t.Fatalf("the file stored again while newscacheclean removed the old one: %v", err)
	}
	if string(b) != "stored again\r\n" {
		t.Fatalf(".art7 holds %q, want the file stored again", b)
	}
}
