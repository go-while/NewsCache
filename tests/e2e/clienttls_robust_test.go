package e2e

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
)

type robustTLSFiles struct {
	certPath string
	keyPath  string
}

type robustTLSFixture struct {
	e     *env
	ca    *testCA
	files robustTLSFiles
	addr  string
}

func robustTLSMaterial(t *testing.T, e *env, ca *testCA, name string, notBefore, notAfter time.Time) robustTLSFiles {
	t.Helper()
	cert := ca.serverCert(t, []string{"localhost"}, notBefore, notAfter)
	key, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("server certificate has private key type %T", cert.PrivateKey)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	files := robustTLSFiles{
		certPath: filepath.Join(e.Dir, name+"-cert.pem"),
		keyPath:  filepath.Join(e.Dir, name+"-key.pem"),
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(files.certPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files.keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return files
}

func robustTLSConfig(e *env, addr string, files robustTLSFiles, priority string) {
	var lines []string
	if addr != "" {
		lines = append(lines, "TLSListenTo "+addr)
	}
	if files.certPath != "" {
		lines = append(lines, "TLSCertFile "+files.certPath)
	}
	if files.keyPath != "" {
		lines = append(lines, "TLSKeyFile "+files.keyPath)
	}
	if priority != "" {
		lines = append(lines, "TLSPriority "+priority)
	}
	if e.Extra != "" {
		e.Extra = strings.TrimRight(e.Extra, "\n") + "\n"
	}
	e.Extra += strings.Join(lines, "\n")
}

func newRobustTLSFixture(t *testing.T) *robustTLSFixture {
	t.Helper()
	e := newEnv(t)
	requireClientTLS(t, e.Bin)
	e.addArticles("test.alpha", 2)
	ca := newTestCA(t, e.Dir)
	files := robustTLSMaterial(t, e, ca, "robust", validFrom, validUntil)
	addr := freeAddr(t)
	for addr == e.Listen {
		addr = freeAddr(t)
	}
	robustTLSConfig(e, addr, files, "")
	return &robustTLSFixture{e: e, ca: ca, files: files, addr: addr}
}

func robustTLSClientConfig(ca *testCA, insecure bool) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "localhost"}
	if insecure {
		// Used only for tests of expired certificates; the handshake itself
		// must still complete so we can check that the listener remains up.
		cfg.InsecureSkipVerify = true
	} else {
		roots := x509.NewCertPool()
		roots.AddCert(ca.cert)
		cfg.RootCAs = roots
	}
	return cfg
}

func robustTLSDial(addr string, ca *testCA, config *tls.Config) (*tls.Conn, error) {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	return tls.DialWithDialer(dialer, "tcp", addr, config)
}

func robustTLSSession(t *testing.T, addr string, ca *testCA) (*client.Conn, *tls.Conn) {
	t.Helper()
	tlsConn, err := robustTLSDial(addr, ca, robustTLSClientConfig(ca, false))
	if err != nil {
		t.Fatalf("TLS dial %s: %v", addr, err)
	}
	c := client.New(t, tlsConn)
	if greeting, err := c.ReadGreeting(); err != nil ||
		(!strings.HasPrefix(greeting, "200 ") && !strings.HasPrefix(greeting, "201 ")) {
		t.Fatalf("TLS greeting = %q, %v; want 200 or 201", greeting, err)
	}
	return c, tlsConn
}

func robustTLSCertSerial(t *testing.T, f *robustTLSFixture) string {
	t.Helper()
	conn, err := robustTLSDial(f.addr, f.ca, robustTLSClientConfig(f.ca, false))
	if err != nil {
		t.Fatalf("TLS handshake: %v", err)
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].SerialNumber.String()
}

func hasPlainNNTPStatus(data []byte) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) >= 4 && line[0] >= '0' && line[0] <= '9' &&
			line[1] >= '0' && line[1] <= '9' && line[2] >= '0' &&
			line[2] <= '9' && line[3] == ' ' {
			return true
		}
	}
	return false
}

func readUntilPeerCloses(t *testing.T, conn net.Conn, within time.Duration) []byte {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(within)); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(conn)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("peer did not close within %v (received %d bytes)", within, len(b))
	}
	return b
}

