package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/conformance"
)

// Exercise the public reader protocol against a real NewsCache daemon backed
// by the in-process Store upstream, not against the fake server itself.
func TestProtocolConformance(t *testing.T) {
	t.Run("reader", func(t *testing.T) {
		e := newEnv(t)
		e.addArticles("test.alpha", 3)
		e.Store.AddGroup("test.empty", "empty group")
		e.standalone()
		report := checkConformance(t, conformance.Options{
			// The Store and group are disposable. Supplying the group
			// lets transfer checks skip on missing capabilities instead
			// of being skipped merely because no target was provided.
			Addr: e.Listen, PostGroup: "test.alpha", Deadline: 10 * time.Second, Workers: 2,
			// NewsCache sends RFC 3977's :bytes and :lines, not the
			// older Bytes: and Lines: the checker accepts by default.
			StrictOverviewFmt: true,
		})
		if pass, _, _ := report.Counts(); pass < 25 {
			t.Errorf("reader conformance ran only %d passing checks, want at least 25", pass)
		}
		for _, result := range report.Results {
			if result.Status != conformance.Skip {
				continue
			}
			if conformanceSkipAllowed(result, report.Capabilities) {
				continue
			}
			t.Errorf("unexpected skipped reader check %s: %s", result.Name, result.Detail)
		}
	})
	t.Run("implicit TLS", func(t *testing.T) {
		// TLSListenTo (RFC 8143): the reader checks over TLS from the
		// first byte, and tls-capabilities (no STARTTLS listed there)
		f := newClientTLSFixture(t) // with two articles in test.alpha
		f.e.Store.AddGroup("test.empty", "empty group")
		f.e.standalone()
		report := checkConformance(t, conformance.Options{
			Addr: f.addr, TLS: clientTLSConfig(f.ca, nil), PostGroup: "test.alpha",
			Deadline: 10 * time.Second, Workers: 2, StrictOverviewFmt: true,
		})
		if pass, _, _ := report.Counts(); pass < 25 {
			t.Errorf("reader conformance over TLS ran only %d passing checks, want at least 25", pass)
		}
		ran := false
		for _, result := range report.Results {
			if result.Name == "tls-capabilities" {
				ran = result.Status == conformance.Pass
			}
			if result.Status == conformance.Skip && !conformanceSkipAllowed(result, report.Capabilities) {
				t.Errorf("unexpected skipped check over TLS %s: %s", result.Name, result.Detail)
			}
		}
		if !ran {
			t.Errorf("tls-capabilities didn't pass over TLS")
		}
	})
	t.Run("authentication", func(t *testing.T) {
		// The helper normally configures an inetd client; the conformance
		// checker connects to the standalone daemon over loopback instead.
		e := loginOnlyEnv(t, "tester:"+md5Hash+":*:*:*:read,post")
		e.AccessList = strings.Replace(e.AccessList, "Client stdin", "Client 127.0.0.0/8", 1)
		e.Extra = "AuthFailureDelay 0"
		e.standalone()
		var skip []string
		for _, name := range conformance.Names() {
			if !strings.HasPrefix(name, "authinfo-") {
				// The first fixture already covers these; this fixture is
				// for RFC 4643 with authentication required.
				skip = append(skip, name)
			}
		}
		report := checkConformance(t, conformance.Options{
			Addr: e.Listen, User: "tester", Pass: "secret", Skip: skip,
			Deadline: 10 * time.Second, Workers: 2,
		})
		authChecks := 0
		for _, result := range report.Results {
			if strings.HasPrefix(result.Name, "authinfo-") {
				authChecks++
				if result.Status != conformance.Pass {
					t.Errorf("authentication check %s: %s (%s)", result.Name, result.Status, result.Detail)
				}
			}
		}
		if authChecks < 6 {
			t.Errorf("ran %d authentication checks, want at least 6", authChecks)
		}
	})
}

// These capabilities are intentionally absent from the reader fixture. An
// auth check skipped here is exercised by the authenticated fixture instead.
func conformanceSkipAllowed(result conformance.Result, capabilities []string) bool {
	if strings.HasPrefix(result.Name, "authinfo-") && result.Detail == "no Options.User" {
		return true
	}
	// the "implicit TLS" fixture runs it over TLS
	if result.Name == "tls-capabilities" && strings.HasPrefix(result.Detail, "no Options.TLS") {
		return true
	}
	// STARTTLS is for plain connections
	if strings.HasPrefix(result.Name, "starttls") && strings.Contains(result.Detail, "TLS from the first byte") {
		return true
	}
	capability := result.Missing
	switch capability {
	// NEWNEWS isn't implemented (REVIEW.md)
	case "STREAMING", "IHAVE", "STARTTLS", "COMPRESS", "OVER MSGID", "NEWNEWS":
	default:
		return false
	}
	want := strings.Fields(capability)
	for _, line := range capabilities {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.EqualFold(fields[0], want[0]) {
			continue
		}
		if len(want) == 1 {
			return false
		}
		for _, arg := range fields[1:] {
			if strings.EqualFold(arg, want[1]) {
				return false
			}
		}
	}
	return true
}

func TestConformanceSkipPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, detail, missing string
		caps                  []string
		allowed               bool
	}{
		{"authinfo-480", "no Options.User", "", nil, true},
		{"greeting", "no Options.User", "", nil, false},
		{"stream", "STREAMING not advertised", "STREAMING", nil, true},
		{"tls", "STARTTLS not advertised", "STARTTLS", nil, true},
		{"compress", "COMPRESS not advertised", "COMPRESS", nil, true},
		{"newnews", "NEWNEWS not advertised", "NEWNEWS", nil, true},
		{"msgid", "OVER MSGID not advertised", "OVER MSGID", []string{"OVER"}, true},
		{"msgid", "OVER MSGID not advertised", "OVER MSGID", []string{"OVER MSGID"}, false},
		{"stream", "STREAMING not advertised", "STREAMING", []string{"STREAMING"}, false},
		{"other", "OTHER not advertised", "OTHER", nil, false},
		{"stream", "temporarily unavailable", "", nil, false},
		{"stream", "STREAMING not advertised", "", nil, false},
		{"tls-capabilities", "no Options.TLS: the connection is plain", "", nil, true},
		{"starttls-twice", "the connection is TLS from the first byte (Options.TLS): STARTTLS is for plain connections", "", nil, true},
		{"greeting", "the connection is TLS from the first byte", "", nil, false},
	} {
		result := conformance.Result{Name: tc.name, Detail: tc.detail, Missing: tc.missing, Status: conformance.Skip}
		if got := conformanceSkipAllowed(result, tc.caps); got != tc.allowed {
			t.Errorf("skip %q (%q, missing %q), capabilities %q: got %v, want %v", tc.name, tc.detail, tc.missing, tc.caps, got, tc.allowed)
		}
	}
}

func checkConformance(t *testing.T, options conformance.Options) conformance.Report {
	t.Helper()
	report, err := conformance.Check(options)
	if err != nil {
		t.Fatalf("conformance: %v", err)
	}
	t.Logf("conformance report:\n%s", report.String())
	for _, result := range report.Failed() {
		t.Errorf("conformance %s (%s): %s", result.Name, result.RFC, result.Detail)
	}
	return report
}
