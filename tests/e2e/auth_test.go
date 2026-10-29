package e2e

import (
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/proc"
)

// secretHash is crypt("secret") with SHA-512 ($6$), which glibc and libxcrypt
// support. macOS's crypt() only does DES, so tests that need the system
// crypt() skip there; the others use md5Hash, which NewsCache checks itself.
const secretHash = "$6$e2esalt$14AiApLt4fjJwCUoL7cnR2lk2Ud56tWQGtode.8FL5VjLppQrOdD3JEfiWos6RfNwB/lVvP23URi3Hjp4dxnx0"

// authFileEnv returns an env whose inetd client may log in against an auth
// file with the given lines. Its entry has "allow read post", so the login
// is optional: it only adds the rights from the auth file.
func authFileEnv(t *testing.T, lines ...string) *env {
	return authEnv(t, `allow read post
    List *
    Read *
    PostTo *`, lines...)
}

// loginOnlyEnv is like authFileEnv, but its entry allows nothing except the
// login, so the client has no rights until it has logged in.
func loginOnlyEnv(t *testing.T, lines ...string) *env {
	return authEnv(t, `allow authentication
    List !*
    Read !*
    PostTo !*`, lines...)
}

// authEnv returns an env whose inetd client gets the given allow/List/Read/
// PostTo settings plus an auth file with the given lines.
func authEnv(t *testing.T, rights string, lines ...string) *env {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	path := filepath.Join(e.Dir, "newscache.auth")
	var data []byte
	for _, l := range lines {
		data = append(data, l+"\n"...)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	e.AccessList = `AccessList {
  Client stdin {
    ` + rights + `
    Authentication file:` + path + `
  }
  Default {
    allow none
  }
}`
	return e
}

// TestAuthFileShortLine: an auth-file line with only user, password and the
// List field is valid. The Read, PostTo and flags fields are missing, and
// reading them must not run past the end of the field list.
func TestAuthFileShortLine(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*")
	c := e.session()
	if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS secret"); code != 281 {
		t.Fatalf("AUTHINFO PASS: %q, want 281", line)
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP after login: %q, want 211", line)
	}
	if code, line := c.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1 after login: %q, want 220", line)
	}
	c.ReadDotLines()
}

// TestAuthFileSecurityWarnings: permissions and symlinks on an auth file
// produce one warning per path per config read, but never change login outcomes.
func TestAuthFileSecurityWarnings(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		setup  func(*testing.T, string)
	}{
		{name: "private"},
		{
			name:   "group-and-world-writable",
			reason: "writable by group and world",
			setup: func(t *testing.T, path string) {
				if err := os.Chmod(path, 0o666); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:   "symlink",
			reason: "symbolic link",
			setup: func(t *testing.T, path string) {
				target := path + ".target"
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("can't create symlink: %v", err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
			authFile := filepath.Join(e.Dir, "newscache.auth")
			if tc.setup != nil {
				tc.setup(t, authFile)
			}
			e.AccessList = strings.Replace(e.AccessList, "Client stdin {",
				"Client 127.0.0.0/8 {", 1)
			p := e.standalone()
			for n := 1; n <= 2; n++ {
				c := e.dial()
				if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
					t.Fatalf("connection %d AUTHINFO USER: %q, want 381", n, line)
				}
				if code, line := c.Cmd("AUTHINFO PASS secret"); code != 281 {
					t.Fatalf("connection %d AUTHINFO PASS: %q, want 281", n, line)
				}
				c.Cmd("QUIT")
				c.Close()
			}
			p.Stop()
			log := e.logFile()
			prefix := "authentication file " + authFile
			warningLines := grepLines(log, prefix)
			var lines []string
			if warningLines != "" {
				lines = strings.Split(warningLines, "\n")
			}
			if tc.reason == "" {
				if len(lines) != 0 {
					t.Errorf("private auth file produced a warning:\n%s", strings.Join(lines, "\n"))
				}
				return
			}
			if len(lines) != 1 {
				t.Fatalf("got %d warning lines for %s, want one:\n%s", len(lines), authFile, strings.Join(lines, "\n"))
			}
			if !strings.Contains(lines[0], tc.reason) {
				t.Errorf("warning lacks reason %q:\n%s", tc.reason, lines[0])
			}
		})
	}
}

func TestAuthFileSecurityWarningsOnReload(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	requireFileLog(e)
	authFile := filepath.Join(e.Dir, "newscache.auth")
	if err := os.Chmod(authFile, 0o666); err != nil {
		t.Fatal(err)
	}
	e.AccessList = strings.Replace(e.AccessList, "Client stdin {",
		"Client 127.0.0.0/8 {", 1)
	p := e.standalone()
	e.writeConf()
	if err := p.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	prefix := "authentication file " + authFile
	deadline := time.Now().Add(5 * time.Second)
	for {
		log, err := os.ReadFile(filepath.Join(e.LogDir, "newscache.log"))
		if err == nil && strings.Count(string(log), prefix) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("SIGHUP reload did not log the auth-file warning again:\n%s", log)
		}
		time.Sleep(10 * time.Millisecond)
	}
	c := e.dial()
	if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER after SIGHUP: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS secret"); code != 281 {
		t.Fatalf("AUTHINFO PASS after SIGHUP: %q, want 281", line)
	}
	c.Cmd("QUIT")
	c.Close()
	p.Stop()
	log := e.logFile()
	warnings := grepLines(log, prefix)
	if n := strings.Count(warnings, prefix); n != 2 {
		t.Errorf("got %d warning lines for startup and SIGHUP reload, want 2:\n%s", n, warnings)
	}
}

// TestAuthFileUserHashOnly: an auth-file line with only user and hash is
// valid; the missing fields add nothing, like empty ones. Such lines used to
// be refused (502 after the failure delay).
func TestAuthFileUserHashOnly(t *testing.T) {
	t.Run("keeps the entry's rights", func(t *testing.T) {
		e := authFileEnv(t, "tester:"+md5Hash)
		c := e.session()
		login(t, c)
		if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
			t.Fatalf("GROUP after login: %q, want 211", line)
		}
	})
	t.Run("adds no rights", func(t *testing.T) {
		e := loginOnlyEnv(t, "tester:"+md5Hash)
		c := e.session()
		login(t, c)
		if code, line := c.Cmd("GROUP test.alpha"); code == 211 {
			t.Fatalf("GROUP after login: %q; the line gives no List/Read rights", line)
		}
	})
}

// md5Hash is base64(MD5("secret")), the auth-file format NewsCache detects
// as an MD5 hash (24 characters ending in "=="). printf secret | openssl md5
// -binary | base64 gives the same value.
const md5Hash = "Xr4ilOzQ4PCOq3aQ0qbuaQ=="

