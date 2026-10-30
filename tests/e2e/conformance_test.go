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
		})
		for _, result := range report.Results {
			if result.Status != conformance.Skip {
				continue
			}
			if strings.HasPrefix(result.Name, "authinfo-") && result.Detail == "no Options.User" {
				continue // exercised by the authentication fixture below
			}
			if result.Detail == "STREAMING not advertised" || result.Detail == "IHAVE not advertised" {
				continue // NewsCache is a reader, not an RFC 4644 transit server
			}
			t.Errorf("unexpected skipped reader check %s: %s", result.Name, result.Detail)
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
		if authChecks != 6 {
			t.Errorf("ran %d authentication checks, want 6", authChecks)
		}
	})
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
