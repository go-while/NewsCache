package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// COMPRESS DEFLATE (RFC 8054) to the news server, with Options compress.

// requireZlib skips the test for a newscache built without zlib
// (configure --without-zlib), which doesn't compress.
func requireZlib(t *testing.T, bin string) {
	t.Helper()
	b, err := exec.Command(bin, "-o").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -o: %v\n%s", err, b)
	}
	if !slices.Contains(strings.Fields(string(b)), "--with-zlib") {
		t.Skip("newscache built without zlib")
	}
}

// compressBody is n lines of text that differ a little, so the compressed
// article is larger than one buffer of either side.
func compressBody(n int) []string {
	body := make([]string, n)
	for i := range body {
		body[i] = fmt.Sprintf("line %05d of a long article, with some text to fill the line %d", i, i*7919%1000)
	}
	return body
}

// waitUpstreamLine waits until the news server has received line.
func waitUpstreamLine(t *testing.T, e *env, line string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(e.Upstream.Lines(), line) {
		if time.Now().After(deadline) {
			t.Fatalf("the news server never received %q: %q", line, e.Upstream.Lines())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func lineIndex(lines []string, want string) int {
	return slices.Index(lines, want)
}

// wireTap sits between NewsCache and the news server and keeps the raw
// bytes of each connection, both ways: what went over the wire, where
// the fake server's Lines are the inflated commands.
type wireTap struct {
	ln    net.Listener
	mu    sync.Mutex
	conns []*tapConn
}

type tapConn struct {
	up, down bytes.Buffer // to the server, from the server
}

type tapWriter struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w tapWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func newWireTap(t *testing.T, target string) *wireTap {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	w := &wireTap{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			tc := &tapConn{}
			w.mu.Lock()
			w.conns = append(w.conns, tc)
			w.mu.Unlock()
			go w.serve(c, target, tc)
		}
	}()
	return w
}

func (w *wireTap) serve(c net.Conn, target string, tc *tapConn) {
	defer c.Close()
	u, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer u.Close()
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(u, io.TeeReader(c, tapWriter{&w.mu, &tc.up}))
		u.(*net.TCPConn).CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		io.Copy(c, io.TeeReader(u, tapWriter{&w.mu, &tc.down}))
		c.(*net.TCPConn).CloseWrite()
		done <- struct{}{}
	}()
	<-done
	<-done
}

func (w *wireTap) addr() string {
	return w.ln.Addr().String()
}

func (w *wireTap) port() string {
	return fmt.Sprint(w.ln.Addr().(*net.TCPAddr).Port)
}

// wire returns the raw bytes of each connection so far.
func (w *wireTap) wire() (up, down [][]byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range w.conns {
		up = append(up, bytes.Clone(c.up.Bytes()))
		down = append(down, bytes.Clone(c.down.Bytes()))
	}
	return up, down
}

// afterCompress splits a connection's raw bytes at the COMPRESS DEFLATE
// command and at the 206 to it: the rest of each direction, or nil when
// there was no 206.
func afterCompress(up, down []byte) (upRest, downRest []byte) {
	i := bytes.Index(up, []byte("COMPRESS DEFLATE\r\n"))
	j := bytes.Index(down, []byte("\r\n206 "))
	if i < 0 || j < 0 {
		return nil, nil
	}
	k := bytes.Index(down[j+2:], []byte("\r\n"))
	if k < 0 {
		return nil, nil
	}
	return up[i+len("COMPRESS DEFLATE\r\n"):], down[j+2+k+2:]
}

// TestUpstreamCompress: with Options compress NewsCache sends COMPRESS
// DEFLATE after MODE READER, and everything after the 206 is compressed
// both ways: the fake server inflates the commands and deflates its
// replies, so any byte sent or read uncompressed fails the session. A
// large article, a large active list and a large posting take more than
// one buffer each way; the QUIT at the end comes through the compressor
// too.
func TestUpstreamCompress(t *testing.T) {
	e := newEnv(t)
	requireZlib(t, e.Bin)
	upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{Compress: true}})
	e.addArticles("test.alpha", 2)
	big := compressBody(3000)
	e.Store.AddArticle("test.alpha", fakeserver.Article{
		Number:    3,
		MessageID: "<big@upstream.test>",
		Headers:   []string{"From: poster@upstream.test", "Subject: big", "Newsgroups: test.alpha"},
		Body:      big,
	})
	for i := 0; i < 500; i++ {
		e.Store.AddGroup(fmt.Sprintf("test.many.group%03d", i), "")
	}
	e.ServerOptions = "compress"
	tap := newWireTap(t, e.Upstream.Addr())
	e.ServerHost = "127.0.0.1"
	e.ServerPort = tap.port()

	p, c := e.sessionProcess()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	if code, line := c.Cmd("ARTICLE 3"); code != 220 {
		t.Fatalf("ARTICLE 3: %q", line)
	}
	lines := c.ReadDotLines()
	if got := lines[len(lines)-len(big):]; !slices.Equal(got, big) {
		t.Fatalf("ARTICLE 3: the body differs (%d lines, want %d)", len(lines), len(big))
	}
	if code, line := c.Cmd("OVER 1-3"); code != 224 {
		t.Fatalf("OVER: %q", line)
	} else if n := len(c.ReadDotLines()); n != 3 {
		t.Errorf("OVER 1-3: %d lines, want 3", n)
	}
	if code, line := c.Cmd("LIST ACTIVE"); code != 215 {
		t.Fatalf("LIST ACTIVE: %q", line)
	} else if n := len(c.ReadDotLines()); n != 501 {
		t.Errorf("LIST ACTIVE: %d groups, want 501", n)
	}
	posted := compressBody(2000)
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("POST: %q", line)
	}
	c.Send("From: a@b.test\r\nSubject: compressed post\r\nNewsgroups: test.alpha\r\n" +
		"Message-ID: <post@client.test>\r\n\r\n" + strings.Join(posted, "\r\n") + "\r\n.\r\n")
	if code, line, err := c.ReadStatus(); err != nil || code != 240 {
		t.Fatalf("POST: %d %q %v", code, line, err)
	}
	c.Cmd("QUIT")
	p.Wait()

	if a := e.Store.Posted(); len(a) != 1 || !slices.Equal(a[0].Body, posted) {
		t.Errorf("the posting reached the news server damaged or not at all (%d postings)", len(a))
	}
	waitUpstreamLine(t, e, "quit")
	up := e.Upstream.Lines()
	mr, cd := lineIndex(up, "mode reader"), lineIndex(up, "COMPRESS DEFLATE")
	if cd < 0 || mr < 0 || mr > cd {
		t.Errorf("want MODE READER before COMPRESS DEFLATE: %q", up)
	}
	if e.Upstream.Accepted() != 1 {
		t.Fatalf("%d connections to the news server, want 1", e.Upstream.Accepted())
	}

	// On the wire: the large texts after the 206 not in plain text, and
	// much smaller than the plain text. (A short reply or command may
	// show as it is: DEFLATE keeps data it can't make smaller in stored
	// blocks. Commands in plain text would fail the fake server's
	// inflater.)
	wu, wd := tap.wire()
	upRest, downRest := afterCompress(wu[0], wd[0])
	if downRest == nil {
		t.Fatalf("no 206 to COMPRESS DEFLATE on the wire:\n%q", wd[0])
	}
	if bytes.Contains(upRest, []byte(posted[100])) {
		t.Errorf("the posting went to the news server uncompressed")
	}
	for _, plain := range []string{"test.many.group100", big[100]} {
		if bytes.Contains(downRest, []byte(plain)) {
			t.Errorf("%q came from the news server uncompressed", plain)
		}
	}
	plainArticle := len(strings.Join(big, "\r\n"))
	if len(downRest) > plainArticle/2 {
		t.Errorf("%d bytes from the news server after the 206, want well under the article's %d", len(downRest), plainArticle)
	}
	plainPost := len(strings.Join(posted, "\r\n"))
	if len(upRest) > plainPost/2 {
		t.Errorf("%d bytes to the news server after the 206, want well under the posting's %d", len(upRest), plainPost)
	}
}