// TestAuthFileMD5Hash: a login against an MD5 hash. The MD5 code used a
// 64-bit type for its 32-bit words, overran a stack buffer and killed the
// session on 64-bit systems.
func TestAuthFileMD5Hash(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	c := e.session()
	if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS secret"); code != 281 {
		t.Fatalf("AUTHINFO PASS: %q, want 281", line)
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP after login: %q, want 211", line)
	}
}

// TestAuthFileCryptHash: a login against a crypt() hash from the system's
// crypt library.
func TestAuthFileCryptHash(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS crypt() has no SHA-512 ($6$)")
	}
	e := authFileEnv(t, "tester:"+secretHash+":*:*:*:")
	c := e.session()
	if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS secret"); code != 281 {
		t.Fatalf("AUTHINFO PASS: %q, want 281", line)
	}
}

// TestAuthFileWrongPassword: a wrong password is refused with 481 (RFC 4643
// 2.3.2; it was 502). AuthFailureDelay 0 skips the delay before the answer
// (15 s by default).
func TestAuthFileWrongPassword(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	e.Extra = "AuthFailureDelay 0"
	c := e.session()
	c.Cmd("AUTHINFO USER tester")
	if code, line := c.Cmd("AUTHINFO PASS wrong"); code != 481 {
		t.Fatalf("AUTHINFO PASS wrong: %q, want 481", line)
	}
}

// TestFailedPassRequiresNewUser: RFC 4643 2.3.2 requires PASS to follow USER.
// After any failed PASS, another PASS must be out of sequence; a fresh USER
// lets the client retry successfully.
func TestFailedPassRequiresNewUser(t *testing.T) {
	for _, tc := range []struct {
		name     string
		password string
	}{
		{"wrong password", "wrong"},
		{"password too long", strings.Repeat("p", 600)},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
			e.Extra = "AuthFailureDelay 0"
			c := e.session()
			if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
				t.Fatalf("AUTHINFO USER: %q, want 381", line)
			}
			if code, line := c.Cmd("AUTHINFO PASS %s", tc.password); code != 481 {
				t.Fatalf("AUTHINFO PASS failure: %q, want 481", line)
			}
			if code, line := c.Cmd("AUTHINFO PASS secret"); code != 482 {
				t.Fatalf("AUTHINFO PASS after failure: %q, want 482", line)
			}
			if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
				t.Fatalf("AUTHINFO USER retry: %q, want 381", line)
			}
			if code, line := c.Cmd("AUTHINFO PASS secret"); code != 281 {
				t.Fatalf("AUTHINFO PASS retry: %q, want 281", line)
			}
		})
	}
}

// TestOverlongUserEndsPendingUser: a replacement AUTHINFO USER that is too
// long gets 481 and also ends the pending USER, so the next PASS gets 482
// instead of being checked against the earlier name (RFC 4643 2.3.2).
func TestOverlongUserEndsPendingUser(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	e.Extra = "AuthFailureDelay 0"
	c := e.session()
	if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO USER %s", strings.Repeat("u", 600)); code != 481 {
		t.Fatalf("AUTHINFO USER (too long): %q, want 481", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS secret"); code != 482 {
		t.Fatalf("AUTHINFO PASS after a failed USER: %q, want 482", line)
	}
	if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER retry: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS secret"); code != 281 {
		t.Fatalf("AUTHINFO PASS retry: %q, want 281", line)
	}
}

// TestAuthinfoFailureLogNamesCheckedUser: "failed authinfo for user <name>"
// is logged only when that user's password was checked and refused. A PASS
// out of sequence (482) or too long, after an earlier failure, is logged as
// "failed authinfo" without a name; it was logged as a second failed login
// of the earlier user, whose password wasn't checked again.
func TestAuthinfoFailureLogNamesCheckedUser(t *testing.T) {
	const wrong = "wr0ng-client-pw"
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	e.Extra = "AuthFailureDelay 0"
	c := e.session()
	if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS %s", wrong); code != 481 {
		t.Fatalf("AUTHINFO PASS (wrong): %q, want 481", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS %s", wrong); code != 482 {
		t.Fatalf("AUTHINFO PASS again: %q, want 482", line)
	}
	c.Cmd("QUIT")
	time.Sleep(100 * time.Millisecond)
	log := e.logFile()
	if strings.Contains(log, wrong) {
		t.Errorf("the log has the password:\n%s", grepLines(log, wrong))
	}
	if n := strings.Count(log, "failed authinfo for user  tester"); n != 1 {
		t.Errorf("%d failed logins of tester logged, want 1 (the 482 isn't one):\n%s",
			n, grepLines(log, "uthinfo"))
	}
	if n := strings.Count(log, "failed authinfo\n"); n != 1 {
		t.Errorf("%d nameless AUTHINFO failures logged, want 1 (the 482):\n%s",
			n, grepLines(log, "uthinfo"))
	}
}

// TestAuthFileBadHash: a stored hash that is no hash ("*" or "!", the
// locked-account markers of shadow files, an empty field, or a method
// crypt() doesn't know) refuses the login and leaves the session usable;
// another user's line still works. The check passed crypt()'s result to
// strcmp without looking: crypt() returns NULL for such a setting with
// glibc's and BSD's crypt, which killed the session, and libxcrypt's
// failure token "*0" matched a stored "*0" for any password.
func TestAuthFileBadHash(t *testing.T) {
	for _, hash := range []string{"*", "!", "", "$9$junk", "*0", "*1", "x"} {
		t.Run(fmt.Sprintf("%q", hash), func(t *testing.T) {
			e := authFileEnv(t, "bad:"+hash+":*:*:*:", "tester:"+md5Hash+":*:*:*:")
			e.Extra = "AuthFailureDelay 0"
			c := e.session()
			if code, line := c.Cmd("AUTHINFO USER bad"); code != 381 {
				t.Fatalf("AUTHINFO USER bad: %q, want 381", line)
			}
			if code, line := c.Cmd("AUTHINFO PASS secret"); code != 481 {
				t.Fatalf("AUTHINFO PASS with stored hash %q: %q, want 481", hash, line)
			}
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP after the refused login: %q, want 211", line)
			}
			login(t, c)
		})
	}
}

