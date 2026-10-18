package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// secretHash is crypt("secret") with SHA-512 ($6$), which glibc and libxcrypt
// both support.
const secretHash = "$6$e2esalt$14AiApLt4fjJwCUoL7cnR2lk2Ud56tWQGtode.8FL5VjLppQrOdD3JEfiWos6RfNwB/lVvP23URi3Hjp4dxnx0"

// authFileEnv returns an env whose inetd client must log in against an auth
// file with the given lines.
func authFileEnv(t *testing.T, lines ...string) *env {
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
    allow read post
    List *
    Read *
    PostTo *
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
	e := authFileEnv(t, "tester:"+secretHash+":*")
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

// TestAuthFileWrongPassword: a wrong password is refused. NewsCache waits
// 15 s per failed attempt before it answers, so this test is slow.
func TestAuthFileWrongPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("waits 15 s for the auth failure delay")
	}
	t.Parallel()
	e := authFileEnv(t, "tester:"+secretHash+":*:*:*:")
	c := e.session()
	c.Deadline = 30 * time.Second
	c.Cmd("AUTHINFO USER tester")
	if code, line := c.Cmd("AUTHINFO PASS wrong"); code != 502 {
		t.Fatalf("AUTHINFO PASS wrong: %q, want 502", line)
	}
}
