package e2e

// TLS to the upstream server: NewsCache must verify the certificate (chain,
// name, validity) for "Options ssl" and "Options starttls", accept it only
// from the CA in TLSCAFile or the system trust store, and never fall back to
// plain text when STARTTLS fails. A TLS (or STARTTLS) proxy with a test CA
// sits between NewsCache and the fake upstream.

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/fakeserver"
)

// testCA is a CA with a PEM file NewsCache can use as TLSCAFile.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	file string
}

func newTestCA(t *testing.T, dir string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "NewsCache e2e test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key, file: file}
}

// serverCert returns a certificate for the given DNS names, signed by ca and
// valid from notBefore to notAfter.
func (ca *testCA) serverCert(t *testing.T, names []string, notBefore, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// tlsProxy accepts connections on 127.0.0.1, does TLS (from the start, or
// after STARTTLS) and relays to the fake upstream.
type tlsProxy struct {
	ln       net.Listener
	upstream string
	cfg      *tls.Config
	starttls bool // plain greeting first, TLS after STARTTLS
	refuse   bool // answer STARTTLS with 502, as an attacker in the middle would

	mu        sync.Mutex
	plain     []string // client lines seen in plain text
	handshake int      // successful TLS handshakes
	sni       string   // server name the client sent
}

func newTLSProxy(t *testing.T, upstream string, cert tls.Certificate, starttls, refuse bool) *tlsProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tlsProxy{ln: ln, upstream: upstream, starttls: starttls, refuse: refuse}
	p.cfg = &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			p.mu.Lock()
			p.sni = hello.ServerName
			p.mu.Unlock()
			return nil, nil
		},
	}
	t.Cleanup(func() { ln.Close() })
	go p.serve()
	return p
}

func (p *tlsProxy) port() string {
	_, port, _ := net.SplitHostPort(p.ln.Addr().String())
	return port
}

func (p *tlsProxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(c)
	}
}

func (p *tlsProxy) handle(c net.Conn) {
	defer c.Close()
	up, err := net.Dial("tcp", p.upstream)
	if err != nil {
		return
	}
	defer up.Close()
	if !p.starttls {
		tc := tls.Server(c, p.cfg)
		if tc.Handshake() != nil {
			return
		}
		p.mu.Lock()
		p.handshake++
		p.mu.Unlock()
		relay(tc, up)
		return
	}
	// STARTTLS: relay the plain-text greeting and one-line commands until
	// the client sends STARTTLS.
	cr, ur := bufio.NewReader(c), bufio.NewReader(up)
	line, err := ur.ReadString('\n')
	if err != nil {
		return
	}
	io.WriteString(c, line)
	for {
		cmd, err := cr.ReadString('\n')
		if err != nil {
			return
		}
		p.mu.Lock()
		p.plain = append(p.plain, strings.TrimRight(cmd, "\r\n"))
		p.mu.Unlock()
		if strings.EqualFold(strings.TrimSpace(cmd), "STARTTLS") {
			if p.refuse {
				io.WriteString(c, "502 STARTTLS not available\r\n")
				continue
			}
			io.WriteString(c, "382 Continue with TLS negotiation\r\n")
			tc := tls.Server(c, p.cfg)
			if tc.Handshake() != nil {
				return
			}
			p.mu.Lock()
			p.handshake++
			p.mu.Unlock()
			relay(tc, struct {
				io.Reader
				io.Writer
			}{ur, up})
			return
		}
		io.WriteString(up, cmd)
		resp, err := ur.ReadString('\n')
		if err != nil {
			return
		}
		io.WriteString(c, resp)
	}
}

func relay(a io.ReadWriter, b io.ReadWriter) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
}

func (p *tlsProxy) state() (plain []string, handshakes int, sni string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.plain...), p.handshake, p.sni
}

// tlsEnv points NewsCache at a TLS proxy for the fake upstream at
// "localhost", so the certificate is checked against a DNS name.
func tlsEnv(t *testing.T, names []string, notBefore, notAfter time.Time, starttls, refuse bool) (*env, *testCA, *tlsProxy) {
	t.Helper()
	e := newEnv(t)
	requireTLS(t, e.Bin)
	e.addArticles("test.alpha", 1)
	ca := newTestCA(t, e.Dir)
	p := newTLSProxy(t, e.Upstream.Addr(), ca.serverCert(t, names, notBefore, notAfter), starttls, refuse)
	e.ServerHost = "localhost"
	e.ServerPort = p.port()
	e.ServerOptions = "ssl"
	if starttls {
		e.ServerOptions = "starttls"
	}
	return e, ca, p
}

