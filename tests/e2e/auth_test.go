package e2e

import (
	"os"
	"path/filepath"
	"runtime"
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