// TestAuthFailureDelay: after each failed login NewsCache waits
// AuthFailureDelay seconds (at most 1) before it answers; a later correct
// password still logs in. The delay was a fixed 15 seconds, then (1.3.22)
// AuthFailureDelay times the number of failures.
func TestAuthFailureDelay(t *testing.T) {
	t.Parallel()
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	e.Extra = "AuthFailureDelay 1"
	c := e.session()
	c.Deadline = 20 * time.Second
	for n := 1; n <= 2; n++ {
		c.Cmd("AUTHINFO USER tester")
		start := time.Now()
		if code, line := c.Cmd("AUTHINFO PASS wrong"); code != 481 {
			t.Fatalf("failure %d: AUTHINFO PASS wrong: %q, want 481", n, line)
		}
		want := time.Second
		if d := time.Since(start); d < want || d > want+2*time.Second {
			t.Errorf("failure %d: answered after %v, want about %v", n, d.Round(time.Millisecond), want)
		}
	}
	login(t, c)
}

// TestLoginOnlyEntry: an entry with "allow authentication" and nothing
// listed, as in FEDORA.md and the container config, lets a client that
// hasn't logged in neither list nor read; after the login it gets the rights
// from the auth file. Before the login, known commands get 480, so that
// newsreaders offer a login (RFC 4643); they used to get "500 What?".
// MODE READER is answered (201, no posting yet): clients send it before
// AUTHINFO and must not send it after the login (RFC 4643 2.2); it got 480.
func TestLoginOnlyEntry(t *testing.T) {
	e := loginOnlyEnv(t, "tester:"+md5Hash+":*:*:*:read,post")
	c := e.session()
	if code, line := c.Cmd("MODE READER"); code != 201 {
		t.Fatalf("MODE READER before login: %q, want 201", line)
	}
	for _, cmd := range []string{"LIST", "GROUP test.alpha", "ARTICLE 1", "POST"} {
		if code, line := c.Cmd("%s", cmd); code != 480 {
			t.Fatalf("%s before login: %q, want 480", cmd, line)
		}
	}
	if code, line := c.Cmd("FOOBAR"); code != 500 {
		t.Fatalf("unknown command before login: %q, want 500", line)
	}
	if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS secret"); code != 281 {
		t.Fatalf("AUTHINFO PASS: %q, want 281", line)
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP after login: %q, want 211", line)
	}
	if code, line := c.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("ARTICLE 1 after login: %q, want 220", line)
	}
	c.ReadDotLines()
}

// postingCase is an access entry and whether its client may post right
// after the greeting: 200 if it may, 201 if not (RFC 3977 5.1.1, 5.3).
type postingCase struct {
	name string
	env  func(t *testing.T) *env
	want int
}

// postingCases are the entries TestModeReaderPosting and TestGreetingPosting
// check: one that may post, and four that may not (yet).
func postingCases() []postingCase {
	entry := func(rights, auth string) string {
		return `AccessList {
  Client stdin {
    ` + rights + `
    List *
    Read *
    PostTo *
    Authentication ` + auth + `
  }
  Default {
    allow none
  }
}`
	}
	return []postingCase{
		{"read post", func(t *testing.T) *env {
			e := newEnv(t)
			e.AccessList = entry("allow read post", "none")
			return e
		}, 200},
		{"read only", func(t *testing.T) *env {
			e := newEnv(t)
			e.AccessList = entry("allow read", "none")
			return e
		}, 201},
		// "Authentication unix" without fields: POST needs the login
		{"login required", func(t *testing.T) *env {
			e := newEnv(t)
			e.AccessList = entry("allow read post", "unix")
			return e
		}, 201},
		{"login only", func(t *testing.T) *env {
			return loginOnlyEnv(t, "tester:"+md5Hash+":*:*:*:read,post")
		}, 201},
		{"optional login adds post", func(t *testing.T) *env {
			return authEnv(t, `allow read
    List *
    Read *
    PostTo *`, "tester:"+md5Hash+":*:*:*:post")
		}, 201},
	}
}

// TestModeReaderPosting: MODE READER answers 200 only when the client may
// post now and 201 otherwise (RFC 3977 5.3), also before a login. It
// answered 200 to every client, and 480 where the entry allows only the
// login.
func TestModeReaderPosting(t *testing.T) {
	for _, tc := range postingCases() {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.env(t).session()
			code, line := c.Cmd("MODE READER")
			if code != tc.want {
				t.Fatalf("MODE READER: %q, want %d", line, tc.want)
			}
			// the reply must match what POST does
			if code == 201 {
				if code, line := c.Cmd("POST"); code == 340 {
					t.Errorf("POST after MODE READER 201: %q", line)
				}
			}
		})
	}
}

// TestGreetingPosting: the greeting follows the same rule as MODE READER,
// 200 only when the client may post now and 201 otherwise (RFC 3977
// 5.1.1). It was 200 for every client. The texts of both replies are the
// same as MODE READER's.
func TestGreetingPosting(t *testing.T) {
	for _, tc := range postingCases() {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.env(t).session()
			want := fmt.Sprintf("%d ", tc.want)
			if !strings.HasPrefix(c.Greeting, want) {
				t.Fatalf("greeting %q, want %d", c.Greeting, tc.want)
			}
			if code, line := c.Cmd("MODE READER"); len(line) < 4 || line[4:] != c.Greeting[4:] {
				t.Errorf("MODE READER %q (%d), greeting %q: different texts", line, code, c.Greeting)
			}
		})
	}
}

// TestAuthFileLoginOptional: with "allow read post" and an auth file, the
// login is optional. A client that doesn't log in still reads. FEDORA.md
// warns about this; the test keeps the behaviour from changing unnoticed.
func TestAuthFileLoginOptional(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	c := e.session()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP without login: %q, want 211", line)
	}
}

// TestAuthFileLoginOptionalPost: with "allow read" and an auth file, POST
// before the login gets 480, since a login can add the post right.
func TestAuthFileLoginOptionalPost(t *testing.T) {
	e := authEnv(t, `allow read
    List *
    Read *
    PostTo *`, "tester:"+md5Hash+":*:*:*:post")
	c := e.session()
	if code, line := c.Cmd("POST"); code != 480 {
		t.Fatalf("POST before login: %q, want 480", line)
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP without login: %q, want 211", line)
	}
}

// TestNoLoginNo480: without an Authentication entry no login can help, so a
// command the entry doesn't allow gets 502 (440 for POST), not 480. It got
// "500 What?", the reply to an unknown command (RFC 3977 3.2.1, 6.3.1).
// An unknown command still gets 500.
func TestNoLoginNo480(t *testing.T) {
	e := newEnv(t)
	e.AccessList = `AccessList {
  Client stdin {
    allow read
    List *
    Read *
    PostTo *
    Authentication none
  }
  Default {
    allow none
  }
}`
	c := e.session()
	if code, line := c.Cmd("POST"); code != 440 {
		t.Fatalf("POST: %q, want 440", line)
	}
	if code, line := c.Cmd("XDEBUG help"); code != 502 {
		t.Fatalf("XDEBUG: %q, want 502 (no debug right)", line)
	}
	if code, line := c.Cmd("FOOBAR"); code != 500 {
		t.Fatalf("FOOBAR: %q, want 500 (unknown command)", line)
	}
	if code, line := c.Cmd("DATE"); code != 111 {
		t.Fatalf("DATE: %q, want 111 (the session goes on)", line)
	}
}