// requireTLS skips the test for a newscache built without GnuTLS
// (configure --without-gnutls), which ignores Options ssl and starttls.
func requireTLS(t *testing.T, bin string) {
	t.Helper()
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("gnutls_handshake")) {
		t.Skip("newscache built without GnuTLS")
	}
}

var (
	validFrom  = time.Now().Add(-time.Hour)
	validUntil = time.Now().Add(24 * time.Hour)
)

// groupOK reports whether GROUP test.alpha worked through the upstream.
func groupOK(t *testing.T, e *env) bool {
	t.Helper()
	c := e.session()
	code, _ := c.Cmd("GROUP test.alpha")
	return code == 211
}

func TestTLSVerified(t *testing.T) {
	e, ca, p := tlsEnv(t, []string{"localhost"}, validFrom, validUntil, false, false)
	e.ServerLines = "    TLSCAFile " + ca.file
	if !groupOK(t, e) {
		t.Fatal("GROUP failed over TLS with a valid certificate from TLSCAFile")
	}
	if _, _, sni := p.state(); sni != "localhost" {
		t.Errorf("SNI %q, want localhost", sni)
	}
}

func TestTLSUntrustedCA(t *testing.T) {
	// No TLSCAFile: the test CA is not in the system trust store.
	e, _, _ := tlsEnv(t, []string{"localhost"}, validFrom, validUntil, false, false)
	if groupOK(t, e) {
		t.Fatal("GROUP worked with a certificate from an untrusted CA")
	}
	if n := len(e.Upstream.Lines()); n != 0 {
		t.Errorf("upstream received %d lines through an unverified TLS connection", n)
	}
}

func TestTLSWrongName(t *testing.T) {
	e, ca, _ := tlsEnv(t, []string{"other.test"}, validFrom, validUntil, false, false)
	e.ServerLines = "    TLSCAFile " + ca.file
	if groupOK(t, e) {
		t.Fatal("GROUP worked with a certificate for another host name")
	}
}

func TestTLSExpired(t *testing.T) {
	e, ca, _ := tlsEnv(t, []string{"localhost"}, time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour), false, false)
	e.ServerLines = "    TLSCAFile " + ca.file
	if groupOK(t, e) {
		t.Fatal("GROUP worked with an expired certificate")
	}
}

func TestTLSNoVerify(t *testing.T) {
	// Options noverify accepts the untrusted certificate on purpose.
	e, _, _ := tlsEnv(t, []string{"other.test"}, validFrom, validUntil, false, false)
	e.ServerOptions = "ssl noverify"
	if !groupOK(t, e) {
		t.Fatal("GROUP failed with Options ssl noverify")
	}
}

func TestSTARTTLSVerified(t *testing.T) {
	e, ca, p := tlsEnv(t, []string{"localhost"}, validFrom, validUntil, true, false)
	e.ServerLines = "    TLSCAFile " + ca.file
	if !groupOK(t, e) {
		t.Fatal("GROUP failed over STARTTLS with a valid certificate")
	}
	if _, n, _ := p.state(); n == 0 {
		t.Error("no TLS handshake after STARTTLS")
	}
}

func TestSTARTTLSUntrusted(t *testing.T) {
	e, _, _ := tlsEnv(t, []string{"localhost"}, validFrom, validUntil, true, false)
	if groupOK(t, e) {
		t.Fatal("GROUP worked over STARTTLS with a certificate from an untrusted CA")
	}
}

// TestSTARTTLSRefused: when STARTTLS is refused (as an attacker in the middle
// would), NewsCache must disconnect, not send the login in plain text.
func TestSTARTTLSRefused(t *testing.T) {
	e, ca, p := tlsEnv(t, []string{"localhost"}, validFrom, validUntil, true, true)
	e.ServerLines = "    TLSCAFile " + ca.file + "\n    User tester\n    Password secret"
	if groupOK(t, e) {
		t.Fatal("GROUP worked although STARTTLS was refused")
	}
	plain, _, _ := p.state()
	for _, l := range plain {
		u := strings.ToUpper(l)
		if strings.HasPrefix(u, "AUTHINFO") || strings.HasPrefix(u, "GROUP") {
			t.Errorf("sent %q in plain text after STARTTLS was refused", l)
		}
	}
}

