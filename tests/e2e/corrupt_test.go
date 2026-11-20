package e2e

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
	"github.com/go-while/nntp-testkit/proc"
)

// Damaged spool databases: a group's .db and the active database
// .active64 are mapped into memory, and their offsets and sizes come from
// the file. NewsCache now checks them; a damaged file is moved to
// name.bad, a new one is started and filled from the upstream server
// again. Before, damage behind the header crashed every session that
// touched the group (SIGSEGV), a short or damaged .active64 aborted every
// session before the greeting, and newscacheclean ended with SIGABRT at a
// damaged .db and cleaned nothing.

// corruptEnv returns an env with test.alpha (20 articles and a big one,
// 21, which goes to its own .art file) and test.beta (3 articles), both
// cached by a first session.
func corruptEnv(t *testing.T) *env {
	t.Helper()
	return corruptEnvSize(t, 20)
}

// corruptEnvSize is corruptEnv with n articles in test.alpha before the
// big one (n+1).
func corruptEnvSize(t *testing.T, n int) *env {
	t.Helper()
	e := newEnv(t)
	e.addArticles("test.alpha", n)
	big := int64(n + 1)
	e.Store.AddArticle("test.alpha", groupArticle("test.alpha", big, bigBody()))
	e.addArticles("test.beta", 3)
	c := e.session()
	for _, cmd := range []string{"GROUP test.alpha", fmt.Sprintf("XOVER 1-%d", big), "ARTICLE 1",
		fmt.Sprintf("ARTICLE %d", big), "GROUP test.beta", "XOVER 1-3"} {
		code, line := c.Cmd("%s", cmd)
		switch code {
		case 211:
		case 220, 224:
			c.ReadDotLines()
		default:
			t.Fatalf("%s: %q", cmd, line)
		}
	}
	c.Cmd("QUIT")
	return e
}

// corruptSession starts a session and returns the process too, to check
// how it ended. A crashed newscache must not fail the test right away.
func corruptSession(t *testing.T, e *env) (*client.Conn, *proc.Process) {
	t.Helper()
	e.writeConf()
	p := proc.StartStdio(t, e.procOpts("-i"))
	c := client.New(t, p.Conn())
	c.Strict = false
	if _, err := c.ReadGreeting(); err != nil {
		t.Errorf("greeting: %v (stderr %q)", err, p.Stderr())
	}
	return c, p
}

// readCmds runs cmds and returns "cmd:code" for each, or an error when a
// reply can't be read (the session died or was closed, or a list wasn't
// ended).
func readCmds(c *client.Conn, cmds []string) ([]string, error) {
	var out []string
	for _, cmd := range cmds {
		code, _, err := c.TryCmd("%s", cmd)
		if err != nil {
			return out, fmt.Errorf("%s: %w", cmd, err)
		}
		multi := map[int]bool{215: true, 220: true, 221: true, 222: true, 224: true, 225: true, 231: true}
		if multi[code] || code == 211 && strings.HasPrefix(cmd, "LISTGROUP") {
			if _, err := c.TryReadDotLines(); err != nil {
				return out, fmt.Errorf("%s: list: %w", cmd, err)
			}
		}
		out = append(out, fmt.Sprintf("%s:%d", cmd, code))
	}
	return out, nil
}

// damage functions for a database file
func truncateTo(n int64) func(string) error {
	return func(p string) error { return os.Truncate(p, n) }
}

func cutHalf(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	return os.Truncate(p, st.Size()/2)
}

func overwrite(off, n int, b byte) func(string) error {
	return func(p string) error {
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteAt([]byte(strings.Repeat(string([]byte{b}), n)), int64(off))
		return err
	}
}

// behindHeader overwrites n bytes right behind the file's NVcontainer header
// (48 bytes on 64-bit hosts, 28 on 32-bit ones; see nvLayout): the free list
// entry or index block that newscache placed there.
func behindHeader(n int, b byte) func(string) error {
	return func(p string) error {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		l, err := nvLayoutOf(data)
		if err != nil {
			return fmt.Errorf("%s: %v", p, err)
		}
		return overwrite(int(l.hlen()), n, b)(p)
	}
}

// checkMovedAside: name.bad is there, and the log names the file.
func checkMovedAside(t *testing.T, e *env, path string) {
	t.Helper()
	if _, err := os.Stat(path + ".bad"); err != nil {
		t.Errorf("%s.bad: %v", path, err)
	}
	if b, err := os.ReadFile(filepath.Join(e.LogDir, "newscache.log")); err == nil {
		if !strings.Contains(string(b), "corrupt database "+path) {
			t.Errorf("no \"corrupt database %s\" in the log", path)
		}
	}
}

