package e2e

import (
	"debug/elf"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// TestAuthFileWrongPassword: a wrong password is refused. AuthFailureDelay 0
// skips the delay before the answer (15 s by default).
func TestAuthFileWrongPassword(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	e.Extra = "AuthFailureDelay 0"
	c := e.session()
	c.Cmd("AUTHINFO USER tester")
	if code, line := c.Cmd("AUTHINFO PASS wrong"); code != 502 {
		t.Fatalf("AUTHINFO PASS wrong: %q, want 502", line)
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
			if code, line := c.Cmd("AUTHINFO PASS secret"); code != 502 {
				t.Fatalf("AUTHINFO PASS with stored hash %q: %q, want 502", hash, line)
			}
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP after the refused login: %q, want 211", line)
			}
			login(t, c)
		})
	}
}

// TestAuthFailureDelay: after a failed login NewsCache waits AuthFailureDelay
// seconds times the number of failures before it answers; a later correct
// password still logs in. The delay was a fixed 15 seconds.
func TestAuthFailureDelay(t *testing.T) {
	t.Parallel()
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	e.Extra = "AuthFailureDelay 1"
	c := e.session()
	c.Deadline = 20 * time.Second
	for n := 1; n <= 2; n++ {
		c.Cmd("AUTHINFO USER tester")
		start := time.Now()
		if code, line := c.Cmd("AUTHINFO PASS wrong"); code != 502 {
			t.Fatalf("failure %d: AUTHINFO PASS wrong: %q, want 502", n, line)
		}
		want := time.Duration(n) * time.Second
		if d := time.Since(start); d < want || d > want+5*time.Second {
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
func TestLoginOnlyEntry(t *testing.T) {
	e := loginOnlyEnv(t, "tester:"+md5Hash+":*:*:*:read,post")
	c := e.session()
	for _, cmd := range []string{"MODE READER", "LIST", "GROUP test.alpha", "ARTICLE 1", "POST"} {
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
// command the entry doesn't allow still gets 500, not 480.
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
	if code, line := c.Cmd("POST"); code != 500 {
		t.Fatalf("POST: %q, want 500", line)
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
	if code, line := c.Cmd("XDEBUG"); code != 500 {
		t.Fatalf("XDEBUG after login: %q, want 500 (no debug right)", line)
	}
	if code, line := c.Cmd("POST"); code != 500 {
		t.Fatalf("POST after login: %q, want 500 (no post right)", line)
	}
}

// TestAuthFileNegatedFlagRemoves: "!post" in an auth-file line takes the
// post right of the Client entry away after the login. It used to leave it.
func TestAuthFileNegatedFlagRemoves(t *testing.T) {
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:!post")
	c := e.session()
	login(t, c)
	if code, line := c.Cmd("POST"); code != 500 {
		t.Fatalf("POST after login: %q, want 500 (post removed)", line)
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
	if code, line := c.Cmd("AUTHINFO PASS wrong"); code != 502 {
		t.Fatalf("AUTHINFO PASS: %q, want 502", line)
	}
	c.Cmd("QUIT")
	if log := e.logFile(); !strings.Contains(log, "cannot read the shadow password of "+u.Username) {
		t.Errorf("log does not say why the login failed:\n%s", log)
	}
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
	if runtime.GOOS != "linux" {
		t.Skip("needs Linux-PAM")
	}
	f, err := elf.Open(bin)
	if err != nil {
		t.Skipf("can't read %s: %v", bin, err)
	}
	libs, _ := f.ImportedLibraries()
	f.Close()
	pam := false
	for _, l := range libs {
		pam = pam || strings.HasPrefix(l, "libpam.so")
	}
	if !pam {
		t.Skip("newscache built without PAM (configure --with-pam)")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root to write a PAM service to /etc/pam.d")
	}
	found := false
	for _, d := range []string{"/usr/lib64/security", "/lib64/security", "/usr/lib/x86_64-linux-gnu/security",
		"/lib/x86_64-linux-gnu/security", "/usr/lib/aarch64-linux-gnu/security", "/usr/lib/security", "/lib/security"} {
		if _, err := os.Stat(filepath.Join(d, "pam_permit.so")); err == nil {
			found = true
			break
		}
	}
	if !found {
		t.Skip("pam_permit.so not found")
	}
	name := "newscache-e2e-" + filepath.Base(t.TempDir())
	path := filepath.Join("/etc/pam.d", name)
	if err := os.WriteFile(path, []byte("auth required pam_permit.so\naccount required pam_permit.so\n"), 0o644); err != nil {
		t.Skipf("can't write %s: %v", path, err)
	}
	t.Cleanup(func() { os.Remove(path) })
	return name
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
		if code := loginAs(t, c, "otto"); code == 281 {
			t.Fatalf("login of a user without any line: %d, want a refusal", code)
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
		if code := try(tc.user, tc.pass); code == 281 {
			t.Errorf("AUTHINFO %s / %q: logged in, want a refusal", tc.user, tc.pass)
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
	if code, line := c.Cmd("\tAUTHINFO PASS %s", secret); code != 502 {
		t.Fatalf("AUTHINFO PASS (wrong): %q, want 502", line)
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
	if !strings.Contains(log, "failed authinfo for user") {
		t.Errorf("no masked AUTHINFO failure in the log:\n%s", grepLines(log, "uthinfo"))
	}
}