// login logs in as tester with password secret.
func login(t *testing.T, c interface {
	Cmd(format string, args ...any) (int, string)
}) {
	t.Helper()
	if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS secret"); code != 281 {
		t.Fatalf("AUTHINFO PASS: %q, want 281", line)
	}
}

// TestAuthFileNegatedFlagGrantsNothing: "!post" in an auth-file line must
// not grant anything. It used to set every flag except post (access_flags
// |= ~post), so after the login the client also got debug (XDEBUG).
func TestAuthFileNegatedFlagGrantsNothing(t *testing.T) {
	e := loginOnlyEnv(t, "tester:"+md5Hash+":*:*:*:read,!post")
	c := e.session()
	login(t, c)
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP after login: %q, want 211", line)
	}
	// XDEBUG without arguments answers 501 when debug is allowed.
	if code, line := c.Cmd("XDEBUG"); code != 502 {
		t.Fatalf("XDEBUG after login: %q, want 502 (no debug right)", line)
	}
	if code, line := c.Cmd("POST"); code != 440 {
		t.Fatalf("POST after login: %q, want 440 (no post right)", line)
	}
}

// TestAuthFileNegatedFlagRemoves: "!post" in an auth-file line takes the
// post right of the Client entry away after the login. It used to leave it.
func TestAuthFileNegatedFlagRemoves(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:!post")
	c := e.session()
	login(t, c)
	if code, line := c.Cmd("POST"); code != 440 {
		t.Fatalf("POST after login: %q, want 440 (post removed)", line)
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP after login: %q, want 211 (read kept)", line)
	}
}

// TestAuthUnixShadowNotReadable: "Authentication unix" can't check a local
// account with a shadow password when NewsCache doesn't run as root (only
// root may read /etc/shadow). The login fails, and the log says why; it used
// to fail silently. configure also never enabled shadow support by default,
// so even as root the check compared against the "x" from /etc/passwd.
func TestAuthUnixShadowNotReadable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /etc/passwd with shadow passwords")
	}
	if os.Geteuid() == 0 {
		t.Skip("runs as root, which may read /etc/shadow")
	}
	if testing.Short() {
		t.Skip("waits 15 s for the auth failure delay")
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if !hasShadowPassword(u.Username) {
		t.Skipf("%s has no shadow password (\"x\") in /etc/passwd", u.Username)
	}
	t.Parallel()
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.AccessList = `AccessList {
  Client stdin {
    allow read post
    List *
    Read *
    PostTo *
    Authentication unix::::
  }
  Default {
    allow none
  }
}`
	c := e.session()
	c.Deadline = 30 * time.Second
	if code, line := c.Cmd("AUTHINFO USER %s", u.Username); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS wrong"); code != 481 {
		t.Fatalf("AUTHINFO PASS: %q, want 481", line)
	}
	c.Cmd("QUIT")
	if log := e.logFile(); !strings.Contains(log, "cannot read the shadow password of "+u.Username) {
		t.Errorf("log does not say why the login failed:\n%s", log)
	}
}

// unixUser is the account unixSession adds to its /etc/passwd, with the
// password "secret" (md5Hash).
const unixUser = "nc-e2e-unix-tester"

// unixSession starts an inetd session for e in which "Authentication unix"
// can log in as unixUser: newscache runs in a user and mount namespace
// with an /etc/passwd of the test's own bind-mounted over the system's.
func unixSession(t *testing.T, e *env) *client.Conn {
	t.Helper()
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, unixUser+":"+md5Hash+":4242:4242::/nonexistent:/bin/false\n"...)
	passwd := filepath.Join(e.Dir, "passwd")
	if err := os.WriteFile(passwd, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return namespaceSession(t, e, map[string]string{passwd: "/etc/passwd"})
}

// namespaceSession starts an inetd session for e in a user and mount
// namespace with the given files or directories (source -> target)
// bind-mounted over the system's (skipped where the system doesn't allow
// one). It runs as root of the namespace, so Username and Groupname are
// left out.
func namespaceSession(t *testing.T, e *env, binds map[string]string) *client.Conn {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("needs Linux namespaces")
	}
	if err := exec.Command("unshare", "-rm", "true").Run(); err != nil {
		t.Skipf("no user and mount namespace here (unshare -rm: %v)", err)
	}
	e.writeConf()
	conf, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(conf), "\n") {
		if !strings.HasPrefix(l, "Username ") && !strings.HasPrefix(l, "Groupname ") {
			lines = append(lines, l)
		}
	}
	if err := os.WriteFile(e.Conf, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	o := e.procOpts("-i")
	script := ""
	n := 0
	for src, dst := range binds {
		n++
		o.Env = append(o.Env, fmt.Sprintf("BIND_SRC%d=%s", n, src), fmt.Sprintf("BIND_DST%d=%s", n, dst))
		script += fmt.Sprintf("mount --bind \"$BIND_SRC%d\" \"$BIND_DST%d\" || exit 99\n", n, n)
	}
	o.Args = append([]string{"-rm", "/bin/sh", "-c", script + `exec "$@"`, "nc", o.Path}, o.Args...)
	o.Path = "unshare"
	p := proc.StartStdio(t, o)
	c := client.New(t, p.Conn())
	if _, err := c.ReadGreeting(); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	return c
}

// hasShadowPassword reports whether name's password field in /etc/passwd is
// "x", i.e. the hash is in /etc/shadow.
func hasShadowPassword(name string) bool {
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Split(l, ":"); len(f) > 1 && f[0] == name {
			return f[1] == "x"
		}
	}
	return false
}

// pamPermitService writes a PAM service that accepts every user and
// password (pam_permit) and returns its name; it is removed when the test
// ends. It skips unless the binary has PAM support and the test may write
// /etc/pam.d (as root; PAM reads services only from there).
func pamPermitService(t *testing.T, bin string) string {
	t.Helper()
	return pamService(t, bin, "pam_permit")
}

// pamService is pamPermitService with the given module (pam_permit or
// pam_deny) for auth and account.
func pamService(t *testing.T, bin, module string) string {
	t.Helper()
	return pamServiceText(t, bin, module, "auth required "+module+".so\naccount required "+module+".so\n")
}