func TestCorruptGroupDB(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(string) error
	}{
		{"short", truncateTo(20)},
		{"cut-off", cutHalf},
		{"header", overwrite(0, 8, 0xff)},
		{"size-field", overwrite(8, 8, 0x7f)},
		{"behind-header", behindHeader(64, 0xff)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := corruptEnv(t)
			db := filepath.Join(e.Spool, "test", "alpha", ".db")
			if err := tc.damage(db); err != nil {
				t.Fatal(err)
			}
			c, p := corruptSession(t, e)
			got, err := readCmds(c, []string{"GROUP test.alpha", "XOVER 1-21", "ARTICLE 1", "ARTICLE 21",
				"GROUP test.beta", "XOVER 1-3"})
			if err != nil {
				t.Errorf("after the damage: %v (so far %q)", err, got)
			}
			want := "GROUP test.alpha:211 XOVER 1-21:224 ARTICLE 1:220 ARTICLE 21:220 GROUP test.beta:211 XOVER 1-3:224"
			if strings.Join(got, " ") != want {
				t.Errorf("replies %q, want %q", strings.Join(got, " "), want)
			}
			c.TryCmd("QUIT")
			if err := p.Wait(); err != nil {
				t.Errorf("newscache: %v, stderr %q", err, p.Stderr())
			}
			checkMovedAside(t, e, db)

			// The new database works: the overview is complete.
			c = e.session()
			c.Cmd("GROUP test.alpha")
			if code, line := c.Cmd("XOVER 1-21"); code != 224 {
				t.Fatalf("XOVER: %q", line)
			}
			if ov := c.ReadDotLines(); len(ov) != 21 {
				t.Errorf("XOVER 1-21 on the new database: %d lines, want 21", len(ov))
			}
		})
	}
}

func TestCorruptActiveDB(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(string) error
	}{
		{"short", truncateTo(20)},
		{"cut-off", cutHalf},
		{"header", overwrite(0, 8, 0xff)},
		{"table", behindHeader(64, 0xff)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := corruptEnv(t)
			active := filepath.Join(e.Spool, ".active64")
			if err := tc.damage(active); err != nil {
				t.Fatal(err)
			}
			c, p := corruptSession(t, e)
			got, err := readCmds(c, []string{"GROUP test.alpha", "ARTICLE 1", "LIST", "GROUP test.beta"})
			if err != nil {
				t.Errorf("after the damage: %v (so far %q)", err, got)
			}
			want := "GROUP test.alpha:211 ARTICLE 1:220 LIST:215 GROUP test.beta:211"
			if strings.Join(got, " ") != want {
				t.Errorf("replies %q, want %q", strings.Join(got, " "), want)
			}
			c.TryCmd("QUIT")
			if err := p.Wait(); err != nil {
				t.Errorf("newscache: %v, stderr %q", err, p.Stderr())
			}
			checkMovedAside(t, e, active)
		})
	}
}

// TestCorruptDBFuzz: random damage anywhere in a group's .db or in
// .active64 must never crash a session or leave a reply unfinished, and
// the next session must work again. A list the damage interrupts may end
// the session (closed, never a "." after part of it); then the log says
// so. The group has 201 records, more than the 64 formatted in one chunk,
// so that damage past the first chunk is found after part of the list
// went out.
func TestCorruptDBFuzz(t *testing.T) {
	if os.Getenv("NEWSCACHE_E2E_SHORT") == "1" {
		t.Skip("repeated fuzz coverage runs in full make check and race tests (NEWSCACHE_E2E_SHORT=1)")
	}
	cmds := []string{"GROUP test.alpha", "XOVER 1-201", "ARTICLE 1", "ARTICLE 2", "ARTICLE 201",
		"XHDR subject 1-201", "STAT 5", "LISTGROUP test.alpha", "GROUP test.beta", "XOVER 1-3",
		"LIST", "NEWGROUPS 20000101 000000"}
	for _, target := range []string{"db", "active"} {
		t.Run(target, func(t *testing.T) {
			e := corruptEnvSize(t, 200)
			path := filepath.Join(e.Spool, "test", "alpha", ".db")
			if target == "active" {
				path = filepath.Join(e.Spool, ".active64")
			}
			clean, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			used := len(clean)
			for used > 0 && clean[used-1] == 0 {
				used--
			}
			r := rand.New(rand.NewSource(1))
			closed := 0
			for k := 0; k < 150; k++ {
				b := append([]byte(nil), clean...)
				off := r.Intn(used)
				n := 1 + r.Intn(256)
				for i := 0; i < n && off+i < len(b); i++ {
					switch k % 3 {
					case 0:
						b[off+i] = byte(r.Intn(256))
					case 1:
						b[off+i] = 0xff
					case 2:
						b[off+i] = 0
					}
				}
				if err := os.WriteFile(path, b, 0o644); err != nil {
					t.Fatal(err)
				}
				for _, round := range []string{"damaged", "next"} {
					c, p := corruptSession(t, e)
					got, err := readCmds(c, cmds)
					c.TryCmd("QUIT")
					werr := p.Wait()
					if err != nil && round == "damaged" && werr == nil &&
						(errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
						closed++
						err = nil
					}
					if err != nil || werr != nil {
						t.Fatalf("damage %d (offset %d, %d bytes), %s session: %v, exit %v, stderr %q, replies %q",
							k, off, n, round, err, werr, p.Stderr(), got)
					}
				}
			}
			// With this seed, damage to the group's .db past the first
			// chunk of 64 records interrupts lists after part of them
			// went out: none closed means such a list was ended with a
			// "." as if it were complete. (Damage in the first chunk is
			// found before anything is sent, and the command is retried
			// on the repaired database.)
			if target == "db" && closed == 0 {
				t.Errorf("no session was closed in the middle of a list: interrupted lists are ended as if complete")
			}
			if closed > 0 {
				t.Logf("%s: %d damaged sessions were closed in the middle of a list", target, closed)
				if mode, err := logMode(e.Bin); err == nil && mode == "file" {
					if log := e.logFile(); !strings.Contains(log, "after part of") {
						t.Errorf("%d sessions closed mid-list, but the log doesn't say why", closed)
					}
				}
			}
		})
	}
}