// TLS 1.2 ClientHello with no session id. Sending the same hello to each
// fork makes the returned server randoms and session ids directly comparable.
func robustClientHello12() []byte {
	var extensions []byte
	appendExtension := func(kind uint16, data []byte) {
		extensions = append(extensions, byte(kind>>8), byte(kind), byte(len(data)>>8), byte(len(data)))
		extensions = append(extensions, data...)
	}

	host := []byte("localhost")
	name := append([]byte{0, byte(len(host) >> 8), byte(len(host))}, host...)
	sni := append([]byte{byte(len(name) >> 8), byte(len(name))}, name...)
	appendExtension(0, sni)
	appendExtension(10, []byte{0, 4, 0, 29, 0, 23}) // x25519, secp256r1
	appendExtension(11, []byte{1, 0})               // uncompressed points
	appendExtension(13, []byte{0, 6, 4, 3, 8, 4, 4, 1})

	body := []byte{3, 3}
	body = append(body, bytes.Repeat([]byte{0x42}, 32)...)
	body = append(body, 0) // empty session id
	body = append(body, 0, 6, 0xc0, 0x2b, 0xc0, 0x2c, 0xc0, 0x2f)
	body = append(body, 1, 0) // null compression
	body = append(body, byte(len(extensions)>>8), byte(len(extensions)))
	body = append(body, extensions...)
	handshake := []byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	handshake = append(handshake, body...)
	record := []byte{22, 3, 1, byte(len(handshake) >> 8), byte(len(handshake))}
	return append(record, handshake...)
}

type robustServerHello struct {
	random    string
	sessionID string
}

func readRobustServerHello(conn net.Conn) (robustServerHello, error) {
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return robustServerHello{}, err
	}
	if _, err := conn.Write(robustClientHello12()); err != nil {
		return robustServerHello{}, err
	}
	return parseServerHello(conn)
}

const (
	maxHelloRecordLen  = 16384 + 2048 // RFC 5246 6.2.3: ciphertext limit
	maxHelloMessageLen = 16384
	maxHelloRecords    = 16
)

// parseServerHello reads TLS records from r until the first handshake message
// (which must be a ServerHello) is complete. A handshake message may span
// several records (RFC 5246 6.2.1), and the 4-byte handshake header may itself
// be split. The session id may be empty (RFC 5246 7.4.1.3). All lengths are
// bounded; the caller sets the deadline on the connection.
func parseServerHello(r io.Reader) (robustServerHello, error) {
	var hs []byte // handshake bytes collected so far
	need := 4     // bytes wanted: header first, then header plus body
	for records := 0; len(hs) < need; records++ {
		if records >= maxHelloRecords {
			return robustServerHello{}, fmt.Errorf("ServerHello not complete after %d records", records)
		}
		header := make([]byte, 5)
		if _, err := io.ReadFull(r, header); err != nil {
			return robustServerHello{}, fmt.Errorf("reading TLS record header (have %d of %d handshake bytes): %w", len(hs), need, err)
		}
		n := int(header[3])<<8 | int(header[4])
		if n > maxHelloRecordLen {
			return robustServerHello{}, fmt.Errorf("TLS record length %d exceeds %d", n, maxHelloRecordLen)
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return robustServerHello{}, fmt.Errorf("reading TLS record payload: %w", err)
		}
		switch header[0] {
		case 22:
			hs = append(hs, payload...)
		case 21:
			if len(payload) >= 2 {
				return robustServerHello{}, fmt.Errorf("TLS alert instead of ServerHello: level %d description %d", payload[0], payload[1])
			}
			return robustServerHello{}, fmt.Errorf("truncated TLS alert record")
		default:
			return robustServerHello{}, fmt.Errorf("TLS record type %d before ServerHello is complete, want handshake (22)", header[0])
		}
		if need == 4 && len(hs) >= 4 {
			if hs[0] != 2 {
				return robustServerHello{}, fmt.Errorf("first handshake message type %d is not ServerHello", hs[0])
			}
			messageLen := int(hs[1])<<16 | int(hs[2])<<8 | int(hs[3])
			// version 2 + random 32 + session id length 1 + suite 2 + compression 1
			if messageLen < 38 || messageLen > maxHelloMessageLen {
				return robustServerHello{}, fmt.Errorf("ServerHello length %d out of range 38..%d", messageLen, maxHelloMessageLen)
			}
			need = 4 + messageLen
		}
	}
	body := hs[4:need]
	sessionLen := int(body[34])
	if sessionLen > 32 || 35+sessionLen+3 > len(body) {
		return robustServerHello{}, fmt.Errorf("ServerHello session id length %d in %d-byte body", sessionLen, len(body))
	}
	return robustServerHello{
		random:    string(body[2:34]),
		sessionID: string(body[35 : 35+sessionLen]),
	}, nil
}