// requirePamBuild skips unless the binary is linked with Linux-PAM.
func requirePamBuild(t *testing.T, bin string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("needs Linux-PAM")
	}
	f, err := elf.Open(bin)
	if err != nil {
		t.Skipf("can't read %s: %v", bin, err)
	}
	libs, _ := f.ImportedLibraries()
	f.Close()
	for _, l := range libs {
		if strings.HasPrefix(l, "libpam.so") {
			return
		}
	}
	t.Skip("newscache built without PAM (configure --with-pam)")
}

// requirePamModule skips unless this is Linux, the binary is linked with
// PAM and the PAM module is installed.
func requirePamModule(t *testing.T, bin, module string) {
	t.Helper()
	requirePamBuild(t, bin)
	found := false
	for _, d := range []string{"/usr/lib64/security", "/lib64/security", "/usr/lib/x86_64-linux-gnu/security",
		"/lib/x86_64-linux-gnu/security", "/usr/lib/aarch64-linux-gnu/security", "/usr/lib/security", "/lib/security"} {
		if _, err := os.Stat(filepath.Join(d, module+".so")); err == nil {
			found = true
			break
		}
	}
	if !found {
		t.Skip(module + ".so not found")
	}
}

// pamServiceText writes a PAM service with the given text, after the same
// checks as pamService (Linux, a PAM build, module present). It needs write
// access to /etc/pam.d: root, or (better, newscache never runs as root) a
// test user given it, e.g. setfacl -m u:newscache:rwx /etc/pam.d.
func pamServiceText(t *testing.T, bin, module, text string) string {
	t.Helper()
	requirePamModule(t, bin, module)
	name := "newscache-e2e-" + filepath.Base(t.TempDir())
	path := filepath.Join("/etc/pam.d", name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Skipf("can't write %s (needs write access to /etc/pam.d): %v", path, err)
	}
	t.Cleanup(func() { os.Remove(path) })
	return name
}

// TestPamRhost: NewsCache gives PAM the client's address as PAM_RHOST, so
// that host-based rules apply. It didn't: pam_access, pam_succeed_if rhost
// and the like saw no host, and a service that allows logins only from some
// hosts refused them all (or, written the other way round, didn't restrict).
// A service that requires rhost 127.0.0.1 lets the local client in; one that
// requires another address refuses it.
func TestPamRhost(t *testing.T) {
	for _, tc := range []struct {
		rhost string
		want  int
	}{
		{"127.0.0.1", 281},
		{"192.0.2.1", 481},
	} {
		t.Run(tc.rhost, func(t *testing.T) {
			e := newEnv(t)
			service := pamServiceText(t, e.Bin, "pam_succeed_if",
				"auth required pam_succeed_if.so rhost = "+tc.rhost+"\naccount required pam_permit.so\n")
			e.Extra = "AuthFailureDelay 0"
			e.AccessList = `AccessList {
  Client 127.0.0.1 {
    allow authentication
    List !*
    Read !*
    PostTo !*
    PAMServicename ` + service + `
    Authentication pam
  }
  Default {
    allow none
  }
}`
			e.standalone()
			// An existing user: pam_succeed_if before Linux-PAM 1.4
			// (openSUSE Leap 15.6 has 1.3.0) looks the user up first and
			// refuses an unknown one, whatever the condition tests.
			u, err := user.Current()
			if err != nil {
				t.Fatal(err)
			}
			c := e.dialRaw()
			if code, line := c.Cmd("AUTHINFO USER %s", u.Username); code != 381 {
				t.Fatalf("AUTHINFO USER: %q, want 381", line)
			}
			if code, line := c.Cmd("AUTHINFO PASS anything"); code != tc.want {
				t.Errorf("AUTHINFO PASS with rhost rule %s: %q, want %d", tc.rhost, line, tc.want)
			}
		})
	}
}

