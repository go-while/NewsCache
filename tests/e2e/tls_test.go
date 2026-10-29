package e2e

// TLS to the upstream server: NewsCache must verify the certificate (chain,
// name, validity) for "Options ssl" and "Options starttls", accept it only
// from the CA in TLSCAFile or the system trust store, and never fall back to
// plain text when STARTTLS fails. A TLS (or STARTTLS) proxy with a test CA
// sits between NewsCache and the fake upstream.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	drop     int  // close this many connections right after the TLS handshake (no greeting)

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
		drop := p.handshake <= p.drop
		p.mu.Unlock()
		if drop {
			return
		}
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

// A peer that sends a valid TLS record header but never completes the
// record. Every 150 ms it sends another byte: a per-read timeout alone
// cannot bound the whole handshake.
func dribblingTLSUpstream(t *testing.T, starttls bool) (*fakeserver.Server, <-chan time.Time, *atomic.Int32) {
	t.Helper()
	started := make(chan time.Time, 1)
	var drips atomic.Int32
	s := fakeserver.New(t, fakeserver.Options{
		NoGreeting: !starttls,
		Handler: func(c *fakeserver.Conn) {
			if starttls {
				for {
					line, err := c.ReadLine()
					if err != nil {
						return
					}
					switch strings.ToUpper(strings.TrimSpace(line)) {
					case "MODE READER":
						if c.WriteLines("200 reader ready") != nil {
							return
						}
					case "STARTTLS":
						if c.WriteLines("382 begin TLS") != nil {
							return
						}
						goto handshake
					default:
						return
					}
				}
			}
		handshake:
			raw := c.Raw()
			raw.SetReadDeadline(time.Now().Add(5 * time.Second))
			var header [5]byte
			if _, err := io.ReadFull(raw, header[:]); err != nil || header[0] != 22 {
				return
			}
			n := int(header[3])<<8 | int(header[4])
			if _, err := io.CopyN(io.Discard, raw, int64(n)); err != nil {
				return
			}
			raw.SetReadDeadline(time.Time{})
			// An incomplete 16383-byte handshake record, well within TLS's
			// record limit, so the client keeps waiting rather than rejecting it.
			if _, err := raw.Write([]byte{22, 3, 3, 0x3f, 0xff}); err != nil {
				return
			}
			select {
			case started <- time.Now():
			default:
			}
			tick := time.NewTicker(150 * time.Millisecond)
			defer tick.Stop()
			for range tick.C {
				if _, err := raw.Write([]byte{0}); err != nil {
					return
				}
				drips.Add(1)
			}
		},
	})
	return s, started, &drips
}