// distinctHelloProblems returns one message per duplicate: every server
// random must be unique; session ids only when non-empty (an empty id means
// no resumption, and many servers send one).
func distinctHelloProblems(hellos []robustServerHello) []string {
	var problems []string
	randoms, ids := map[string]bool{}, map[string]bool{}
	for _, h := range hellos {
		if randoms[h.random] {
			problems = append(problems, fmt.Sprintf("duplicate ServerHello random across forked sessions: %x", h.random))
		}
		randoms[h.random] = true
		if h.sessionID == "" {
			continue
		}
		if ids[h.sessionID] {
			problems = append(problems, fmt.Sprintf("duplicate TLS session id across forked sessions: %x", h.sessionID))
		}
		ids[h.sessionID] = true
	}
	return problems
}

func TestClientTLSForksUseDistinctServerRandoms(t *testing.T) {
	f := newRobustTLSFixture(t)
	f.e.MaxConnections = 32
	f.e.standalone()

	const clients = 16
	type result struct {
		hello robustServerHello
		err   error
	}
	results := make(chan result, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", f.addr, 3*time.Second)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer conn.Close()
			hello, err := readRobustServerHello(conn)
			results <- result{hello: hello, err: err}
		}()
	}
	wg.Wait()
	close(results)
	var hellos []robustServerHello
	for result := range results {
		if result.err != nil {
			t.Fatalf("TLS handshake prefix: %v", result.err)
		}
		hellos = append(hellos, result.hello)
	}
	for _, p := range distinctHelloProblems(hellos) {
		t.Error(p)
	}
}

