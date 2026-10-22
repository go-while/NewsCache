package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Article numbers above 2^31 and 2^32. RFC 3977 allows up to 2^31-1, but
// the big binary providers are far past that (alt.binaries.boneless was at
// 67486767815 in 2026). NewsCache kept them in int and unsigned int: the
// GROUP reply, OVER, ARTICLE n and the spool's overview records were
// truncated, so such groups showed wrong numbers or nothing at all.

const bigGroup = "alt.binaries.test"

// addBig adds three articles numbered first..first+2 to bigGroup; the last
// one is big enough for its own .art<number> file.
func addBig(e *env, first int64) []string {
	e.Store.AddGroup(bigGroup, "big article numbers")
	ids := make([]string, 0, 3)
	for i := int64(0); i < 3; i++ {
		body := []string{fmt.Sprintf("body of article %d", first+i)}
		if i == 2 {
			body = bigBody()
		}
		a := groupArticle(bigGroup, first+i, body)
		e.Store.AddArticle(bigGroup, a)
		ids = append(ids, a.MessageID)
	}
	return ids
}

func TestArticleNumbers64(t *testing.T) {
	for _, first := range []int64{
		2147483646,  // 2^31-2: crosses 2^31
		4294967294,  // 2^32-2: crosses 2^32
		67486767813, // where the binary groups are
	} {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			testArticleNumbers64(t, first)
		})
	}
}

func testArticleNumbers64(t *testing.T, first int64) {
	e := newEnv(t)
	ids := addBig(e, first)
	n1, n2, n3 := first, first+1, first+2

	c := e.session()
	code, line := c.Cmd("GROUP %s", bigGroup)
	if want := fmt.Sprintf("211 3 %d %d %s", n1, n3, bigGroup); code != 211 || !strings.HasPrefix(line, want) {
		t.Fatalf("GROUP: %q, want %q", line, want)
	}

	if code, line := c.Cmd("XOVER %d-%d", n1, n3); code != 224 {
		t.Fatalf("XOVER: %q", line)
	}
	ov := c.ReadDotLines()
	if len(ov) != 3 {
		t.Fatalf("XOVER %d-%d: %d lines, want 3: %q", n1, n3, len(ov), ov)
	}
	for i, l := range ov {
		if num := strings.SplitN(l, "\t", 2)[0]; num != fmt.Sprint(first+int64(i)) {
			t.Errorf("XOVER line %d starts with %q, want %d", i+1, num, first+int64(i))
		}
	}
	if code, line := c.Cmd("XOVER %d-", n2); code != 224 {
		t.Fatalf("XOVER %d-: %q", n2, line)
	}
	if ov := c.ReadDotLines(); len(ov) != 2 {
		t.Errorf("XOVER %d-: %d lines, want 2: %q", n2, len(ov), ov)
	}

	if code, line := c.Cmd("XHDR subject %d-%d", n1, n3); code != 221 {
		t.Fatalf("XHDR: %q", line)
	}
	hd := c.ReadDotLines()
	want := []string{
		fmt.Sprintf("%d article %d in %s", n1, n1, bigGroup),
		fmt.Sprintf("%d article %d in %s", n2, n2, bigGroup),
		fmt.Sprintf("%d article %d in %s", n3, n3, bigGroup),
	}
	if strings.Join(hd, "\n") != strings.Join(want, "\n") {
		t.Errorf("XHDR subject: %q, want %q", hd, want)
	}

	code, line = c.Cmd("ARTICLE %d", n2)
	if want := fmt.Sprintf("220 %d %s", n2, ids[1]); code != 220 || !strings.HasPrefix(line, want) {
		t.Fatalf("ARTICLE %d: %q, want %q", n2, line, want)
	}
	if art := c.ReadDotLines(); !containsLine(art, fmt.Sprintf("body of article %d", n2)) {
		t.Errorf("ARTICLE %d: wrong article: %q", n2, art)
	}
	if !containsLine(e.Upstream.Lines(), fmt.Sprintf("ARTICLE %d", n2)) &&
		!containsLine(e.Upstream.Lines(), fmt.Sprintf("article %d", n2)) {
		t.Errorf("upstream was not asked for article %d", n2)
	}

	// NEXT and LAST from the current article n2
	if code, line := c.Cmd("NEXT"); code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d %s", n3, ids[2])) {
		t.Errorf("NEXT: %q, want 223 %d %s", line, n3, ids[2])
	}
	if code, line := c.Cmd("LAST"); code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d %s", n2, ids[1])) {
		t.Errorf("LAST: %q, want 223 %d %s", line, n2, ids[1])
	}
	if code, line := c.Cmd("STAT %d", n1); code != 223 || !strings.HasPrefix(line, fmt.Sprintf("223 %d %s", n1, ids[0])) {
		t.Errorf("STAT %d: %q, want 223 %d %s", n1, line, n1, ids[0])
	}

	// The big article goes to a file named after its number.
	if code, line := c.Cmd("BODY %d", n3); code != 222 {
		t.Fatalf("BODY %d: %q", n3, line)
	}
	if body := c.ReadDotLines(); len(body) != len(bigBody()) {
		t.Errorf("BODY %d: %d lines, want %d", n3, len(body), len(bigBody()))
	}
	artFile := filepath.Join(e.Spool, "alt", "binaries", "test", fmt.Sprintf(".art%d", n3))
	if _, err := os.Stat(artFile); err != nil {
		left, _ := filepath.Glob(filepath.Join(e.Spool, "alt", "binaries", "test", ".art*"))
		t.Errorf("big article file: %v (files: %q)", err, left)
	}

	if code, line := c.Cmd("LISTGROUP"); code != 211 {
		t.Fatalf("LISTGROUP: %q", line)
	}
	if lg, want := c.ReadDotLines(), []string{fmt.Sprint(n1), fmt.Sprint(n2), fmt.Sprint(n3)}; strings.Join(lg, " ") != strings.Join(want, " ") {
		t.Errorf("LISTGROUP: %q, want %q", lg, want)
	}
	c.Cmd("QUIT")

	// A second process serves both articles from the spool.
	before := e.Upstream.Count("ARTICLE")
	c2 := e.session()
	c2.Cmd("GROUP %s", bigGroup)
	for i, n := range []int64{n2, n3} {
		code, line := c2.Cmd("ARTICLE %d", n)
		if want := fmt.Sprintf("220 %d %s", n, ids[i+1]); code != 220 || !strings.HasPrefix(line, want) {
			t.Fatalf("ARTICLE %d from the cache: %q, want %q", n, line, want)
		}
		c2.ReadDotLines()
	}
	if n := e.Upstream.Count("ARTICLE"); n != before {
		t.Errorf("upstream ARTICLE count went from %d to %d; want served from the cache", before, n)
	}
}