// TestUpstreamCompressLogin: the login comes before COMPRESS (RFC 8054
// 2.2.2: no AUTHINFO after it). When the server asks for the login again
// on the compressed connection, NewsCache doesn't log in there but opens
// a new connection, which logs in and compresses again.
func TestUpstreamCompressLogin(t *testing.T) {
	e := newEnv(t)
	requireZlib(t, e.Bin)
	upstreamWith(t, e, fakeserver.Options{
		StoreOpts: fakeserver.StoreOptions{Compress: true, User: "tester", Pass: "secret"},
		Faults: fakeserver.Faults{
			// the second ARTICLE on a connection, once
			Override:      map[string][]string{"ARTICLE": {"480 Authentication required"}},
			OverrideFrom:  map[string]int{"ARTICLE": 2},
			OverrideTimes: map[string]int{"ARTICLE": 1},
		},
	})
	e.addArticles("test.alpha", 3)
	e.ServerOptions = "compress"
	e.ServerLines = "    User tester\n    Password secret"
	tap := newWireTap(t, e.Upstream.Addr())
	e.ServerHost = "127.0.0.1"
	e.ServerPort = tap.port()

	p, c := e.sessionProcess()
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP: %q", line)
	}
	for _, n := range []int{1, 2} {
		if code, line := c.Cmd("ARTICLE %d", n); code != 220 {
			t.Fatalf("ARTICLE %d: %q", n, line)
		}
		c.ReadDotLines()
	}
	c.Cmd("QUIT")
	p.Wait()

	up := e.Upstream.Lines()
	if n := e.Upstream.Accepted(); n != 2 {
		t.Fatalf("%d connections to the news server, want 2 (a new one for the login): %q", n, up)
	}
	// on each connection: AUTHINFO USER, AUTHINFO PASS, then COMPRESS,
	// and no AUTHINFO after a COMPRESS before the next MODE READER
	compressed := false
	logins, compresses := 0, 0
	for _, l := range up {
		switch {
		case l == "mode reader":
			compressed = false
		case strings.HasPrefix(l, "authinfo "):
			if compressed {
				t.Fatalf("AUTHINFO on a compressed connection: %q", up)
			}
			logins++
		case l == "COMPRESS DEFLATE":
			compressed = true
			compresses++
		}
	}
	if logins != 4 || compresses != 2 {
		t.Errorf("%d AUTHINFO and %d COMPRESS lines, want 4 and 2: %q", logins, compresses, up)
	}
	wu, wd := tap.wire()
	for i := range wu {
		upRest, downRest := afterCompress(wu[i], wd[i])
		if downRest == nil {
			t.Errorf("connection %d: no 206 to COMPRESS DEFLATE on the wire", i+1)
		} else if bytes.Contains(upRest, []byte("authinfo")) {
			t.Errorf("connection %d: AUTHINFO in plain text after COMPRESS", i+1)
		}
	}
}