func TestClientTLSStalledReaderTimesOutAndReleasesGroup(t *testing.T) {
	f := newRobustTLSFixture(t)
	f.e.ClientTimeout = 6
	const group = "alt.binaries.tls-stall"
	f.e.Store.AddGenerated(group, "", fakeserver.Generator{Low: 1, High: 40000})
	f.e.writeConf()
	mode, err := logMode(f.e.Bin)
	if err != nil {
		t.Fatal(err)
	}
	haveLog := mode != "syslog"
	haveProc := runtime.GOOS == "linux"
	if !haveLog && !haveProc {
		t.Skip("needs the log file (configure --without-syslog) or /proc (Linux) to see the session end")
	}
	p := f.e.standalone()

	first, tlsConn := robustTLSSession(t, f.addr, f.ca)
	defer tlsConn.Close()
	first.Deadline = 20 * time.Second
	tcp, ok := tlsConn.NetConn().(*net.TCPConn)
	if !ok {
		t.Fatalf("TLS connection's network transport is %T, want *net.TCPConn", tlsConn.NetConn())
	}
	if err := tcp.SetReadBuffer(4096); err != nil {
		t.Fatal(err)
	}
	if code, line := first.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("first GROUP: %q", line)
	}
	// The daemon's only child is the first client's session process.
	var firstPid string
	if haveProc {
		kids := linuxProcChildren(t, p.Pid())
		if len(kids) != 1 {
			t.Fatalf("daemon children before the stall = %v, want the one session", kids)
		}
		firstPid = kids[0]
	}
	stallStart := time.Now()
	if code, line := first.Cmd("XOVER 1-40000"); code != 224 {
		t.Fatalf("first XOVER: %q", line)
	}
	// Stop consuming the multi-megabyte TLS reply until the command timeout
	// fires while NewsCache is writing it.
	time.Sleep(time.Duration(f.e.ClientTimeout+1) * time.Second)

	second, _ := robustTLSSession(t, f.addr, f.ca)
	second.Deadline = 12 * time.Second
	started := time.Now()
	if code, line := second.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("second GROUP blocked behind stalled TLS writer: %q", line)
	}
	if code, line := second.Cmd("ARTICLE 39999"); code != 220 {
		t.Fatalf("second ARTICLE while first TLS writer timed out: %q", line)
	}
	second.ReadDotLines()
	secondBound := time.Duration(f.e.ClientTimeout-1) * time.Second
	if elapsed := time.Since(started); elapsed > secondBound {
		t.Errorf("second client waited %v for the group lock, want under %v (well before another ClientTimeout)", elapsed, secondBound)
	}
	// The session must end about one ClientTimeout after the stall began
	// (the send timeout, then a second for the wait for the socket). A
	// failed write used to leave its data in the buffer, and every later
	// flush waited for the timeout again: about three ClientTimeouts.
	// Needs no log file: the session process is gone from the daemon's
	// children (the daemon reaps it).
	endBound := time.Duration(f.e.ClientTimeout+3) * time.Second
	if haveProc {
		for {
			gone := true
			for _, k := range linuxProcChildren(t, p.Pid()) {
				if k == firstPid {
					gone = false
				}
			}
			if gone {
				break
			}
			if time.Since(stallStart) > endBound {
				t.Errorf("stalled TLS writer's session %s still running %v after the stall began, want it gone within %v", firstPid, time.Since(stallStart).Round(time.Millisecond), endBound)
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !haveLog {
		return
	}
	// Releasing the group lock and logging the writer's exit are separate
	// steps. The second client can finish before the first session logs.
	deadline := time.Now().Add(time.Duration(f.e.ClientTimeout+3) * time.Second)
	for {
		log := f.e.logFile()
		if strings.Contains(log, "can't be written to, session ended") {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("stalled TLS writer was not logged as ended after its write timed out:\n%s", log)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestClientTLSHandshakeBoundedWhenClientTimeoutIsZero(t *testing.T) {
	// The 2-second dribble test pins the absolute-deadline code path. This
	// case only checks that ClientTimeout=0 is finite; it does not assert the
	// exact 30-second fallback in tls_handshake_timeout(), which is reviewed
	// in the implementation.
	f := newRobustTLSFixture(t)
	f.e.ClientTimeout = 0
	f.e.standalone()

	conn, err := net.DialTimeout("tcp", f.addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hello := robustClientHello12()
	if _, err := conn.Write(hello[:len(hello)/2]); err != nil {
		t.Fatal(err)
	}
	data := readUntilPeerCloses(t, conn, 40*time.Second)
	if hasPlainNNTPStatus(data) {
		t.Fatalf("partial ClientHello received a plaintext NNTP reply: %q", data)
	}
}

func TestClientTLSDribblingClientHelloUsesAbsoluteDeadline(t *testing.T) {
	const clientTimeout = 2
	f := newRobustTLSFixture(t)
	f.e.ClientTimeout = clientTimeout
	f.e.standalone()

	conn, err := net.DialTimeout("tcp", f.addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(7 * time.Second)); err != nil {
		t.Fatal(err)
	}
	hello := robustClientHello12()
	if _, err := conn.Write(hello[:1]); err != nil {
		t.Fatal(err)
	}
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i := 1; i < len(hello); i++ {
			time.Sleep(time.Second)
			if _, err := conn.Write(hello[i : i+1]); err != nil {
				return
			}
		}
	}()
	started := time.Now()
	deadline := time.Duration(clientTimeout+2) * time.Second
	data := readUntilPeerCloses(t, conn, deadline+time.Second)
	elapsed := time.Since(started)
	_ = conn.Close() // unblock the slow writer if a broken server kept the socket open
	<-writerDone
	if elapsed < 500*time.Millisecond {
		t.Fatalf("dribbling ClientHello was closed after only %v, before the handshake deadline", elapsed)
	}
	if elapsed > deadline {
		t.Fatalf("dribbling ClientHello stayed open for %v, want close within %v", elapsed, deadline)
	}
	if hasPlainNNTPStatus(data) {
		t.Fatalf("dribbling ClientHello received a plaintext NNTP reply: %q", data)
	}
}

type robustInjectConn struct {
	net.Conn
	mu       sync.Mutex
	readData bytes.Buffer
	injected bool
	tail     []byte
}

func (c *robustInjectConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		_, _ = c.readData.Write(p[:n])
		c.mu.Unlock()
	}
	return n, err
}

func (c *robustInjectConn) Write(p []byte) (int, error) {
	originalLen := len(p)
	c.mu.Lock()
	if !c.injected {
		c.injected = true
		combined := append(append([]byte(nil), p...), c.tail...)
		p = combined
	}
	c.mu.Unlock()
	written := 0
	for written < len(p) {
		n, err := c.Conn.Write(p[written:])
		written += n
		if err != nil {
			if written > originalLen {
				written = originalLen
			}
			return written, err
		}
		if n == 0 {
			return min(written, originalLen), io.ErrShortWrite
		}
	}
	return originalLen, nil
}

func (c *robustInjectConn) received() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.readData.Bytes()...)
}

func TestClientTLSDoesNotParsePlaintextAppendedToClientHello(t *testing.T) {
	f := newRobustTLSFixture(t)
	authFile := filepath.Join(f.e.Dir, "injection.auth")
	if err := os.WriteFile(authFile, []byte("root:"+md5Hash+":*:*:*:read,post\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.e.AccessList = `AccessList {
  Client 127.0.0.0/8 {
    allow authentication
    List !*
    Read !*
    PostTo !*
    AuthRequiresTLS yes
    Authentication file:` + authFile + `
  }
  Default {
    allow none
  }
}`
	requireFileLog(f.e)
	f.e.standalone()
	// A normal handshake first proves the listener is ready. The injected
	// bytes must not become an NNTP command before the TLS handshake finishes.
	ready, _ := robustTLSSession(t, f.addr, f.ca)
	ready.Cmd("QUIT")

	raw, err := net.DialTimeout("tcp", f.addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	wrapped := &robustInjectConn{
		Conn: raw,
		tail: []byte("AUTHINFO USER root\r\nAUTHINFO PASS x\r\n"),
	}
	tlsConn := tls.Client(wrapped, robustTLSClientConfig(f.ca, false))
	_ = tlsConn.SetDeadline(time.Now().Add(8 * time.Second))
	err = tlsConn.Handshake()
	if hasPlainNNTPStatus(wrapped.received()) {
		t.Fatalf("server sent a plaintext NNTP status before completing TLS: %q", wrapped.received())
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("TLS handshake with injected plaintext did not finish or fail promptly: %v", err)
	}
	if err == nil {
		c := client.New(t, tlsConn)
		if greeting, err := c.ReadGreeting(); err == nil {
			if !strings.HasPrefix(greeting, "200 ") && !strings.HasPrefix(greeting, "201 ") {
				t.Fatalf("TLS greeting after injected bytes = %q; want 200 or 201", greeting)
			}
			if err := tlsConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if line, err := c.ReadLine(); err == nil {
				t.Fatalf("injected pre-handshake AUTHINFO received an NNTP response: %q", line)
			} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatalf("TLS session remained open after the injected non-TLS bytes: %v", err)
			}
			data := readUntilPeerCloses(t, raw, time.Second)
			if hasPlainNNTPStatus(data) {
				t.Fatalf("server sent a plaintext NNTP status after the TLS greeting: %q", data)
			}
		} else {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatalf("TLS session did not send a greeting or close after the injected bytes: %v", err)
			}
			data := readUntilPeerCloses(t, raw, time.Second)
			if hasPlainNNTPStatus(data) {
				t.Fatalf("server sent a plaintext NNTP status after failing to greet: %q", data)
			}
		}
	} else {
		data := readUntilPeerCloses(t, raw, time.Second)
		if hasPlainNNTPStatus(data) {
			t.Fatalf("server sent a plaintext NNTP status after rejecting the TLS handshake: %q", data)
		}
	}
	if hasPlainNNTPStatus(wrapped.received()) {
		t.Fatalf("server sent a plaintext NNTP status after the TLS handshake: %q", wrapped.received())
	}
	log := strings.ToLower(f.e.logFile())
	if strings.Contains(log, "authinfo") {
		t.Fatalf("pre-handshake AUTHINFO bytes appeared in the server log:\n%s", log)
	}
}

func TestClientTLSMaxConnectionsClosesWithoutPlaintext(t *testing.T) {
	f := newRobustTLSFixture(t)
	f.e.MaxConnections = 2
	f.e.ClientTimeout = 0
	p := f.e.standalone()
	if runtime.GOOS == "linux" {
		waitForNoLinuxChildren(t, p.Pid(), 3*time.Second)
	} else {
		time.Sleep(300 * time.Millisecond) // let the readiness probe's child exit
	}

	_, first := robustTLSSession(t, f.addr, f.ca)
	defer first.Close()
	_, second := robustTLSSession(t, f.addr, f.ca)
	defer second.Close()
	third, err := net.DialTimeout("tcp", f.addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	data := readUntilPeerCloses(t, third, 3*time.Second)
	if len(data) != 0 {
		t.Fatalf("full TLS listener sent %d bytes before closing, want a clean zero-byte close: %x", len(data), data)
	}
}

func TestClientTLSAndPlainListenersDoNotAcceptEachOthersProtocol(t *testing.T) {
	f := newRobustTLSFixture(t)
	f.e.ClientTimeout = 2
	f.e.standalone()

	plainOnTLS, err := net.DialTimeout("tcp", f.addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(plainOnTLS, "GROUP test.alpha\r\n"); err != nil {
		plainOnTLS.Close()
		t.Fatal(err)
	}
	plainReply := readUntilPeerCloses(t, plainOnTLS, 5*time.Second)
	plainOnTLS.Close()
	if hasPlainNNTPStatus(plainReply) {
		t.Fatalf("plaintext GROUP on TLS listener received an NNTP reply: %q", plainReply)
	}

	plainAddr, err := net.DialTimeout("tcp", f.e.Listen, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer plainAddr.Close()
	tlsClient := tls.Client(plainAddr, robustTLSClientConfig(f.ca, false))
	if err := tlsClient.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := tlsClient.Handshake(); err == nil {
		t.Fatal("TLS ClientHello unexpectedly succeeded on the plain listener")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("TLS ClientHello to plain listener took %v to fail; want prompt rejection", elapsed)
	}
}

func linuxProcFDCount(t *testing.T, pid int) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(pid), "fd"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func linuxProcChildren(t *testing.T, pid int) []string {
	t.Helper()
	path := filepath.Join("/proc", strconv.Itoa(pid), "task", strconv.Itoa(pid), "children")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func waitForNoLinuxChildren(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(linuxProcChildren(t, pid)) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("daemon still has child processes after %v: %v", timeout, linuxProcChildren(t, pid))
}

func TestClientTLSHandshakeAbortCyclesDoNotLeakFDsOrChildren(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("checks /proc for child and fd leaks")
	}
	f := newRobustTLSFixture(t)
	f.e.ClientTimeout = 0
	f.e.MaxConnections = 32
	p := f.e.standalone()
	waitForNoLinuxChildren(t, p.Pid(), 3*time.Second)
	before := linuxProcFDCount(t, p.Pid())
	hello := robustClientHello12()
	for i := 0; i < 500; i++ {
		conn, err := net.DialTimeout("tcp", f.addr, 2*time.Second)
		if err != nil {
			t.Fatalf("cycle %d dial: %v", i, err)
		}
		if _, err := conn.Write(hello[:8]); err != nil {
			conn.Close()
			t.Fatalf("cycle %d partial ClientHello: %v", i, err)
		}
		conn.Close()
		time.Sleep(10 * time.Millisecond) // let each aborted-handshake child exit before the next burst
	}
	waitForNoLinuxChildren(t, p.Pid(), 10*time.Second)
	time.Sleep(500 * time.Millisecond) // allow the accept loop to drain its backlog
	waitForNoLinuxChildren(t, p.Pid(), 3*time.Second)
	after := linuxProcFDCount(t, p.Pid())
	if after != before {
		t.Errorf("daemon fd count changed after 500 aborted handshakes: %d -> %d", before, after)
	}
	if children := linuxProcChildren(t, p.Pid()); len(children) != 0 {
		t.Errorf("daemon has child processes/zombies after aborted handshakes: %v", children)
	}
}

func addIPv6LoopbackAccess(e *env) {
	e.AccessList = strings.Replace(DefaultAccessList, "  Default {", `  Client ::1/128 {
    allow read post
    List *
    Read *
    PostTo *
    Authentication none
  }
  Default {`, 1)
}

func TestClientTLSIPv6Listeners(t *testing.T) {
	ipv6Probe, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	ipv6Probe.Close()

	t.Run("dual stack", func(t *testing.T) {
		f := newRobustTLSFixture(t)
		addIPv6LoopbackAccess(f.e)
		_, port, err := net.SplitHostPort(f.addr)
		if err != nil {
			t.Fatal(err)
		}
		f.e.Extra = ""
		robustTLSConfig(f.e, "DEFAULT:"+port, f.files, "")
		f.e.standalone()
		for _, addr := range []string{net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("::1", port)} {
			c, _ := robustTLSSession(t, addr, f.ca)
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Errorf("TLS GROUP from %s: %q, want 211", addr, line)
			}
		}
	})

	t.Run("IPv6 only", func(t *testing.T) {
		f := newRobustTLSFixture(t)
		addIPv6LoopbackAccess(f.e)
		probe, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			t.Skipf("no IPv6 loopback: %v", err)
		}
		_, port, err := net.SplitHostPort(probe.Addr().String())
		probe.Close()
		if err != nil {
			t.Fatal(err)
		}
		f.e.Extra = ""
		addr := net.JoinHostPort("::1", port)
		robustTLSConfig(f.e, addr, f.files, "")
		f.e.standalone()
		c, _ := robustTLSSession(t, addr, f.ca)
		if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
			t.Fatalf("IPv6-only TLS GROUP: %q, want 211", line)
		}
	})
}

func TestClientTLSExpiredCertificateWarnsButServes(t *testing.T) {
	f := newRobustTLSFixture(t)
	expired := robustTLSMaterial(t, f.e, f.ca, "expired",
		time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
	f.e.Extra = ""
	robustTLSConfig(f.e, f.addr, expired, "")
	requireFileLog(f.e)
	f.e.standalone()

	tlsConn, err := robustTLSDial(f.addr, f.ca, robustTLSClientConfig(f.ca, true))
	if err != nil {
		t.Fatalf("TLS listener did not serve with expired leaf: %v", err)
	}
	leaf, err := x509.ParseCertificate(tlsConn.ConnectionState().PeerCertificates[0].Raw)
	if err != nil {
		t.Fatal(err)
	}
	if !leaf.NotAfter.Before(time.Now()) {
		t.Fatalf("test certificate is not expired: NotAfter=%s", leaf.NotAfter)
	}
	tlsConn.Close()

	log := f.e.logFile()
	warning := expired.certPath + " has expired"
	if n := strings.Count(log, warning); n != 1 {
		t.Errorf("expired-certificate warning appears %d times, want once:\n%s", n, log)
	}
	if !strings.Contains(log, warning) {
		t.Errorf("log lacks an expiry warning:\n%s", log)
	}
}

func TestClientTLSWorldReadableKeyWarnsButLoginWorks(t *testing.T) {
	f := newRobustTLSFixture(t)
	if err := os.Chmod(f.files.keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(f.e.Dir, "key-warning.auth")
	if err := os.WriteFile(authFile, []byte("tester:"+md5Hash+":*:*:*:read,post\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.e.AccessList = `AccessList {
  Client 127.0.0.0/8 {
    allow authentication
    List !*
    Read !*
    PostTo !*
    AuthRequiresTLS yes
    Authentication file:` + authFile + `
  }
  Default {
    allow none
  }
}`
	requireFileLog(f.e)
	f.e.standalone()

	c, _ := robustTLSSession(t, f.addr, f.ca)
	login(t, c)
	log := f.e.logFile()
	if n := strings.Count(log, f.files.keyPath); n != 1 {
		t.Errorf("world-readable-key warning names the path %d times, want once:\n%s", n, log)
	}
	if !strings.Contains(log, "TLSKeyFile "+f.files.keyPath+" is readable by every user") {
		t.Errorf("log lacks the world-readable-key warning:\n%s", log)
	}
}

func TestClientTLSFailedSIGHUPReloadKeepsPreviousCertificate(t *testing.T) {
	f := newRobustTLSFixture(t)
	requireFileLog(f.e)
	p := f.e.standalone()
	initialSerial := robustTLSCertSerial(t, f)

	if err := os.WriteFile(f.files.keyPath, []byte("not a PEM private key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("SIGHUP: %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(f.e.logFile(), "the certificate loaded before stays") {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(f.e.logFile(), "the certificate loaded before stays") {
		t.Fatalf("failed TLS reload was not logged:\n%s", f.e.logFile())
	}
	if got := robustTLSCertSerial(t, f); got != initialSerial {
		t.Fatalf("certificate serial after failed reload = %s, want previous serial %s", got, initialSerial)
	}
}

func TestClientTLSAuthRequiresTLSRejectionsDoNotCountAsFailures(t *testing.T) {
	f := newRobustTLSFixture(t)
	authFile := filepath.Join(f.e.Dir, "tls-required.auth")
	if err := os.WriteFile(authFile, []byte("tester:"+md5Hash+":*:*:*:read,post\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.e.AccessList = `AccessList {
  Client 127.0.0.0/8 {
    allow authentication
    List !*
    Read !*
    PostTo !*
    AuthRequiresTLS yes
    Authentication file:` + authFile + `
  }
  Default {
    allow none
  }
}`
	f.e.Extra += "\nAuthFailureDelay 1"
	f.e.standalone()

	plain := f.e.dial()
	defer plain.Close()
	plain.Deadline = 5 * time.Second
	started := time.Now()
	for i := 1; i <= 3; i++ {
		if code, line := plain.Cmd("AUTHINFO USER tester"); code != 483 {
			t.Fatalf("plain AUTHINFO USER attempt %d: %q, want 483", i, line)
		}
		if code, line := plain.Cmd("AUTHINFO PASS secret"); code != 483 {
			t.Fatalf("plain AUTHINFO PASS attempt %d: %q, want 483", i, line)
		}
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("three TLS-required 483 replies took %v, want no auth-failure delay", elapsed)
	}

	secure, _ := robustTLSSession(t, f.addr, f.ca)
	secure.Deadline = 5 * time.Second
	login(t, secure)
}

func TestClientTLSPost(t *testing.T) {
	f := newRobustTLSFixture(t)
	f.e.standalone()
	c, _ := robustTLSSession(t, f.addr, f.ca)
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("TLS POST: %q, want 340", line)
	}
	article := "Newsgroups: test.alpha\r\n" +
		"From: tls-client@newscache.test\r\n" +
		"Subject: posted over implicit TLS\r\n" +
		"Message-ID: <tls-post@newscache.test>\r\n\r\n" +
		"posted through TLS\r\n.\r\n"
	if err := c.Send(article); err != nil {
		t.Fatal(err)
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 240 {
		t.Fatalf("TLS POST completion: %q, %v; want 240", line, err)
	}
	if posted := f.e.Store.Posted(); len(posted) != 1 {
		t.Fatalf("TLS POST sent %d articles upstream, want 1", len(posted))
	}
}

func TestClientTLSClosesAfterThreeFailedLogins(t *testing.T) {
	f := newRobustTLSFixture(t)
	authFile := filepath.Join(f.e.Dir, "tls-failures.auth")
	if err := os.WriteFile(authFile, []byte("tester:"+md5Hash+":*:*:*:read,post\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.e.AccessList = `AccessList {
  Client 127.0.0.0/8 {
    allow authentication
    List !*
    Read !*
    PostTo !*
    Authentication file:` + authFile + `
  }
  Default {
    allow none
  }
}`
	f.e.Extra += "\nAuthFailureDelay 0"
	f.e.standalone()

	c, conn := robustTLSSession(t, f.addr, f.ca)
	defer conn.Close()
	c.Deadline = 5 * time.Second
	for i := 1; i <= 3; i++ {
		if code, line := c.Cmd("AUTHINFO USER tester"); code != 381 {
			t.Fatalf("TLS AUTHINFO USER attempt %d: %q, want 381", i, line)
		}
		if code, line := c.Cmd("AUTHINFO PASS wrong%d", i); code != 481 {
			t.Fatalf("TLS AUTHINFO PASS attempt %d: %q, want 481", i, line)
		}
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 400 {
		t.Fatalf("after three failed TLS logins: %q, %v; want 400", line, err)
	}
	if line, err := c.ReadLine(); err == nil {
		t.Fatalf("after the TLS 400: %q, want the connection closed", line)
	}
}

func TestClientTLSAuthRequiresTLSWithoutTLSListenerWarns(t *testing.T) {
	e := newEnv(t)
	authFile := filepath.Join(e.Dir, "no-listener.auth")
	if err := os.WriteFile(authFile, []byte("tester:"+md5Hash+":*:*:*:read,post\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.AccessList = `AccessList {
  Client 127.0.0.0/8 {
    allow authentication
    List !*
    Read !*
    PostTo !*
    AuthRequiresTLS yes
    Authentication file:` + authFile + `
  }
  Default {
    allow none
  }
}`
	requireFileLog(e)
	e.standalone()
	log := e.logFile()
	if !strings.Contains(log, "AuthRequiresTLS, but no TLS for clients") {
		t.Fatalf("log lacks the warning for AuthRequiresTLS without a TLS listener:\n%s", log)
	}
}