// TestPamRhostInetd: in inetd mode (and when getpeername fails) there is no
// client address, but ClientData::client_addr was never initialised, so
// check_pam_user_pass passed whatever the stack held as PAM_RHOST. PAM must
// see no remote host. pam_exec records the PAM_RHOST it is given. (On the
// unfixed code this fails only when the stack is not zero there, certainly
// with -ftrivial-auto-var-init=pattern.)
func TestPamRhostInetd(t *testing.T) {
	e := newEnv(t)
	out := filepath.Join(e.Dir, "rhost.out")
	script := filepath.Join(e.Dir, "rhost.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"${PAM_RHOST-unset}\" > "+out+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	requirePamModule(t, e.Bin, "pam_exec")
	requirePamModule(t, e.Bin, "pam_permit")
	// The service lives in a pam.d of the test's own, bind-mounted over
	// /etc/pam.d in a namespace, so that no write access to /etc/pam.d
	// is needed.
	const service = "nc-e2e-rhost"
	pamd := filepath.Join(e.Dir, "pam.d")
	if err := os.Mkdir(pamd, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "auth required pam_exec.so quiet " + script + "\naccount required pam_permit.so\n"
	if err := os.WriteFile(filepath.Join(pamd, service), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Extra = "AuthFailureDelay 0"
	e.AccessList = `AccessList {
  Client stdin {
    allow authentication
    List !*
    Read !*
    PostTo !*
    PAMServicename ` + service + `
    Authentication pam
  }
  Default {
    allow none
  }
}`
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	c := namespaceSession(t, e, map[string]string{pamd: "/etc/pam.d"})
	if code, line := c.Cmd("AUTHINFO USER %s", u.Username); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("AUTHINFO PASS anything"); code != 281 {
		t.Fatalf("AUTHINFO PASS: %q, want 281", line)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("pam_exec did not run: %v", err)
	}
	if got := string(b); got != "unset" && got != "" {
		t.Errorf("PAM_RHOST in inetd mode = %q, want none", got)
	}
}

// TestPamServiceWarning: a PAM service without a configuration in
// /etc/pam.d or /etc/pam.conf makes PAM fall back to its "other" service,
// which nobody intended. NewsCache warns once per service name, at startup
// and after a reload; an existing service gets no warning. Logins stay the
// same, so this needs no write access to /etc/pam.d.
func TestPamServiceWarning(t *testing.T) {
	e := newEnv(t)
	requirePamBuild(t, e.Bin)
	requireFileLog(e)
	existing := ""
	for _, n := range []string{"login", "sshd", "su", "passwd", "other", "system-auth"} {
		if _, err := os.Stat("/etc/pam.d/" + n); err == nil {
			existing = n
			break
		}
	}
	if existing == "" {
		t.Skip("no common PAM service in /etc/pam.d to use as the existing one")
	}
	const missing = "newscache-e2e-no-such-service"
	entry := func(addr, service, auth string) string {
		return `  Client ` + addr + ` {
    allow authentication
    List !*
    Read !*
    PostTo !*
    PAMServicename ` + service + `
    Authentication ` + auth + `
  }
`
	}
	authFile := filepath.Join(e.Dir, "pam.auth")
	if err := os.WriteFile(authFile, []byte("*:*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.AccessList = "AccessList {\n" +
		entry("127.0.0.1", missing, "pam") +
		entry("127.0.0.2", missing, "pam+file:"+authFile) +
		entry("127.0.0.3", existing, "pam") +
		"  Default {\n    allow none\n  }\n}"
	p := e.standalone()
	e.writeConf()
	if err := p.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	prefix := "PAM service " + missing + " not found in /etc/pam.d or /etc/pam.conf"
	deadline := time.Now().Add(5 * time.Second)
	for {
		log, _ := os.ReadFile(filepath.Join(e.LogDir, "newscache.log"))
		if strings.Count(string(log), prefix) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected a warning at startup and after SIGHUP:\n%s", log)
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Stop()
	log := e.logFile()
	if n := strings.Count(log, prefix); n != 2 {
		t.Errorf("got %d warnings for the missing service, want 2 (startup, reload; once per name):\n%s",
			n, grepLines(log, "PAM service"))
	}
	if strings.Contains(log, "PAM service "+existing+" not found") {
		t.Errorf("warned about the existing service %s:\n%s", existing, grepLines(log, "PAM service"))
	}
}

// TestAuthPamFileRights: with "Authentication pam+file", the rights of the
// line "*" and of the user's own line add up. The fields used to be joined
// as strings without a comma ("test.alpha" and "test.beta" gave
// "test.alphatest.beta"), so the user lost both groups; lines of different
// lengths lost fields; and a user without a line of their own was refused
// although the manual says "*" applies to all users.
func TestAuthPamFileRights(t *testing.T) {
	e := newEnv(t)
	service := pamPermitService(t, e.Bin)
	e.addArticles("test.alpha", 1)
	e.addArticles("test.beta", 1)
	e.addArticles("test.gamma", 1)
	e.Extra = "AuthFailureDelay 0"
	setup := func(lines ...string) {
		path := filepath.Join(e.Dir, "pamfile.auth")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		e.AccessList = `AccessList {
  Client stdin {
    allow authentication
    List !*
    Read !*
    PostTo !*
    PAMServicename ` + service + `
    Authentication pam+file:` + path + `
  }
  Default {
    allow none
  }
}`
	}
	loginAs := func(t *testing.T, c interface {
		Cmd(format string, args ...any) (int, string)
	}, user string) int {
		t.Helper()
		if code, line := c.Cmd("AUTHINFO USER %s", user); code != 381 {
			t.Fatalf("AUTHINFO USER %s: %q, want 381", user, line)
		}
		code, _ := c.Cmd("AUTHINFO PASS anything")
		return code
	}
	// group checks that the client may (open) or may not read g.
	group := func(t *testing.T, c interface {
		Cmd(format string, args ...any) (int, string)
	}, g string, open bool) {
		t.Helper()
		code, line := c.Cmd("GROUP %s", g)
		if open && code != 211 {
			t.Errorf("GROUP %s: %q, want 211", g, line)
		} else if !open && code == 211 {
			t.Errorf("GROUP %s: %q, want a refusal", g, line)
		}
	}

	t.Run("own line and star line add up", func(t *testing.T) {
		// the user's line is longer than the "*" line
		setup("hugo:test.alpha:test.alpha::read", "*:test.beta:test.beta")
		c := e.session()
		if code := loginAs(t, c, "hugo"); code != 281 {
			t.Fatalf("login: %d, want 281", code)
		}
		group(t, c, "test.alpha", true)
		group(t, c, "test.beta", true)
		group(t, c, "test.gamma", false)
	})
	t.Run("a negation in one line doesn't open another", func(t *testing.T) {
		// "*" and "!test.gamma" must not become "*!test.gamma"
		setup("hugo:test.alpha:test.alpha::read", "*:!test.gamma:!test.gamma")
		c := e.session()
		if code := loginAs(t, c, "hugo"); code != 281 {
			t.Fatalf("login: %d, want 281", code)
		}
		group(t, c, "test.alpha", true)
		group(t, c, "test.beta", false)
	})
	t.Run("star line for a user without a line", func(t *testing.T) {
		setup("hugo:test.alpha:test.alpha::read", "*:test.beta:test.beta::read")
		c := e.session()
		if code := loginAs(t, c, "otto"); code != 281 {
			t.Fatalf("login: %d, want 281 (the \"*\" line applies to all users)", code)
		}
		group(t, c, "test.beta", true)
		group(t, c, "test.alpha", false)
	})
	t.Run("no line applies", func(t *testing.T) {
		setup("hugo:test.alpha:test.alpha::read")
		c := e.session()
		if code := loginAs(t, c, "otto"); code != 481 {
			t.Fatalf("login of a user without any line: %d, want 481", code)
		}
	})
}

// TestAuthFileLockedAndHashAsPassword: a locked auth-file line (password
// field "*" or "!...") never lets anyone in, and a hash doesn't work as its
// own password. Built with --enable-plainpass, the password was first
// compared as plain text: "*" logged in to a locked line, and the hash
// string to any line.
func TestAuthFileLockedAndHashAsPassword(t *testing.T) {
	e := authEnv(t, `allow authentication
    List !*
    Read !*
    PostTo !*`, "locked:*", "banned:!"+secretHash, "tester:"+secretHash, "md5:"+md5Hash)
	e.Extra = "AuthFailureDelay 0"
	try := func(user, pass string) int {
		c := e.session()
		defer c.Cmd("QUIT")
		c.Cmd("AUTHINFO USER %s", user)
		code, _ := c.Cmd("AUTHINFO PASS %s", pass)
		return code
	}
	for _, tc := range []struct{ user, pass string }{
		{"locked", "*"},
		{"banned", "!" + secretHash},
		{"banned", "secret"},
		{"tester", secretHash},
		{"md5", md5Hash},
	} {
		if code := try(tc.user, tc.pass); code != 481 {
			t.Errorf("AUTHINFO %s / %q: %d, want 481 (refused)", tc.user, tc.pass, code)
		}
	}
	if runtime.GOOS != "darwin" {
		if code := try("tester", "secret"); code != 281 {
			t.Errorf("AUTHINFO tester / the right password: %d, want 281", code)
		}
	}
	if code := try("md5", "secret"); code != 281 {
		t.Errorf("AUTHINFO md5 / the right password: %d, want 281", code)
	}
}

// TestAuthinfoLeadingBlankNotLogged: the log shows AUTHINFO without its
// arguments, also when the line starts with blanks. The test for AUTHINFO
// looked at the line as it came, but the command split skips leading
// blanks, so " AUTHINFO PASS <password>" ran as AUTHINFO and was logged in
// full, at level info and again with "failed" at level notice.
func TestAuthinfoLeadingBlankNotLogged(t *testing.T) {
	const secret = "s3cr3t-client-pw"
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	e.Extra = "AuthFailureDelay 0"
	c := e.session()
	if code, line := c.Cmd(" AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd("\tAUTHINFO PASS %s", secret); code != 481 {
		t.Fatalf("AUTHINFO PASS (wrong): %q, want 481", line)
	}
	if code, line := c.Cmd("  AUTHINFO USER tester"); code != 381 {
		t.Fatalf("AUTHINFO USER: %q, want 381", line)
	}
	if code, line := c.Cmd(" AUTHINFO PASS secret"); code != 281 {
		t.Fatalf("AUTHINFO PASS: %q, want 281", line)
	}
	c.Cmd("QUIT")
	time.Sleep(100 * time.Millisecond)
	log := e.logFile()
	for _, s := range []string{secret, "PASS secret"} {
		if strings.Contains(log, s) {
			t.Errorf("the log has the password (%q):\n%s", s, grepLines(log, s))
		}
	}
	if !strings.Contains(log, "failed authinfo for user  tester") {
		t.Errorf("AUTHINFO failure log does not name tester:\n%s", grepLines(log, "uthinfo"))
	}
}

// TestAuthinfoVariantsNotLogged: malformed commands whose first token starts
// with "auth" were logged with their arguments at Info and Notice. Keep only
// the command word, and clear the reused request buffer before the next read.
func TestAuthinfoVariantsNotLogged(t *testing.T) {
	const secret = "s3cr3t-client-pw"
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	e.Extra = "AuthFailureDelay 0"
	c := e.session()
	for _, tc := range []struct {
		command string
		want    int
	}{
		{"auth info pass " + secret, 500},
		{"authinfo: pass " + secret, 500},
		{"authinfo pass " + secret, 482},
	} {
		if code, line := c.Cmd("%s", tc.command); code != tc.want {
			t.Errorf("%q: %q, want %d", tc.command, line, tc.want)
		}
	}
	c.Cmd("QUIT")
	time.Sleep(100 * time.Millisecond)
	log := e.logFile()
	if strings.Contains(log, secret) {
		t.Errorf("the log has the AUTHINFO password:\n%s", grepLines(log, secret))
	}
	for _, want := range []string{"unrecognized auth", "unrecognized authinfo:", "failed authinfo"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks redacted entry %q:\n%s", want, grepLines(log, "auth"))
		}
	}
}

// TestAuthFailureDelayFixed: each failed login waits the same short delay,
// at most 1 s, also with an AuthFailureDelay from an older configuration.
// The delay was AuthFailureDelay times the number of failures (15, 30 and
// 45 s by default, up to 3 h with 3600), which only held the session's
// process and memory for each guessing connection.
func TestAuthFailureDelayFixed(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	e.Extra = "AuthFailureDelay 15"
	c := e.session()
	for i := 1; i <= 2; i++ {
		if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
			t.Fatalf("AUTHINFO USER: %q, want 381", line)
		}
		start := time.Now()
		if code, line := c.Cmd("AUTHINFO PASS wrong"); code != 481 {
			t.Fatalf("AUTHINFO PASS (wrong) #%d: %q, want 481", i, line)
		}
		if d := time.Since(start); d < 900*time.Millisecond || d > 3*time.Second {
			t.Errorf("failed login #%d answered after %v, want about 1 s", i, d.Round(10*time.Millisecond))
		}
	}
}

// TestAuthMaxFailures: after the third failed login the connection is
// closed (400), with every LogStyle. With "LogStyle strict-inn" the check
// was skipped (it sat in the block that writes NewsCache's own log lines),
// so a client could guess passwords for ever on one connection.
func TestAuthMaxFailures(t *testing.T) {
	for _, logStyle := range []string{"", "LogStyle ip-address strict-inn"} {
		name := logStyle
		if name == "" {
			name = "default LogStyle"
		}
		t.Run(name, func(t *testing.T) {
			e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
			e.Extra = "AuthFailureDelay 0\n" + logStyle
			c := e.session()
			c.Deadline = 10 * time.Second
			for n := 1; n <= 3; n++ {
				if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
					t.Fatalf("try %d: AUTHINFO USER: %q, want 381", n, line)
				}
				if code, line := c.Cmd("AUTHINFO PASS wrong%d", n); code != 481 {
					t.Fatalf("try %d: AUTHINFO PASS: %q, want 481", n, line)
				}
			}
			code, line, err := c.ReadStatus()
			if err != nil || code != 400 {
				t.Fatalf("after the third failure: %q, %v; want 400", line, err)
			}
			if l, err := c.ReadLine(); err == nil {
				t.Fatalf("after the 400: %q, want the connection closed", l)
			}
		})
	}
}

// TestAuthinfoAfterLogin: after a successful login AUTHINFO gets 502 (RFC
// 4643 2.2). It was accepted, and a second login added the second user's
// rights to the first one's; a failed second login kept the first one's
// rights under the second user's name. Without an Authentication there is
// no login at all: 502 at once, no 381, no failure delay.
func TestAuthinfoAfterLogin(t *testing.T) {
	t.Run("second login", func(t *testing.T) {
		e := loginOnlyEnv(t, "tester:"+md5Hash+":*:*:*:read",
			"poster:"+md5Hash+":*:*:*:read,post")
		c := e.session()
		login(t, c)
		for _, cmd := range []string{"AUTHINFO USER poster", "AUTHINFO PASS secret", "AUTHINFO USER tester"} {
			if code, line := c.Cmd("%s", cmd); code != 502 {
				t.Errorf("%s after the login: %q, want 502", cmd, line)
			}
		}
		if code, line := c.Cmd("POST"); code != 440 {
			if code == 340 {
				c.Send(".\r\n")
				c.ReadStatus()
			}
			t.Errorf("POST: %q, want 440 (tester has no post right)", line)
		}
		if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
			t.Errorf("GROUP: %q, want 211 (tester's rights kept)", line)
		}
	})
	t.Run("no Authentication", func(t *testing.T) {
		e := newEnv(t)
		c := e.session()
		if code, line := c.Cmd("AUTHINFO USER tester"); code != 502 {
			t.Errorf("AUTHINFO USER without Authentication: %q, want 502", line)
		}
	})
}

// TestAuthinfoFailedReplies: every failed login answers 481 (RFC 4643
// 2.3.2), whatever the backend and the reason; they all answered 502, which
// means that AUTHINFO isn't available at all. AUTHINFO PASS without a user
// name stays 482 (out of sequence), also with a password that is too long.
func TestAuthinfoFailedReplies(t *testing.T) {
	// tryLogin sends AUTHINFO USER user (381 expected) and AUTHINFO PASS
	// pass, and returns the reply to the PASS.
	tryLogin := func(t *testing.T, c interface {
		Cmd(format string, args ...any) (int, string)
	}, user, pass string) (int, string) {
		t.Helper()
		if code, line := c.Cmd("AUTHINFO USER %s", user); code != 381 {
			t.Fatalf("AUTHINFO USER %s: %q, want 381", user, line)
		}
		return c.Cmd("AUTHINFO PASS %s", pass)
	}
	t.Run("unknown user, file", func(t *testing.T) {
		e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
		e.Extra = "AuthFailureDelay 0"
		c := e.session()
		if code, line := tryLogin(t, c, "nosuchuser", "secret"); code != 481 {
			t.Fatalf("AUTHINFO PASS for an unknown user: %q, want 481", line)
		}
		login(t, c)
	})
	t.Run("unknown user, unix", func(t *testing.T) {
		e := newEnv(t)
		e.Extra = "AuthFailureDelay 0"
		e.AccessList = `AccessList {
  Client stdin {
    allow read post
    List *
    Read *
    PostTo *
    Authentication unix::::
  }
  Default {
    allow none
  }
}`
		c := e.session()
		if code, line := tryLogin(t, c, "nc-e2e-no-such-user", "secret"); code != 481 {
			t.Fatalf("AUTHINFO PASS for an unknown user: %q, want 481", line)
		}
	})
	t.Run("PAM refuses", func(t *testing.T) {
		e := newEnv(t)
		service := pamService(t, e.Bin, "pam_deny")
		e.Extra = "AuthFailureDelay 0"
		e.AccessList = `AccessList {
  Client stdin {
    allow read post
    List *
    Read *
    PostTo *
    PAMServicename ` + service + `
    Authentication pam::::
  }
  Default {
    allow none
  }
}`
		c := e.session()
		if code, line := tryLogin(t, c, "tester", "secret"); code != 481 {
			t.Fatalf("AUTHINFO PASS refused by PAM: %q, want 481", line)
		}
	})
	t.Run("argument too long", func(t *testing.T) {
		e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
		e.Extra = "AuthFailureDelay 0"
		c := e.session()
		long := strings.Repeat("u", 600)
		if code, line := c.Cmd("AUTHINFO USER %s", long); code != 481 {
			t.Fatalf("AUTHINFO USER with 600 characters: %q, want 481", line)
		}
		if code, line := tryLogin(t, c, "tester", long); code != 481 {
			t.Fatalf("AUTHINFO PASS with 600 characters: %q, want 481", line)
		}
		login(t, c)
	})
	t.Run("PASS before USER", func(t *testing.T) {
		e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
		c := e.session()
		if code, line := c.Cmd("AUTHINFO PASS secret"); code != 482 {
			t.Fatalf("AUTHINFO PASS before AUTHINFO USER: %q, want 482", line)
		}
		login(t, c)
	})
	// The sequence comes first: a password too long to keep, sent
	// without a user name, got 481 (the length check ran first).
	t.Run("long PASS before USER", func(t *testing.T) {
		e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
		c := e.session()
		long := strings.Repeat("p", 600)
		if code, line := c.Cmd("AUTHINFO PASS %s", long); code != 482 {
			t.Fatalf("AUTHINFO PASS with 600 characters before AUTHINFO USER: %q, want 482", line)
		}
		login(t, c)
	})
}

// TestLoginRequiredCommands: where a login is required ("Authentication
// unix" without fields), CAPABILITIES and MODE READER are answered before
// the login, and an unknown command gets 500; the other commands get 480.
// Everything but AUTHINFO, QUIT and HELP got 480, so a client couldn't
// learn from CAPABILITIES that it may log in (RFC 3977 5.2, RFC 4643 2.2).
func TestLoginRequiredCommands(t *testing.T) {
	e := newEnv(t)
	e.addArticles("test.alpha", 1)
	e.AccessList = `AccessList {
  Client stdin {
    allow read post
    List *
    Read *
    PostTo *
    Authentication unix
  }
  Default {
    allow none
  }
}`
	c := e.session()
	checkCaps(t, "before login", capabilities(t, c), []string{"VERSION 2", "AUTHINFO USER"}, nil)
	if code, line := c.Cmd("MODE READER"); code != 200 && code != 201 {
		t.Errorf("MODE READER before login: %q, want 200 or 201", line)
	}
	if code, line := c.Cmd("FOOBAR"); code != 500 {
		t.Errorf("unknown command before login: %q, want 500", line)
	}
	for _, cmd := range []string{"GROUP test.alpha", "LIST", "POST"} {
		if code, line := c.Cmd("%s", cmd); code != 480 {
			t.Errorf("%s before login: %q, want 480", cmd, line)
		}
	}
	if code, line := c.Cmd("AUTHINFO USER nobody"); code != 381 {
		t.Errorf("AUTHINFO USER: %q, want 381", line)
	}
}

// TestAuthFileLongLine: auth-file lines may be longer than 1024 bytes.
// They were read in pieces of 1023 bytes: the line lost the fields after
// the cut, and the rest was parsed as a line of its own, so text at that
// place became another user.
func TestAuthFileLongLine(t *testing.T) {
	// a List field of about 2000 bytes; test.alpha is in it
	var pad strings.Builder
	for i := 0; pad.Len() < 2000; i++ {
		fmt.Fprintf(&pad, "pad.group%04d,", i)
	}
	long := "tester:" + md5Hash + ":" + pad.String() + "test.alpha:test.alpha:*:read"
	// a line whose byte 1024 starts what looks like a line for "bob"
	head := "other:" + md5Hash + ":"
	phantom := head + strings.Repeat("x", 1023-len(head)) + "bob:" + md5Hash + ":*:*:*:read"
	e := loginOnlyEnv(t, long, phantom)
	e.Extra = "AuthFailureDelay 0"
	c := e.session()
	login(t, c)
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP test.alpha after the login with the long line: %q, want 211", line)
	}
	c = e.session()
	c.Cmd("AUTHINFO USER bob")
	if code, line := c.Cmd("AUTHINFO PASS secret"); code == 281 {
		t.Errorf("login as bob, who has no line: %q, want a refusal", line)
	}
}

// TestLoginLogged: a successful login is logged with the user name, as a
// failed one is. The log said only "authinfo", so an admin couldn't tell
// which account a session used.
func TestLoginLogged(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	c := e.session()
	login(t, c)
	c.Cmd("QUIT")
	time.Sleep(100 * time.Millisecond)
	if log := e.logFile(); !strings.Contains(log, "authenticated as tester") {
		t.Errorf("no \"authenticated as tester\" in the log:\n%s", log)
	}
}