// TestUpstreamCompressRefused: a server that refuses COMPRESS (403) or
// doesn't know it (500) is used without compression.
func TestUpstreamCompressRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    fakeserver.Options
	}{
		{"403", fakeserver.Options{
			StoreOpts: fakeserver.StoreOptions{Compress: true},
			Faults:    fakeserver.Faults{Override: map[string][]string{"COMPRESS": {"403 Unable to activate compression"}}},
		}},
		{"500", fakeserver.Options{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			requireZlib(t, e.Bin)
			upstreamWith(t, e, tc.o)
			e.addArticles("test.alpha", 2)
			e.ServerOptions = "compress"
			tap := newWireTap(t, e.Upstream.Addr())
			e.ServerHost = "127.0.0.1"
			e.ServerPort = tap.port()
			p, c := e.sessionProcess()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			if code, line := c.Cmd("ARTICLE 2"); code != 220 {
				t.Fatalf("ARTICLE 2: %q", line)
			}
			c.ReadDotLines()
			c.Cmd("QUIT")
			p.Wait()
			if !slices.Contains(e.Upstream.Lines(), "COMPRESS DEFLATE") {
				t.Errorf("COMPRESS DEFLATE not sent: %q", e.Upstream.Lines())
			}
			// the rest in plain text
			if wu, _ := tap.wire(); !bytes.Contains(wu[0], []byte("group test.alpha")) {
				t.Errorf("GROUP not in plain text on the wire")
			}
			if mode, err := logMode(e.Bin); err == nil && mode == "file" {
				e.waitLog("refused COMPRESS DEFLATE: " + tc.name)
			}
		})
	}
}

