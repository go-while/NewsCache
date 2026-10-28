package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// requireFileLog skips the test unless the binary writes its own log file
// (configure --without-syslog). It writes the config.
func requireFileLog(e *env) {
	e.t.Helper()
	e.writeConf()
	out, err := exec.Command(e.Bin, "-c", e.Conf, "-o").Output()
	if err != nil {
		e.t.Fatalf("newscache -o: %v", err)
	}
	if strings.Contains(string(out), "--with-syslog") {
		e.t.Skip("binary built with syslog (configure --without-syslog to test the log file)")
	}
}

// TestLogFileNoFollow: built --without-syslog, newscache and newscacheclean
// opened their log as root, before setugid, with ofstream(ios::app): a
// symlink at the log's name was followed and a target that didn't exist was
// created. The sample config says to make LogDirectory writable for
// Username, so that user could make root append to (or create) any file,
// with text that clients choose. Now only a regular file with one link is
// used; a symlink to the process's own stderr (the container image) is
// still stderr. (As an ordinary user the target is the test's own file; the
// open is the same as root's.)
func TestLogFileNoFollow(t *testing.T) {
	const content = "important content, not a log\n"
	for _, kind := range []string{"symlink", "hard link", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			requireFileLog(e)
			victim := filepath.Join(e.Dir, "victim")
			if kind != "dangling symlink" {
				if err := os.WriteFile(victim, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			link := os.Symlink
			if kind == "hard link" {
				link = os.Link
			}
			if err := link(victim, filepath.Join(e.LogDir, "newscache.log")); err != nil {
				t.Skipf("%s: %v", kind, err)
			}
			p := e.standalone()
			c := e.dial()
			c.Cmd("QUIT")
			c.Close()
			p.Signal(syscall.SIGTERM)
			p.Wait()
			b, err := os.ReadFile(victim)
			if kind == "dangling symlink" {
				if err == nil {
					t.Errorf("the daemon created the target of a dangling symlink at the log's name: %q", b)
				}
			} else if string(b) != content {
				t.Errorf("the daemon wrote through a %s at the log's name: target holds %q", kind, b)
			}
			if !strings.Contains(p.Stderr(), "cannot open log file") {
				t.Errorf("no error about the log file on stderr:\n%s", p.Stderr())
			}
		})
	}
	t.Run("newscacheclean", func(t *testing.T) {
		e := newEnv(t)
		requireFileLog(e)
		clean := filepath.Join(filepath.Dir(e.Bin), "newscacheclean")
		if _, err := os.Stat(clean); err != nil {
			t.Skipf("newscacheclean not found next to %s", e.Bin)
		}
		victim := filepath.Join(e.Dir, "victim")
		if err := os.WriteFile(victim, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(e.LogDir, "newscache_newscacheclean.log")); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(clean, "-c", e.Conf).CombinedOutput()
		if err != nil {
			t.Fatalf("newscacheclean: %v\n%s", err, out)
		}
		if b, _ := os.ReadFile(victim); string(b) != content {
			t.Errorf("newscacheclean wrote through a symlink at its log's name: target holds %q", b)
		}
		if !strings.Contains(string(out), "cannot open log file") {
			t.Errorf("no error about the log file:\n%s", out)
		}
	})
	t.Run("stderr", func(t *testing.T) {
		e := newEnv(t)
		requireFileLog(e)
		if _, err := os.Stat("/dev/stderr"); err != nil {
			t.Skip("no /dev/stderr")
		}
		if err := os.Symlink("/dev/stderr", filepath.Join(e.LogDir, "newscache.log")); err != nil {
			t.Fatal(err)
		}
		p := e.standalone()
		c := e.dial()
		c.Cmd("QUIT")
		c.Close()
		p.Signal(syscall.SIGTERM)
		p.Wait()
		if !strings.Contains(p.Stderr(), "[127.0.0.1]") {
			t.Errorf("a log linked to /dev/stderr didn't reach stderr:\n%s", p.Stderr())
		}
	})
}

// TestGeneralLogNotWritable: when the file log cannot be opened, NewsCache
// reports the error and continues serving clients. This needs the file-log
// build and a non-root user so the read-only log is actually unwritable.
func TestGeneralLogNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: root can write a read-only log file")
	}
	e := newEnv(t)
	e.addArticles("test.log", 1)
	requireFileLog(e)
	log := filepath.Join(e.LogDir, "newscache.log")
	if err := os.WriteFile(log, nil, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(log, 0o444); err != nil {
		t.Fatal(err)
	}

	p := e.standalone()
	c := e.dial()
	if code, line := c.Cmd("GROUP test.log"); code != 211 {
		t.Fatalf("GROUP with an unwritable log: %q, want 211", line)
	}
	if code, line := c.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE with an unwritable log: %q, want 220", line)
	}
	c.ReadDotLines()
	if code, line := c.Cmd("QUIT"); code != 205 {
		t.Errorf("QUIT: %q, want 205", line)
	}
	c.Close()
	p.Signal(syscall.SIGTERM)
	p.Wait()

	if !strings.Contains(p.Stderr(), "cannot open log file") {
		t.Errorf("NewsCache did not report the unwritable log; stderr:\n%s", p.Stderr())
	}
}