func testUpstreamTLSHandshakeBound(t *testing.T, starttls bool, readTimeout, connectTimeout int, signal bool, bound, minimum time.Duration) {
	t.Helper()
	e := newEnv(t)
	requireTLS(t, e.Bin)
	server, started, drips := dribblingTLSUpstream(t, starttls)
	_, port, err := net.SplitHostPort(server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	e.ServerHost, e.ServerPort = "127.0.0.1", port
	e.ServerOptions = "ssl noverify"
	if starttls {
		e.ServerOptions = "starttls noverify"
	}
	e.ServerLines = fmt.Sprintf("    ReadTimeout %d\n    ConnectTimeout %d\n    Retries 1", readTimeout, connectTimeout)
	e.writeConf()
	ctx, cancel := context.WithTimeout(context.Background(), bound+15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, updatenewsBin(t, e), "-c", e.Conf, "-a")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var begin time.Time
	select {
	case begin = <-started:
	case err := <-done:
		t.Fatalf("updatenews exited before the TLS handshake: %v\n%s", err, output.String())
	case <-time.After(10 * time.Second):
		cancel()
		<-done
		t.Fatal("updatenews did not start the TLS handshake")
	}
	if signal {
		time.Sleep(350 * time.Millisecond)
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		begin = time.Now()
	}
	select {
	case err := <-done:
		elapsed := time.Since(begin)
		if elapsed < minimum || elapsed > bound {
			t.Errorf("handshake ended after %v, want %v..%v (err %v, drips %d)\n%s", elapsed, minimum, bound, err, drips.Load(), output.String())
		}
		if !signal && drips.Load() < 4 {
			t.Errorf("peer sent only %d bytes; handshake was not actually dribbling", drips.Load())
		}
	case <-time.After(bound):
		cancel()
		<-done
		t.Fatalf("updatenews did not stop within %v (drips %d)\n%s", bound, drips.Load(), output.String())
	}
}

func TestUpstreamTLSHandshakeDeadline(t *testing.T) {
	for _, starttls := range []bool{false, true} {
		name := "implicit"
		if starttls {
			name = "starttls"
		}
		t.Run(name, func(t *testing.T) {
			testUpstreamTLSHandshakeBound(t, starttls, 2, 8, false, 5*time.Second, time.Second)
		})
	}
}

func TestUpstreamTLSHandshakeSIGTERM(t *testing.T) {
	for _, starttls := range []bool{false, true} {
		name := "implicit"
		if starttls {
			name = "starttls"
		}
		t.Run(name, func(t *testing.T) {
			testUpstreamTLSHandshakeBound(t, starttls, 0, 0, true, 2500*time.Millisecond, 0)
		})
	}
}

// The ordinary client session, as well as updatenews, must stop when its
// upstream handshake is interrupted by SIGTERM.
func TestClientSessionUpstreamTLSSIGTERM(t *testing.T) {
	for _, starttls := range []bool{false, true} {
		name := "implicit"
		if starttls {
			name = "starttls"
		}
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			requireTLS(t, e.Bin)
			server, started, _ := dribblingTLSUpstream(t, starttls)
			_, port, err := net.SplitHostPort(server.Addr())
			if err != nil {
				t.Fatal(err)
			}
			e.ServerHost, e.ServerPort = "127.0.0.1", port
			e.ServerOptions = "ssl noverify"
			if starttls {
				e.ServerOptions = "starttls noverify"
			}
			e.ServerLines = "    ReadTimeout 0\n    Retries 1"
			e.writeConf()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, e.Bin, "-c", e.Conf, "-i")
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			greeting, err := bufio.NewReader(output).ReadString('\n')
			if err != nil || !strings.HasPrefix(greeting, "200 ") {
				cancel()
				<-done
				t.Fatalf("client greeting %q: %v", greeting, err)
			}
			if _, err := io.WriteString(input, "GROUP test.alpha\r\n"); err != nil {
				cancel()
				<-done
				t.Fatal(err)
			}
			select {
			case <-started:
			case err := <-done:
				t.Fatalf("client session exited before TLS handshake: %v\n%s", err, stderr.String())
			case <-time.After(10 * time.Second):
				cancel()
				<-done
				t.Fatal("client session did not start the upstream TLS handshake")
			}
			time.Sleep(350 * time.Millisecond)
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				cancel()
				<-done
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(2500 * time.Millisecond):
				cancel()
				<-done
				t.Fatalf("client session did not stop promptly on SIGTERM\n%s", stderr.String())
			}
		})
	}
}

// When ReadTimeout is zero, ConnectTimeout bounds the whole handshake.
func TestUpstreamTLSHandshakeZeroTimeout(t *testing.T) {
	for _, starttls := range []bool{false, true} {
		name := "implicit"
		if starttls {
			name = "starttls"
		}
		t.Run(name, func(t *testing.T) {
			testUpstreamTLSHandshakeBound(t, starttls, 0, 2, false, 5*time.Second, time.Second)
		})
	}
}

// With neither configured timeout there is still a 60-second hard limit.
// Opt in for the slow boundary test; normal CI covers the 2-second fallback.
func TestUpstreamTLSHandshakeBothTimeoutsZero(t *testing.T) {
	if os.Getenv("NEWSCACHE_SLOW_TLS_DEADLINE") != "1" {
		t.Skip("set NEWSCACHE_SLOW_TLS_DEADLINE=1 for the 60-second fallback test")
	}
	for _, starttls := range []bool{false, true} {
		name := "implicit"
		if starttls {
			name = "starttls"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testUpstreamTLSHandshakeBound(t, starttls, 0, 0, false, 68*time.Second, 55*time.Second)
		})
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

// TestTLSConnectRetry: the server closes the connection after the TLS
// handshake, before the greeting (an overloaded server), and answers the
// next one. connect() tried again with the same stream: with Options ssl
// its GnuTLS session, which had done its handshake on the old connection,
// so every retry failed.
func TestTLSConnectRetry(t *testing.T) {
	e, ca, p := tlsEnv(t, []string{"localhost"}, validFrom, validUntil, false, false)
	p.mu.Lock()
	p.drop = 1
	p.mu.Unlock()
	e.ServerLines = "    TLSCAFile " + ca.file + "\n    Retries 2"
	c := e.session()
	c.Deadline = 20 * time.Second
	if code, line := c.Cmd("GROUP test.alpha"); code != 211 {
		t.Errorf("GROUP test.alpha after a connection closed before the greeting: %q, want 211", line)
	}
	if _, n, _ := p.state(); n != 2 {
		t.Errorf("%d TLS handshakes, want 2", n)
	}
}
