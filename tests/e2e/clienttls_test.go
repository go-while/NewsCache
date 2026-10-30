package e2e

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/client"
)

type clientTLSFiles struct {
	cert   string
	key    string
	serial string
}

type clientTLSFixture struct {
	e     *env
	ca    *testCA
	files clientTLSFiles
	addr  string
}

// clientTLSBuilt uses the configure options the binary reports, rather than
// depending on a GnuTLS symbol surviving compiler/linker changes.
func clientTLSBuilt(t *testing.T, bin string) bool {
	t.Helper()
	b, err := exec.Command(bin, "-o").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -o: %v\n%s", err, b)
	}
	for _, option := range strings.Fields(string(b)) {
		if option == "--with-gnutls" {
			return true
		}
	}
	return false
}

func requireClientTLS(t *testing.T, bin string) {
	t.Helper()
	if !clientTLSBuilt(t, bin) {
		t.Skip("newscache built without GnuTLS")
	}
}

func makeClientTLSFiles(t *testing.T, e *env, ca *testCA, name string) clientTLSFiles {
	t.Helper()
	cert := ca.serverCert(t, []string{"localhost"}, validFrom, validUntil)
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	key, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("server certificate has private key type %T", cert.PrivateKey)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	files := clientTLSFiles{
		cert:   filepath.Join(e.Dir, name+"-cert.pem"),
		key:    filepath.Join(e.Dir, name+"-key.pem"),
		serial: leaf.SerialNumber.String(),
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(files.cert, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files.key, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return files
}

func addClientTLSConfig(e *env, addr string, files clientTLSFiles, priority string) {
	var lines []string
	if addr != "" {
		lines = append(lines, "TLSListenTo "+addr)
	}
	if files.cert != "" {
		lines = append(lines, "TLSCertFile "+files.cert)
	}
	if files.key != "" {
		lines = append(lines, "TLSKeyFile "+files.key)
	}
	if priority != "" {
		lines = append(lines, "TLSPriority "+priority)
	}
	if e.Extra != "" {
		e.Extra = strings.TrimRight(e.Extra, "\n") + "\n"
	}
	e.Extra += strings.Join(lines, "\n")
}

func newClientTLSFixture(t *testing.T) *clientTLSFixture {
	t.Helper()
	e := newEnv(t)
	requireClientTLS(t, e.Bin)
	e.addArticles("test.alpha", 2)
	ca := newTestCA(t, e.Dir)
	files := makeClientTLSFiles(t, e, ca, "server")
	// never the plain listener's port: freeAddr can hand out the same
	// port twice, and the TLS client then talked to the plain listener
	addr := freeAddr(t)
	for addr == e.Listen {
		addr = freeAddr(t)
	}
	addClientTLSConfig(e, addr, files, "")
	return &clientTLSFixture{e: e, ca: ca, files: files, addr: addr}
}

func clientTLSConfig(ca *testCA, config *tls.Config) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	if config == nil {
		config = &tls.Config{}
	} else {
		config = config.Clone()
	}
	config.RootCAs = pool
	if config.ServerName == "" {
		config.ServerName = "localhost"
	}
	return config
}

func dialClientTLS(addr string, ca *testCA, config *tls.Config) (*tls.Conn, error) {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, clientTLSConfig(ca, config))
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func clientTLSSession(t *testing.T, addr string, ca *testCA) (*client.Conn, *tls.Conn) {
	t.Helper()
	tlsConn, err := dialClientTLS(addr, ca, nil)
	if err != nil {
		t.Fatalf("TLS dial %s: %v", addr, err)
	}
	c := client.New(t, tlsConn)
	if greeting, err := c.ReadGreeting(); err != nil || !strings.HasPrefix(greeting, "200 ") {
		t.Fatalf("TLS greeting = %q, %v; want 200", greeting, err)
	}
	return c, tlsConn
}

func TestClientTLSListenerAndCommands(t *testing.T) {
	f := newClientTLSFixture(t)
	f.e.standalone()

	// ListenTo remains a plain NNTP listener while TLSListenTo adds a second
	// listener to the same daemon.
	plain := f.e.dial()
	if code, line := plain.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("plain GROUP: %q, want 211", line)
	}

	tlsClient, _ := clientTLSSession(t, f.addr, f.ca)
	if code, line := tlsClient.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("TLS GROUP: %q, want 211", line)
	}
	if code, line := tlsClient.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("TLS ARTICLE 1: %q, want 220", line)
	}
	article := tlsClient.ReadDotLines()
	if !strings.Contains(strings.Join(article, "\n"), "body of article 1") {
		t.Errorf("TLS ARTICLE body missing: %q", article)
	}
}

