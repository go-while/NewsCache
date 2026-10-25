package e2e

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
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
	e := newEnv(t)
	e.addArticles("test.alpha", 20)
	e.Store.AddArticle("test.alpha", groupArticle("test.alpha", 21, bigBody()))
	e.addArticles("test.beta", 3)
	c := e.session()
	for _, cmd := range []string{"GROUP test.alpha", "XOVER 1-21", "ARTICLE 1", "ARTICLE 21",
		"GROUP test.beta", "XOVER 1-3"} {
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
// reply can't be read (the session died, or a list wasn't ended).
func readCmds(c *client.Conn, cmds []string) ([]string, error) {
	var out []string
	for _, cmd := range cmds {
		code, _, err := c.TryCmd("%s", cmd)
		if err != nil {
			return out, fmt.Errorf("%s: %v", cmd, err)
		}
		multi := map[int]bool{215: true, 220: true, 221: true, 222: true, 224: true, 225: true, 231: true}
		if multi[code] || code == 211 && strings.HasPrefix(cmd, "LISTGROUP") {
			if _, err := c.TryReadDotLines(); err != nil {
				return out, fmt.Errorf("%s: list: %v", cmd, err)
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
		{"behind-header", overwrite(48, 64, 0xff)},
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
		{"table", overwrite(48, 64, 0xff)},
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
// the next session must work again.
func TestCorruptDBFuzz(t *testing.T) {
	cmds := []string{"GROUP test.alpha", "XOVER 1-21", "ARTICLE 1", "ARTICLE 2", "ARTICLE 21",
		"XHDR subject 1-21", "STAT 5", "LISTGROUP test.alpha", "GROUP test.beta", "XOVER 1-3",
		"LIST", "NEWGROUPS 20000101 000000"}
	for _, target := range []string{"db", "active"} {
		t.Run(target, func(t *testing.T) {
			e := corruptEnv(t)
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
					if err != nil || werr != nil {
						t.Fatalf("damage %d (offset %d, %d bytes), %s session: %v, exit %v, stderr %q, replies %q",
							k, off, n, round, err, werr, p.Stderr(), got)
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
	if err := overwrite(48, 64, 0xff)(db); err != nil {
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
	// the index: first and last number (10), then one offset per number
	le := func(off int) uint64 {
		var v uint64
		for i := 7; i >= 0; i-- {
			v = v<<8 | uint64(b[off+i])
		}
		return v
	}
	idx := -1
	for off := 0; off+16+8*10 <= len(b); off += 8 {
		if f := le(off); f >= 1 && f <= 3 && le(off+8) == 10 && le(off+16+8*int(3-f)) != 0 {
			idx = off
			break
		}
	}
	if idx < 0 {
		t.Fatal("index not found in the .db")
	}
	entry := idx + 16 + 8*int(3-le(idx))
	for i := 0; i < 8; i++ {
		b[entry+i] = 0xff
	}
	if err := os.WriteFile(db, b, 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // GroupTimeout 0: sync again

	c, p := corruptSession(t, e)
	got, err := readCmds(c, []string{"GROUP test.alpha", "XOVER 1-10", "ARTICLE 4", "ARTICLE 5"})
	if err != nil {
		t.Errorf("after the damage: %v (so far %q)", err, got)
	}
	want := "GROUP test.alpha:211 XOVER 1-10:224 ARTICLE 4:220 ARTICLE 5:220"
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
