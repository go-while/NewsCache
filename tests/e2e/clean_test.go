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