// TestUpstreamCompressCorrupt: invalid compressed data from the server
// ends the connection (RFC 8054 2.2.2); the client gets an error, not
// garbage, and the log says why.
func TestUpstreamCompressCorrupt(t *testing.T) {
	e := newEnv(t)
	requireZlib(t, e.Bin)
	e.Upstream = fakeserver.New(t, fakeserver.Options{
		Handler: func(c *fakeserver.Conn) {
			for {
				line, err := c.ReadLine()
				if err != nil {
					return
				}
				switch line {
				case "mode reader":
					c.WriteLines("200 ready")
				case "COMPRESS DEFLATE":
					c.WriteLines("206 Compression active")
					// a DEFLATE block of the reserved type 3
					c.Raw().Write([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
				default:
					// the commands after the 206 come compressed
					// and are not inflated here: nothing to answer
				}
			}
		},
	})
	e.addArticles("test.alpha", 1)
	e.ServerOptions = "compress"
	start := time.Now()
	p, c := e.sessionProcess()
	code, line, err := c.TryCmd("GROUP test.alpha")
	if err != nil {
		t.Fatalf("GROUP: no reply: %v", err)
	}
	if code == 211 || code < 400 {
		t.Fatalf("GROUP: %q, want an error", line)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("GROUP took %s", d)
	}
	c.TryCmd("QUIT")
	p.Wait()
	if mode, err := logMode(e.Bin); err == nil && mode == "file" {
		e.waitLog("compressed data: ")
	}
}

// TestUpstreamCompressTLS: compression under TLS (compressed first, then
// encrypted), with ssl and with STARTTLS (which comes before COMPRESS).
func TestUpstreamCompressTLS(t *testing.T) {
	for _, starttls := range []bool{false, true} {
		t.Run(map[bool]string{false: "ssl", true: "starttls"}[starttls], func(t *testing.T) {
			e := newEnv(t)
			requireTLS(t, e.Bin)
			requireZlib(t, e.Bin)
			upstreamWith(t, e, fakeserver.Options{StoreOpts: fakeserver.StoreOptions{Compress: true}})
			e.addArticles("test.alpha", 2)
			ca := newTestCA(t, e.Dir)
			// (the tap sees the stream inside TLS)
			tap := newWireTap(t, e.Upstream.Addr())
			px := newTLSProxy(t, tap.addr(), ca.serverCert(t, []string{"localhost"}, validFrom, validUntil), starttls, false)
			e.ServerHost = "localhost"
			e.ServerPort = px.port()
			e.ServerOptions = "ssl compress"
			if starttls {
				e.ServerOptions = "starttls compress"
			}
			e.ServerLines = "    TLSCAFile " + ca.file
			p, c := e.sessionProcess()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP: %q", line)
			}
			if code, line := c.Cmd("ARTICLE 2"); code != 220 {
				t.Fatalf("ARTICLE 2: %q", line)
			}
			c.ReadDotLines()
			c.Cmd("QUIT")
			p.Wait()
			wu, wd := tap.wire()
			if len(wu) != 1 {
				t.Fatalf("%d connections to the news server, want 1", len(wu))
			}
			upRest, downRest := afterCompress(wu[0], wd[0])
			if downRest == nil {
				t.Fatalf("no 206 to COMPRESS DEFLATE inside TLS")
			}
			// (the fake server inflates the commands, and NewsCache
			// read its compressed replies: here only that the 206
			// was on the wire, and something after it)
			if len(upRest) == 0 || len(downRest) == 0 {
				t.Errorf("nothing after the 206 inside TLS")
			}
		})
	}
}