// TestNewscachecleanCorruptDB: newscacheclean ended with SIGABRT at a
// damaged .db (an Error rethrown by NC_CATCH_ALL), before it had removed
// anything. Now it goes on with the other groups and exits 0; the moved
// aside .db.bad goes too.
func TestNewscachecleanCorruptDB(t *testing.T) {
	e := newEnv(t)
	groups := []string{"test.a", "test.b", "test.c", "test.d", "test.e"}
	for _, g := range groups {
		e.addArticles(g, 3)
	}
	c := e.session()
	for _, g := range groups {
		c.Cmd("GROUP %s", g)
		c.Cmd("XOVER 1-3")
		c.ReadDotLines()
	}
	c.Cmd("QUIT")
	// an article file no database refers to, in every group
	for _, g := range groups {
		dir := filepath.Join(e.Spool, strings.ReplaceAll(g, ".", string(filepath.Separator)))
		if err := os.WriteFile(filepath.Join(dir, ".art999"), []byte("orphan"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(e.Spool, "test", "c", ".db")
	if err := overwrite(0, 8, 0xff)(db); err != nil {
		t.Fatal(err)
	}

	clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to %s", e.Bin)
	}
	if out, err := exec.Command(clean, "-c", e.Conf).CombinedOutput(); err != nil {
		t.Fatalf("newscacheclean: %v\n%s", err, out)
	}
	for _, g := range groups {
		art := filepath.Join(e.Spool, strings.ReplaceAll(g, ".", string(filepath.Separator)), ".art999")
		if _, err := os.Stat(art); err == nil {
			t.Errorf("%s is still there", art)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(e.Spool, "test", "*", "*.bad")); len(left) != 0 {
		t.Errorf("left in the spool: %q", left)
	}
}

// TestNewscachecleanSymlinks: newscacheclean doesn't follow symlinks in the
// spool. Through a link to a directory outside the spool it deleted the
// *.bad and .art files there and replaced a .db it took for damaged; a link
// loop ("loop -> .") made it walk and count the spool hundreds of times.
func TestNewscachecleanSymlinks(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.a", 3)
	c := e.session()
	c.Cmd("GROUP test.a")
	c.Cmd("XOVER 1-3")
	c.ReadDotLines()
	c.Cmd("QUIT")
	grp := filepath.Join(e.Spool, "test", "a")
	// an article file the database doesn't refer to: still removed
	orphan := filepath.Join(grp, ".art999")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(e.Dir, "outside")
	files := map[string]string{
		"notes.bad":     "a file of the admin",
		"grp/.db":       "not a database",
		"grp/.art7":     "an article file",
		"grp/.art1":     "another one",
		"single.art5":   "linked as a file",
		"single.bad":    "linked as a file",
		"history/0":     "linked as a history shard",
		"history/1.bad": "linked as a history shard",
	}
	for name, data := range files {
		p := filepath.Join(outside, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		filepath.Join(e.Spool, "link"):      outside,
		filepath.Join(e.Spool, "loop"):      ".",
		filepath.Join(grp, ".art5"):         filepath.Join(outside, "single.art5"),
		filepath.Join(grp, "x.bad"):         filepath.Join(outside, "single.bad"),
		filepath.Join(e.Spool, "test", "b"): filepath.Join(outside, "grp"),
		filepath.Join(e.Spool, ".history"):  filepath.Join(outside, "history"),
	}
	for l, target := range links {
		if err := os.Symlink(target, l); err != nil {
			t.Fatal(err)
		}
	}
	// SpoolSize 1 KB: everything newscacheclean counts is over the limit
	// and goes
	conf, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.Conf, []byte(strings.Replace(string(conf), "SpoolSize 102400", "SpoolSize 1", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
	if _, err := os.Stat(clean); err != nil {
		t.Skipf("newscacheclean not found next to %s", e.Bin)
	}
	out, err := exec.Command(clean, "-c", e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscacheclean: %v\n%s", err, out)
	}
	// outside the spool, every file is still there, unchanged, and no
	// other file was added (a damaged .db used to become .db.bad there)
	got := map[string]string{}
	filepath.WalkDir(outside, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(outside, p)
			got[filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
	for name, data := range files {
		if g, ok := got[name]; !ok || g != data {
			t.Errorf("outside the spool, %s: %q (there: %v); want %q", name, g, ok, data)
		}
	}
	for name := range got {
		if _, ok := files[name]; !ok {
			t.Errorf("outside the spool, new file %s", name)
		}
	}
	for l := range links {
		if _, err := os.Lstat(l); err != nil {
			t.Errorf("symlink %s: %v", l, err)
		}
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Errorf("%s is still there", orphan)
	}
	// the purge table lists each file once, none through a link
	var table []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "./") {
			table = append(table, strings.Fields(l)[0])
		}
	}
	seen := map[string]bool{}
	for _, p := range table {
		if strings.Contains(p, "/loop") || strings.Contains(p, "/link") ||
			strings.HasPrefix(p, "./test/b/") || strings.HasPrefix(p, "./.history/") ||
			p == "./test/a/.art5" || p == "./test/a/x.bad" || seen[p] {
			t.Errorf("purge table entry %s", p)
		}
		seen[p] = true
	}
	if !seen["./test/a/.db"] || !seen["./test/a/.art999"] {
		t.Errorf("purge table lacks the group's files:\n%s", strings.Join(table, "\n"))
	}
}

// TestUpdatenewsCorruptDB: updatenews prefetches a group whose .db is
// damaged: it is moved aside and the group fetched into a new one.
func TestUpdatenewsCorruptDB(t *testing.T) {
	e := corruptEnv(t)
	prefetch := filepath.Join(e.Dir, "prefetch")
	if err := os.WriteFile(prefetch, []byte("test.alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "PrefetchFile " + prefetch
	e.writeConf()
	db := filepath.Join(e.Spool, "test", "alpha", ".db")
	if err := behindHeader(64, 0xff)(db); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf, "-o").CombinedOutput(); err != nil {
		t.Fatalf("updatenews -o: %v\n%s", err, out)
	}
	if _, err := os.Stat(db + ".bad"); err != nil {
		t.Errorf("%s.bad: %v", db, err)
	}
	// the new database holds the overview again
	e.Upstream.Close()
	c := e.session()
	c.Cmd("GROUP test.alpha")
	if code, line := c.Cmd("XOVER 1-21"); code != 224 {
		t.Fatalf("XOVER with the upstream down: %q", line)
	}
	if ov := c.ReadDotLines(); len(ov) != 21 {
		t.Errorf("XOVER 1-21 from the prefetched database: %d lines, want 21", len(ov))
	}
}

// TestCorruptDBDuringFetch: the spool finds a damaged group database
// while it stores an overview reply (a bad index entry of an article
// inside the fetched range). The CorruptDBError left the rest of the
// reply unread on the upstream connection, and the command, run once
// more on the new database, read those lines as its replies: LISTGROUP
// and OVER were taken as unsupported and the session's XOVER failed.
func TestCorruptDBDuringFetch(t *testing.T) {
	// This targeted corruption walks native unsigned-long NVArray records,
	// not a portable wire format. The Go test and C++ binary use the same
	// native Unix ABI; the other corruption cases do not decode an index.
	wordBytes := strconv.IntSize / 8
	e := newEnv(t)
	e.Store.AddGroup("test.alpha", "test group")
	for n := int64(1); n <= 10; n++ {
		if n != 2 && n != 4 {
			e.Store.AddArticle("test.alpha", groupArticle("test.alpha", n, []string{"body"}))
		}
	}
	c := e.session()
	c.Cmd("GROUP test.alpha")
	if code, line := c.Cmd("XOVER 1-10"); code != 224 {
		t.Fatalf("XOVER 1-10: %q", line)
	}
	c.ReadDotLines()
	c.Cmd("QUIT")

	// 2 and 4 show up: the next sync fetches OVER 2-4, with 3 inside,
	// whose index entry is damaged now
	e.Store.AddArticle("test.alpha", groupArticle("test.alpha", 2, []string{"body"}))
	e.Store.AddArticle("test.alpha", groupArticle("test.alpha", 4, []string{"body"}))
	db := filepath.Join(e.Spool, "test", "alpha", ".db")
	b, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	// The index is native-endian: first/last, then one offset per number.
	word := func(off int) uint64 {
		if wordBytes == 4 {
			return uint64(binary.NativeEndian.Uint32(b[off : off+4]))
		}
		return binary.NativeEndian.Uint64(b[off : off+8])
	}
	idx := -1
	for off := 0; off+wordBytes*(2+10) <= len(b); off += wordBytes {
		if f := word(off); f >= 1 && f <= 3 && word(off+wordBytes) == 10 && word(off+wordBytes*(2+int(3-f))) != 0 {
			idx = off
			break
		}
	}
	if idx < 0 {
		t.Fatal("index not found in the .db")
	}
	entry := idx + wordBytes*(2+int(3-word(idx)))
	for i := 0; i < wordBytes; i++ {
		b[entry+i] = 0xff
	}
	if err := os.WriteFile(db, b, 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // GroupTimeout 0: sync again

	c, p := corruptSession(t, e)
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("after the damage: GROUP: %q", line)
	}
	// the XOVER that meets the damage is run again on the new database
	// and lists every record (the CorruptDBError used to be swallowed by
	// the refresh, and the chunk printed from what the database held)
	wantNumbers(t, "after the damage: XOVER 1-10", xover(t, c, 1, 10), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	got, err := readCmds(c, []string{"ARTICLE 4", "ARTICLE 5"})
	if err != nil {
		t.Errorf("after the damage: %v (so far %q)", err, got)
	}
	want := "ARTICLE 4:220 ARTICLE 5:220"
	if strings.Join(got, " ") != want {
		t.Errorf("replies %q, want %q", strings.Join(got, " "), want)
	}
	c.TryCmd("QUIT")
	if err := p.Wait(); err != nil {
		t.Errorf("newscache: %v, stderr %q", err, p.Stderr())
	}
	checkMovedAside(t, e, db)
}

// TestBadStoredArticle: an article of 15 KB or less is kept in the group
// database as it came from the upstream. One that the read side can't use
// (no Message-ID; a bare CR in a header that the overview walks over) was
// taken for a damaged database on every later read: the whole group
// database went to .db.bad, with every cached record of the group. Now an
// article without a Message-ID isn't stored, and a stored one that doesn't
// parse is dropped on its own and fetched again.
func TestBadStoredArticle(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts fakeserver.Options
		art1 func(e *env)
	}{
		{"no-message-id", fakeserver.Options{Faults: fakeserver.Faults{
			OverrideLine: map[string][]string{"ARTICLE 1": {
				"220 1 <nomid@x> article", "From: a@b", "Subject: no id",
				"Newsgroups: test.alpha", "", "body", "."}},
		}}, func(e *env) {
			e.Store.AddArticle("test.alpha", groupArticle("test.alpha", 1, []string{"body"}))
		}},
		{"bare-cr", fakeserver.Options{}, func(e *env) {
			id := "<bare-cr.1@upstream.test>"
			e.Store.AddArticle("test.alpha", fakeserver.Article{Number: 1, MessageID: id,
				Headers: []string{"From: poster@upstream.test", "Message-ID: " + id,
					"Newsgroups: test.alpha", "Xref: upstream.test test.alpha:1",
					"X-Junk: a\rb", "Subject: s"},
				Body: []string{"body"}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.Store.AddGroup("test.alpha", "x")
			tc.art1(e)
			for n := int64(2); n <= 3; n++ {
				e.Store.AddArticle("test.alpha", groupArticle("test.alpha", n, []string{"body"}))
			}
			upstreamWith(t, e, tc.opts)
			for s := 1; s <= 2; s++ {
				c := e.session()
				c.Strict = false
				if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
					t.Fatalf("session %d: GROUP: %q", s, line)
				}
				if code, line := c.Cmd("XOVER 1-3"); code == 224 {
					if l := c.ReadDotLines(); s == 2 && (!containsPrefix(l, "2\t") ||
						!containsPrefix(l, "3\t")) {
						t.Errorf("session 2: XOVER 1-3 lacks the records of 2 and 3: %q", l)
					}
				} else {
					t.Errorf("session %d: XOVER 1-3: %q", s, line)
				}
				for n := 1; n <= 3; n++ {
					code, line := c.Cmd("ARTICLE %d", n)
					if code == 220 {
						c.ReadDotLines()
					} else if n > 1 {
						t.Errorf("session %d: ARTICLE %d: %q", s, n, line)
					}
				}
				c.Cmd("QUIT")
			}
			if m, _ := filepath.Glob(filepath.Join(e.Spool, "test", "alpha", "*.bad")); len(m) > 0 {
				t.Errorf("the group database was moved aside: %v", m)
			}
		})
	}
}

// TestCorruptDBMidListCloses: the chunked printing formats 64 records under
// the lock and writes them with it released, so a damaged record past the
// first chunk is found after part of the list reached the client. The session
// must then be closed without a ".", which would make the truncated list look
// complete, and the log must say why.
func TestCorruptDBMidListCloses(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 200)
	// fill the group's database with all 200 overview records
	c := e.session()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if code, line := c.Cmd("XOVER 1-200"); code != 224 {
		t.Fatalf("XOVER: %q", line)
	}
	c.ReadDotLines()
	c.Cmd("QUIT")

	path := filepath.Join(e.Spool, "test", "alpha", ".db")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// break the record of article 100: it is read in the second chunk
	// (articles 65..128), after the first chunk went to the client
	breakRecord(t, path, b, 100)

	d, p := corruptSession(t, e)
	if code, line := d.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP (damaged): %q", line)
	}
	if code, line, err := d.TryCmd("XOVER 1-200"); err != nil {
		t.Fatalf("XOVER status: %v", err)
	} else if code != 224 {
		t.Fatalf("XOVER (damaged): %q", line)
	}
	lines := 0
	for {
		l, err := d.ReadLine()
		if err != nil {
			break
		}
		if l == "." {
			t.Fatalf("the interrupted list ended with a \".\" after %d lines, as if it were complete", lines)
		}
		lines++
	}
	t.Logf("the interrupted list sent %d of 200 lines, then the session closed", lines)
	if lines == 0 || lines >= 200 {
		t.Errorf("the damaged list sent %d lines, want a partial list of fewer than 200", lines)
	}
	if err := p.Wait(); err != nil {
		t.Errorf("session after the interrupted list: %v, want a clean exit", err)
	}
	if mode, err := logMode(e.Bin); err == nil && mode == "file" {
		if log := e.logFile(); !strings.Contains(log, "after part of") {
			t.Errorf("the session was closed mid-list but the log does not say why")
		}
	}
}

// nvLayout is how the newscache that wrote a database stores NVcontainer's
// unsigned longs (nvoff_t, nvtime_t, NVArray's index and size prefixes): their
// width and byte order. The file is mapped into memory, so both are those of
// the platform newscache was built for (i386: 4 bytes, little-endian; s390x:
// 8 bytes, big-endian), which need not be this test binary's.
type nvLayout struct {
	order binary.ByteOrder
	word  uint64
}

func (l nvLayout) String() string {
	return fmt.Sprintf("%d-byte words, %s", l.word, l.order)
}

// at reads the unsigned long at off.
func (l nvLayout) at(b []byte, off uint64) uint64 {
	if l.word == 4 {
		return uint64(l.order.Uint32(b[off:]))
	}
	return l.order.Uint64(b[off:])
}

// put writes v as the unsigned long at off.
func (l nvLayout) put(b []byte, off, v uint64) {
	if l.word == 4 {
		l.order.PutUint32(b[off:], uint32(v))
	} else {
		l.order.PutUint64(b[off:], v)
	}
}

// The header's size and where its fields are: hlen and version (4 bytes
// each), then size, mtime, freelist, bytes_free and userdata (one unsigned
// long each).
func (l nvLayout) hlen() uint64 { return 8 + 5*l.word }

func (l nvLayout) mtimeOff() uint64 { return 8 + l.word }

func (l nvLayout) userdataOff() uint64 { return 8 + 4*l.word }

// nvLayoutOf tells the layout from NVcontainer's header: hlen (4 bytes) is
// sizeof(Header), two 4-byte fields and five unsigned longs, so 28 or 48;
// version (4 bytes) is 2. In the other byte order neither value reads as
// itself, so at most one layout matches. A header that matches none is an
// error, not a skip: the corruption must not quietly stop running.
func nvLayoutOf(b []byte) (nvLayout, error) {
	if len(b) < 8 {
		return nvLayout{}, fmt.Errorf("only %d bytes, no NVcontainer header", len(b))
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, word := range []uint64{4, 8} {
			l := nvLayout{order, word}
			hlen := l.hlen()
			if uint64(order.Uint32(b[0:4])) != hlen || order.Uint32(b[4:8]) != 2 {
				continue
			}
			if uint64(len(b)) < hlen {
				return l, fmt.Errorf("%d bytes, shorter than its %d-byte header (%s)", len(b), hlen, l)
			}
			return l, nil
		}
	}
	return nvLayout{}, fmt.Errorf("unknown NVcontainer header (first bytes % x): want hlen 28 or 48 "+
		"and version 2, 4-byte fields in little- or big-endian order; has the database format changed?", b[:8])
}

// breakRecordBytes sets the size prefix of article artnum's record in the
// group database b to all ones and returns the layout it found. The layout is
// NVcontainer's: a Header whose last field is userdata, the offset of the
// index block (NVArray); the block holds the first article number, the last
// one, then one file offset per article. A record starts with its size. All of
// these are unsigned longs in newscache's layout (see nvLayout). b is left
// alone on an error.
func breakRecordBytes(b []byte, artnum uint64) (nvLayout, error) {
	l, err := nvLayoutOf(b)
	if err != nil {
		return l, err
	}
	w := l.word
	userdata := l.at(b, l.userdataOff())
	if userdata < l.hlen() || userdata > uint64(len(b))-2*w {
		return l, fmt.Errorf("index block at %d outside the file (%s)", userdata, l)
	}
	fst := l.at(b, userdata)
	lst := l.at(b, userdata+w)
	if artnum < fst || artnum > lst {
		return l, fmt.Errorf("articles %d..%d, no %d (%s)", fst, lst, artnum, l)
	}
	slot := userdata + 2*w + (artnum-fst)*w
	if slot+w > uint64(len(b)) {
		return l, fmt.Errorf("index entry of %d at %d outside the file (%s)", artnum, slot, l)
	}
	off := l.at(b, slot)
	if off == 0 || off > uint64(len(b))-w {
		return l, fmt.Errorf("record %d at %d outside the file (%s)", artnum, off, l)
	}
	for i := uint64(0); i < w; i++ {
		b[off+i] = 0xff
	}
	return l, nil
}

// breakRecord damages the length prefix of one article's record in a group
// database: the size no longer fits the record's block, so sget calls
// corrupt() ("bad index entry") wherever that record is read.
func breakRecord(t *testing.T, path string, b []byte, artnum uint64) {
	t.Helper()
	l, err := breakRecordBytes(b, artnum)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	t.Logf("%s: record %d broken, NVcontainer layout: %s", path, artnum, l)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// nvTestDB builds a group database as newscache with layout l writes it, with
// the header size hlen and the userdata field at udoff given by the caller
// (not derived from l): the header, the index block for articles fst.. and
// one record per entry of recs, each block after its size as nvalloc stores
// it. It returns the file and each record's offset.
func nvTestDB(l nvLayout, hlen, udoff, fst uint64, recs [][]byte) ([]byte, []uint64) {
	w := l.word
	put := l.put
	n := uint64(len(recs))
	userdata := hlen + w
	end := userdata + (2+n)*w
	offs := make([]uint64, n)
	for i, r := range recs {
		offs[i] = end + w
		end = (offs[i] + w + uint64(len(r)) + w - 1) / w * w
	}
	b := make([]byte, end)
	l.order.PutUint32(b[0:], uint32(hlen))
	l.order.PutUint32(b[4:], 2)
	put(b, 8, end)
	put(b, udoff, userdata)
	put(b, userdata-w, (2+n)*w+w)
	put(b, userdata, fst)
	put(b, userdata+w, fst+n-1)
	for i, r := range recs {
		put(b, userdata+(2+uint64(i))*w, offs[i])
		put(b, offs[i]-w, (2*w+uint64(len(r))+w-1)/w*w)
		put(b, offs[i], uint64(len(r)))
		copy(b[offs[i]+w:], r)
	}
	return b, offs
}

// TestBreakRecordLayouts: breakRecord finds the record in each layout
// newscache writes and sets exactly that record's size prefix to all ones.
// Before, it read the header as 8-byte little-endian words only and skipped
// on any other: on i386 (hlen 28) and s390x (hlen 48 big-endian, which reads
// as 805306368) TestCorruptDBMidListCloses never ran. A header it doesn't
// know fails instead of skipping.
func TestBreakRecordLayouts(t *testing.T) {
	recs := make([][]byte, 5)
	for i := range recs {
		recs[i] = []byte(fmt.Sprintf("\x02%d\tSubject %d\tfrom\tdate\t<%d@test>\t\t%d\t1\x00", 10+i, i, i, 100+i))
	}
	for _, tc := range []struct {
		l           nvLayout
		hlen, udoff uint64
	}{
		{nvLayout{binary.LittleEndian, 8}, 48, 40}, // amd64, arm64
		{nvLayout{binary.LittleEndian, 4}, 28, 24}, // i386, armhf
		{nvLayout{binary.BigEndian, 8}, 48, 40},    // s390x, ppc64
		{nvLayout{binary.BigEndian, 4}, 28, 24},    // powerpc
	} {
		t.Run(tc.l.String(), func(t *testing.T) {
			b, offs := nvTestDB(tc.l, tc.hlen, tc.udoff, 10, recs)
			want := append([]byte(nil), b...)
			// article 12: the third record
			for i := uint64(0); i < tc.l.word; i++ {
				want[offs[2]+i] = 0xff
			}
			l, err := breakRecordBytes(b, 12)
			if err != nil {
				t.Fatal(err)
			}
			if l != tc.l {
				t.Errorf("layout %s, want %s", l, tc.l)
			}
			if !bytes.Equal(b, want) {
				for i := range b {
					if b[i] != want[i] {
						t.Fatalf("byte %d is %#x, want %#x (record 12's size prefix at %d)", i, b[i], want[i], offs[2])
					}
				}
			}

			c := append([]byte(nil), want...)
			if _, err := breakRecordBytes(c, 15); err == nil {
				t.Errorf("article 15 (the group has 10..14): no error")
			} else if !bytes.Equal(c, want) {
				t.Errorf("article 15: the file was changed despite the error %v", err)
			}
		})
	}

	le48 := func(hlen, version uint32) []byte {
		b := make([]byte, 256)
		binary.LittleEndian.PutUint32(b[0:], hlen)
		binary.LittleEndian.PutUint32(b[4:], version)
		return b
	}
	mixed := make([]byte, 256)
	binary.LittleEndian.PutUint32(mixed[0:], 48)
	binary.BigEndian.PutUint32(mixed[4:], 2)
	for _, tc := range []struct {
		name string
		b    []byte
	}{
		{"changed header size", le48(56, 2)},
		{"version 3", le48(48, 3)},
		{"mixed byte order", mixed},
		{"all ones", bytes.Repeat([]byte{0xff}, 256)},
		{"shorter than the fields", []byte{48, 0, 0, 0, 2, 0}},
		{"shorter than the header", le48(48, 2)[:40]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orig := append([]byte(nil), tc.b...)
			if l, err := breakRecordBytes(tc.b, 1); err == nil {
				t.Errorf("no error, layout %s", l)
			} else {
				t.Logf("error as expected: %v", err)
			}
			if !bytes.Equal(tc.b, orig) {
				t.Errorf("the bytes were changed")
			}
		})
	}
}

// TestNVHeaderFields: the places the other corruption tests change, in each
// layout: TestActiveStampFromFuture's mtime and behindHeader's damage, which
// on 32-bit hosts starts at 28. Before, both assumed 8-byte little-endian
// words: the stamp test skipped elsewhere, and overwrite(48, ...) hit the
// file 20 bytes past the header on i386. An unknown header is an error that
// leaves the file alone.
func TestNVHeaderFields(t *testing.T) {
	recs := make([][]byte, 5)
	for i := range recs {
		recs[i] = []byte(fmt.Sprintf("\x02%d\tSubject %d\tfrom\tdate\t<%d@test>\t\t%d\t1\x00", 1+i, i, i, 100+i))
	}
	for _, tc := range []struct {
		l           nvLayout
		hlen, mtime uint64
		stamp       []byte // 0x01020304 as the layout stores it
	}{
		{nvLayout{binary.LittleEndian, 8}, 48, 16, []byte{4, 3, 2, 1, 0, 0, 0, 0}},
		{nvLayout{binary.LittleEndian, 4}, 28, 12, []byte{4, 3, 2, 1}},
		{nvLayout{binary.BigEndian, 8}, 48, 16, []byte{0, 0, 0, 0, 1, 2, 3, 4}},
		{nvLayout{binary.BigEndian, 4}, 28, 12, []byte{1, 2, 3, 4}},
	} {
		t.Run(tc.l.String(), func(t *testing.T) {
			// userdata is the header's last field
			orig, _ := nvTestDB(tc.l, tc.hlen, tc.hlen-tc.l.word, 1, recs)
			b := append([]byte(nil), orig...)
			l, err := nvLayoutOf(b)
			if err != nil {
				t.Fatal(err)
			}
			if l != tc.l || l.hlen() != tc.hlen || l.mtimeOff() != tc.mtime {
				t.Fatalf("layout %s, hlen %d, mtime at %d; want %s, %d, %d",
					l, l.hlen(), l.mtimeOff(), tc.l, tc.hlen, tc.mtime)
			}
			l.put(b, l.mtimeOff(), 0x01020304)
			want := append([]byte(nil), orig...)
			copy(want[tc.mtime:], tc.stamp)
			if !bytes.Equal(b, want) {
				t.Errorf("stamp written as % x, want % x at %d", b[:tc.hlen], want[:tc.hlen], tc.mtime)
			}
			if got := l.at(b, l.mtimeOff()); got != 0x01020304 {
				t.Errorf("stamp read back as %#x", got)
			}

			path := filepath.Join(t.TempDir(), ".db")
			if err := os.WriteFile(path, orig, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := behindHeader(64, 0xff)(path); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want = append([]byte(nil), orig...)
			for i := tc.hlen; i < tc.hlen+64; i++ {
				want[i] = 0xff
			}
			if !bytes.Equal(got, want) {
				t.Errorf("behindHeader: header % x, then % x; want % x, then % x",
					got[:tc.hlen], got[tc.hlen:tc.hlen+64], want[:tc.hlen], want[tc.hlen:tc.hlen+64])
			}
		})
	}

	path := filepath.Join(t.TempDir(), ".db")
	junk := bytes.Repeat([]byte{0xff}, 256) // no layout matches
	if err := os.WriteFile(path, junk, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := behindHeader(64, 0)(path); err == nil {
		t.Errorf("behindHeader on an unknown header: no error")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, junk) {
		t.Errorf("behindHeader changed a file with an unknown header (%v)", err)
	}
}
