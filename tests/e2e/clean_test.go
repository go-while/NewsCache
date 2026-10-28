package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
		"DbCnt: 1\n", "ArtCnt: 2\n", "PurgedDbCnt: 0\n", "PurgedArtCnt: 0\n",
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
		"DbCnt: 1\n", "ArtCnt: 2\n", "PurgedDbCnt: 0\n", "PurgedArtCnt: 0\n",
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

	if !e.Store.RemoveArticle(group, 1) {
		t.Fatal("failed to remove article from upstream store")
	}
	config, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	tinySpool := strings.Replace(string(config), "SpoolSize 102400\n", "SpoolSize 1\n", 1)
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

	// session rewrites the config with its normal SpoolSize; the purge itself
	// has already happened, and the upstream no longer has article 1.
	c := e.session()
	if code, line := c.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("GROUP after expiry: %q, want 211", line)
	}
	if code, line := c.Cmd("OVER 1-1"); code != 224 {
		t.Fatalf("OVER 1-1 after expiry: %q, want 224", line)
	} else if got := c.ReadDotLines(); len(got) != 0 {
		t.Errorf("OVER 1-1 after expiry listed articles: %q", got)
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
