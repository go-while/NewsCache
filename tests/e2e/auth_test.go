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

// TestAuthFileWrongPassword: a wrong password is refused. NewsCache waits
// 15 s per failed attempt before it answers, so this test is slow.
func TestAuthFileWrongPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("waits 15 s for the auth failure delay")
	}
	t.Parallel()
	e := authFileEnv(t, "tester:"+md5Hash+":*:*:*:")
	c := e.session()
	c.Deadline = 30 * time.Second
	c.Cmd("AUTHINFO USER tester")
	if code, line := c.Cmd("AUTHINFO PASS wrong"); code != 502 {
		t.Fatalf("AUTHINFO PASS wrong: %q, want 502", line)
	}
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
		if code, line := c.Cmd(cmd); code != 480 {
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