// TestArticleNumberSyntax: numbers that don't fit in 64 bits, or with
// trailing garbage, are syntax errors (they were cut down by atoi).
func TestArticleNumberSyntax(t *testing.T) {
	e := newEnv(t)
	addBig(e, 67486767813)
	c := e.session()
	c.Cmd("GROUP %s", bigGroup)
	for _, cmd := range []string{
		"ARTICLE 99999999999999999999999",
		"HEAD 67486767814x",
		"STAT 18446744073709551616",
		"XOVER 99999999999999999999999",
		"XOVER 1-99999999999999999999999",
	} {
		if code, line := c.Cmd("%s", cmd); code != 501 {
			switch code {
			case 220, 221, 222, 224: // followed by a dot block
				c.ReadDotLines()
			}
			t.Errorf("%s: %q, want 501", cmd, line)
		}
	}
	// The largest 64-bit number is a valid range end.
	if code, line := c.Cmd("XOVER 67486767814-18446744073709551615"); code != 224 {
		t.Fatalf("XOVER to 2^64-1: %q", line)
	}
	if ov := c.ReadDotLines(); len(ov) != 2 {
		t.Errorf("XOVER to 2^64-1: %d lines, want 2: %q", len(ov), ov)
	}
}

// TestActiveDBFile: the active database is .active64 now (64-bit
// numbers); the old .active is not used.
func TestActiveDBFile(t *testing.T) {
	e := newEnv(t)
	addBig(e, 67486767813)
	c := e.session()
	if code, line := c.Cmd("GROUP %s", bigGroup); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	c.Cmd("QUIT")
	if _, err := os.Stat(filepath.Join(e.Spool, ".active64")); err != nil {
		t.Errorf("no .active64 in the spool: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.Spool, ".active")); err == nil {
		t.Errorf("a new spool has a .active")
	}
}