func TestClientTLSDefaultPort563(t *testing.T) {
	f := newClientTLSFixture(t)
	f.e.Extra = ""
	addClientTLSConfig(f.e, "127.0.0.1", f.files, "")
	f.e.writeConf()
	out, err := exec.Command(f.e.Bin, "-c", f.e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -p with default TLS port: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "\nTLSListenTo 127.0.0.1\n") {
		t.Fatalf("newscache -p did not preserve the TLS listener with its omitted default port:\n%s", out)
	}
	// The configuration assertion above runs without privilege. Keep a live
	// handshake as an extra where the runner can bind the privileged default.
	probe, err := net.Listen("tcp", "127.0.0.1:563")
	if err != nil {
		t.Logf("live default-port handshake unavailable: %v", err)
		return
	}
	probe.Close()
	f.e.standalone()
	c, _ := clientTLSSession(t, "127.0.0.1:563", f.ca)
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("GROUP through the default TLS port: %q, want 211", line)
	}
}

func TestClientTLSRequiresEncryptionForAuthentication(t *testing.T) {
	f := newClientTLSFixture(t)
	authFile := filepath.Join(f.e.Dir, "newscache.auth")
	if err := os.WriteFile(authFile, []byte("tester:"+md5Hash+":*:*:*:read,post\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.e.AccessList = `AccessList {
  Client 127.0.0.0/8 {
    allow read post
    List *
    Read *
    PostTo *
    AuthRequiresTLS yes
    Authentication file:` + authFile + `
  }
  Default {
    allow none
  }
}`
	f.e.standalone()

	plain := f.e.dial()
	checkCaps(t, "plain", capabilities(t, plain), nil, []string{"AUTHINFO USER"})
	for _, command := range []string{"AUTHINFO USER tester", "AUTHINFO PASS secret"} {
		if code, line := plain.Cmd("%s", command); code != 483 {
			t.Errorf("plain %s: %q, want 483", command, line)
		}
	}

	secure, _ := clientTLSSession(t, f.addr, f.ca)
	checkCaps(t, "TLS", capabilities(t, secure), []string{"AUTHINFO USER"}, nil)
	login(t, secure)
	if code, line := secure.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("TLS GROUP after login: %q, want 211", line)
	}
}

func TestClientTLSRejectsBadConfiguration(t *testing.T) {
	t.Run("listener requires credentials", func(t *testing.T) {
		e := newEnv(t)
		requireClientTLS(t, e.Bin)
		addClientTLSConfig(e, freeAddr(t), clientTLSFiles{}, "")
		e.writeConf()
		out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
		if err == nil {
			t.Fatalf("newscache -p accepted TLSListenTo without a certificate and key:\n%s", out)
		}
		if !strings.Contains(string(out), "TLSListenTo needs TLSCertFile and TLSKeyFile") {
			t.Errorf("unexpected failure for missing TLS credentials:\n%s", out)
		}
	})

	t.Run("certificate and key must match", func(t *testing.T) {
		f := newClientTLSFixture(t)
		other := makeClientTLSFiles(t, f.e, f.ca, "other")
		f.e.Extra = ""
		addClientTLSConfig(f.e, f.addr, clientTLSFiles{cert: f.files.cert, key: other.key}, "")
		f.e.writeConf()
		out, err := exec.Command(f.e.Bin, "-c", f.e.Conf, "-p").CombinedOutput()
		if err == nil {
			t.Fatalf("newscache -p accepted a mismatched TLS certificate and key:\n%s", out)
		}
		if !strings.Contains(string(out), "can't load TLSCertFile") || !strings.Contains(string(out), "TLSKeyFile") {
			t.Errorf("unexpected failure for mismatched TLS credentials:\n%s", out)
		}
	})

	t.Run("invalid priority is rejected", func(t *testing.T) {
		f := newClientTLSFixture(t)
		f.e.Extra = ""
		addClientTLSConfig(f.e, f.addr, f.files, "NOT-A-GNUTLS-PRIORITY")
		f.e.writeConf()
		out, err := exec.Command(f.e.Bin, "-c", f.e.Conf, "-p").CombinedOutput()
		if err == nil {
			t.Fatalf("newscache -p accepted an invalid TLSPriority:\n%s", out)
		}
		if !strings.Contains(string(out), "invalid TLSPriority") {
			t.Errorf("unexpected failure for invalid TLS priority:\n%s", out)
		}
	})
}

func TestClientTLSOptionsWithoutGnuTLS(t *testing.T) {
	e := newEnv(t)
	if clientTLSBuilt(t, e.Bin) {
		t.Skip("newscache was built with GnuTLS")
	}
	ca := newTestCA(t, e.Dir)
	files := makeClientTLSFiles(t, e, ca, "server")
	addClientTLSConfig(e, freeAddr(t), files, "")
	e.writeConf()
	out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
	if err == nil {
		t.Fatalf("newscache -p silently accepted client TLS options without GnuTLS:\n%s", out)
	}
}

func expectTLSHandshakeClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(conn)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("TLS handshake connection was not closed within the limit; read %d bytes", len(data))
	}
	if len(data) >= 4 && data[0] >= '0' && data[0] <= '9' &&
		data[1] >= '0' && data[1] <= '9' && data[2] >= '0' &&
		data[2] <= '9' && data[3] == ' ' {
		t.Errorf("server sent a plaintext NNTP reply before TLS completed: %q", data)
	}
}

func TestClientTLSHandshakeFailuresDoNotBlockPlainClients(t *testing.T) {
	f := newClientTLSFixture(t)
	f.e.ClientTimeout = 2
	f.e.standalone()

	stalled, err := net.DialTimeout("tcp", f.addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close() // connect, but never send a ClientHello

	plain := f.e.dial()
	if code, line := plain.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("plain client while TLS handshake is stalled: %q, want 211", line)
	}

	// An invalid pre-handshake payload is closed without a plaintext NNTP reply.
	garbage, err := net.DialTimeout("tcp", f.addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer garbage.Close()
	if _, err := io.WriteString(garbage, "GET / HTTP/1.0\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	expectTLSHandshakeClosed(t, garbage)

	// The silent peer is also dropped after ClientTimeout rather than keeping a
	// child/session indefinitely.
	expectTLSHandshakeClosed(t, stalled)
}

func TestClientTLSIdleSessionTimesOut(t *testing.T) {
	f := newClientTLSFixture(t)
	f.e.ClientTimeout = 2
	f.e.standalone()
	c, _ := clientTLSSession(t, f.addr, f.ca)
	line, err := c.ReadLine()
	if err != nil {
		t.Fatalf("reading timeout reply: %v", err)
	}
	if !strings.HasPrefix(line, "400 ") {
		t.Fatalf("idle TLS session reply %q, want 400 timeout", line)
	}
}

func TestClientTLSSIGHUPReloadsCertificate(t *testing.T) {
	f := newClientTLSFixture(t)
	p := f.e.standalone()
	initial, err := dialClientTLS(f.addr, f.ca, nil)
	if err != nil {
		t.Fatalf("initial TLS handshake: %v", err)
	}
	if got := initial.ConnectionState().PeerCertificates[0].SerialNumber.String(); got != f.files.serial {
		t.Fatalf("initial certificate serial = %s, want %s", got, f.files.serial)
	}
	initial.Close()

	updated := makeClientTLSFiles(t, f.e, f.ca, "updated")
	certPEM, err := os.ReadFile(updated.cert)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(updated.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.files.cert, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.files.key, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("SIGHUP: %v", err)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := dialClientTLS(f.addr, f.ca, nil)
		if err == nil {
			serial := conn.ConnectionState().PeerCertificates[0].SerialNumber.String()
			conn.Close()
			if serial == updated.serial {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("new TLS connections did not see reloaded certificate serial %s", updated.serial)
}

func TestClientTLSPriorityTLS13(t *testing.T) {
	f := newClientTLSFixture(t)
	f.e.Extra = ""
	addClientTLSConfig(f.e, f.addr, f.files, "NORMAL:-VERS-ALL:+VERS-TLS1.3")
	f.e.standalone()

	conn, err := dialClientTLS(f.addr, f.ca, &tls.Config{
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
	})
	if err != nil {
		t.Fatalf("TLS 1.3 handshake: %v", err)
	}
	if got := conn.ConnectionState().Version; got != tls.VersionTLS13 {
		t.Errorf("negotiated TLS version = %#x, want TLS 1.3", got)
	}
	conn.Close()

	oldClient, err := dialClientTLS(f.addr, f.ca, &tls.Config{
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS12,
	})
	if err == nil {
		oldClient.Close()
		t.Fatal("TLS 1.2-only client connected despite TLS 1.3-only priority")
	}
}

func TestClientTLSInetdFlag(t *testing.T) {
	f := newClientTLSFixture(t)
	f.e.NoListenTo = true
	f.e.Extra = ""
	addClientTLSConfig(f.e, "", f.files, "")
	f.e.writeConf()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	clientSide, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	serverSide, err := listener.Accept()
	listener.Close()
	if err != nil {
		clientSide.Close()
		t.Fatal(err)
	}
	file, err := serverSide.(*net.TCPConn).File()
	if err != nil {
		clientSide.Close()
		serverSide.Close()
		t.Fatal(err)
	}

	cmd := exec.Command(f.e.Bin, "-c", f.e.Conf, "-t")
	cmd.Stdin, cmd.Stdout = file, file
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		file.Close()
		clientSide.Close()
		serverSide.Close()
		t.Fatalf("start newscache -t: %v", err)
	}
	file.Close()
	serverSide.Close()
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		clientSide.Close()
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("newscache -t stderr: %s", stderr.String())
		}
	})

	tlsClient := tls.Client(clientSide, clientTLSConfig(f.ca, nil))
	if err := tlsClient.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("inetd -t TLS handshake: %v; stderr: %s", err, stderr.String())
	}
	c := client.New(t, tlsClient)
	if greeting, err := c.ReadGreeting(); err != nil || !strings.HasPrefix(greeting, "200 ") {
		t.Fatalf("inetd -t greeting = %q, %v; want 200", greeting, err)
	}
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Fatalf("inetd -t GROUP: %q, want 211", line)
	}
	if code, line := c.Cmd("QUIT"); code != 205 {
		t.Fatalf("inetd -t QUIT: %q, want 205", line)
	}
	select {
	case <-done:
		if waitErr != nil {
			t.Fatalf("newscache -t exited: %v; stderr: %s", waitErr, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("newscache -t did not exit after QUIT; stderr: %s", stderr.String())
	}
}

// TestClientTLSListenerOnTheSamePort: TLSListenTo on the port of ListenTo
// is a startup error. On Linux both binds succeed (SO_REUSEADDR) and only
// listen() fails; its result wasn't checked, so the daemon ran with the
// plain listener alone and a TLS client got plain text.
func TestClientTLSListenerOnTheSamePort(t *testing.T) {
	e := newEnv(t)
	requireClientTLS(t, e.Bin)
	ca := newTestCA(t, e.Dir)
	files := makeClientTLSFiles(t, e, ca, "server")
	addClientTLSConfig(e, e.Listen, files, "")
	e.writeConf()
	cmd := exec.Command(e.Bin, "-c", e.Conf, "-n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("newscache exited 0 with TLSListenTo = ListenTo %s", e.Listen)
		}
		if !strings.Contains(stderr.String(), "TLSListenTo") {
			t.Errorf("stderr doesn't name TLSListenTo: %q", stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("newscache kept running with TLSListenTo = ListenTo %s; stderr: %q",
			e.Listen, stderr.String())
	}
}