// TestTLSOptionsWithoutGnuTLS: a newscache built without GnuTLS can't do
// ssl or starttls, so it must refuse such a server instead of talking (and
// sending the login) in plain text.
func TestTLSOptionsWithoutGnuTLS(t *testing.T) {
	for _, opt := range []string{"ssl", "starttls"} {
		t.Run(opt, func(t *testing.T) {
			e := newEnv(t)
			b, err := os.ReadFile(e.Bin)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(b, []byte("gnutls_handshake")) {
				t.Skip("newscache built with GnuTLS")
			}
			e.addArticles("test.alpha", 1)
			e.ServerOptions = opt
			e.ServerLines = "    User tester\n    Password secret"
			if groupOK(t, e) {
				t.Fatalf("GROUP worked with Options %s in a build without GnuTLS", opt)
			}
			if n := len(e.Upstream.Lines()); n != 0 {
				t.Errorf("upstream received %d plain-text lines: %q", n, e.Upstream.Lines())
			}
		})
	}
}

// TestSTARTTLSNoLoginBeforeTLS: with Options starttls, a 480 to MODE READER
// (the first command, before STARTTLS) must not make NewsCache log in
// there and then: User and Password would go out in plain text, which
// anyone in the middle could force by injecting the 480. The login waits
// for TLS, and MODE READER is sent again after it.
func TestSTARTTLSNoLoginBeforeTLS(t *testing.T) {
	e := newEnv(t)
	requireTLS(t, e.Bin)
	e.addArticles("test.alpha", 1)
	upstreamWith(t, e, fakeserver.Options{
		StoreOpts: fakeserver.StoreOptions{User: "tester", Pass: "secret"},
		Faults: fakeserver.Faults{
			Override:      map[string][]string{"MODE": {"480 Authentication required"}},
			OverrideTimes: map[string]int{"MODE": 1},
		},
	})
	ca := newTestCA(t, e.Dir)
	p := newTLSProxy(t, e.Upstream.Addr(), ca.serverCert(t, []string{"localhost"}, validFrom, validUntil), true, false)
	e.ServerHost = "localhost"
	e.ServerPort = p.port()
	e.ServerOptions = "starttls"
	e.ServerLines = "    TLSCAFile " + ca.file + "\n    User tester\n    Password secret"
	ok := groupOK(t, e)
	plain, handshakes, _ := p.state()
	for _, l := range plain {
		if strings.HasPrefix(strings.ToUpper(l), "AUTHINFO") {
			t.Errorf("sent %q in plain text, before STARTTLS", l)
		}
	}
	if handshakes == 0 {
		t.Error("no TLS handshake")
	}
	if !ok {
		t.Error("GROUP failed: after STARTTLS and the login it must work")
	}
	if n := e.Upstream.Count("MODE"); n < 2 {
		t.Errorf("MODE READER sent %d times, want it again after the login", n)
	}
}

// TestUpstreamQuit: at the end of a session NewsCache sends QUIT to the
// upstream. Over TLS it was dropped: RServer::disconnect writes it into
// the stream without a flush, and the TLS stream sent its close_notify
// without writing what it still held.
func TestUpstreamQuit(t *testing.T) {
	for _, useTLS := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "TLS"}[useTLS], func(t *testing.T) {
			var e *env
			if useTLS {
				var ca *testCA
				e, ca, _ = tlsEnv(t, []string{"localhost"}, validFrom, validUntil, false, false)
				e.ServerLines = "    TLSCAFile " + ca.file
			} else {
				e = newEnv(t)
				e.addArticles("test.alpha", 1)
			}
			c := e.session()
			if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
				t.Fatalf("GROUP test.alpha: %q", line)
			}
			c.Cmd("QUIT")
			deadline := time.Now().Add(10 * time.Second)
			for e.Upstream.Count("QUIT") == 0 {
				if time.Now().After(deadline) {
					t.Fatalf("the upstream got no QUIT")
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}
