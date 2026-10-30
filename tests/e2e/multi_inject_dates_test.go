package e2e

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A proto-article offered to multiple injecting agents must have the same
// Message-ID, Date, and Injection-Date in every copy (RFC 5537 3.4.1-2).
func TestMultiInjectorDates(t *testing.T) {
	const suppliedDate = "Tue, 02 Jan 2024 03:04:05 +0000"
	const suppliedInjection = "Wed, 03 Jan 2024 04:05:06 +0000"
	const suppliedID = "<multi-dates@client.test>"
	for _, tc := range []struct {
		name, groups, extra, date, injection, id string
		multi                                    bool
	}{
		{"multi-generated", "test.alpha,test.beta", "", "", "", "", true},
		{"multi-date", "test.alpha,test.beta", "Date: " + suppliedDate + "\r\n", suppliedDate, "", "", true},
		{"multi-date-and-id", "test.alpha,test.beta", "Date: " + suppliedDate + "\r\nMessage-ID: " + suppliedID + "\r\n", suppliedDate, "", suppliedID, true},
		{"multi-injection-date", "test.alpha,test.beta", "Injection-Date: " + suppliedInjection + "\r\n", "", suppliedInjection, "", true},
		{"multi-all-supplied", "test.alpha,test.beta", "Date: " + suppliedDate + "\r\nInjection-Date: " + suppliedInjection + "\r\nMessage-ID: " + suppliedID + "\r\n", suppliedDate, suppliedInjection, suppliedID, true},
		{"single-no-dates", "test.alpha", "", "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, b := twoServers(t, "")
			c := e.session()
			if code, line := c.Cmd("POST"); code != 340 {
				t.Fatalf("POST: %q", line)
			}
			before := time.Now()
			if err := c.Send("Newsgroups: " + tc.groups + "\r\nFrom: poster@client.test\r\nSubject: crosspost\r\n" + tc.extra + "\r\nbody\r\n.\r\n"); err != nil {
				t.Fatal(err)
			}
			code, line, err := c.ReadStatus()
			after := time.Now()
			if err != nil || code != 240 {
				t.Fatalf("POST result: %q %v", line, err)
			}
			first, second := e.Upstream.Articles(), b.Articles()
			if len(first) != 1 || len(second) != boolInt(tc.multi) {
				t.Fatalf("posted articles: upstream A=%d B=%d, want 1 and %d", len(first), len(second), boolInt(tc.multi))
			}
			if !tc.multi {
				for _, name := range []string{"Date", "Injection-Date"} {
					if got := multiPostFields(first[0], name); len(got) != 0 {
						t.Errorf("single-server post unexpectedly adds %s: %q", name, got)
					}
				}
				return
			}
			got := make(map[string]string)
			for _, check := range []struct{ name, supplied string }{{"Message-ID", tc.id}, {"Date", tc.date}, {"Injection-Date", tc.injection}} {
				a, other := multiPostFields(first[0], check.name), multiPostFields(second[0], check.name)
				if len(a) != 1 || len(other) != 1 || a[0] == "" || a[0] != other[0] {
					t.Errorf("%s differs or is absent: A=%q B=%q", check.name, a, other)
					continue
				}
				got[check.name] = a[0]
				if check.supplied != "" && a[0] != check.supplied {
					t.Errorf("%s = %q, want unchanged %q", check.name, a[0], check.supplied)
				}
				if check.name != "Message-ID" && check.supplied == "" {
					stamp, err := time.Parse(time.RFC1123Z, a[0])
					if err != nil || stamp.Before(before.Add(-2*time.Second)) || stamp.After(after.Add(2*time.Second)) {
						t.Errorf("generated %s %q is not a current RFC 5322 date: %v", check.name, a[0], err)
					}
				}
			}
			if tc.date == "" && tc.injection == "" && got["Date"] != got["Injection-Date"] {
				t.Errorf("generated dates differ: %q and %q", got["Date"], got["Injection-Date"])
			}
		})
	}
}

func multiPostFields(article []string, name string) []string {
	var result []string
	for _, line := range article {
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(key, name) {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

// One injector is offline initially. A later updatenews retry must send it
// the same identity and dates that were sent to the online injector.
func TestMultiInjectorSpooledDates(t *testing.T) {
	e, b := twoServers(t, "")
	e.ServerOptions = "offline"
	c := e.session()
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("POST: %q", line)
	}
	if err := c.Send("Newsgroups: test.alpha,test.beta\r\nFrom: poster@client.test\r\nSubject: delayed crosspost\r\n\r\nbody\r\n.\r\n"); err != nil {
		t.Fatal(err)
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 240 {
		t.Fatalf("POST result: %q %v", line, err)
	}
	if posts := b.Articles(); len(posts) != 1 || len(e.Upstream.Articles()) != 0 {
		t.Fatalf("initially posted: A=%d B=%d", len(e.Upstream.Articles()), len(posts))
	}
	if queued := spooledIn(t, e, ".artSpool"); len(queued) != 1 {
		t.Fatalf("spooled posts: %q, want one", queued)
	}
	c.Cmd("QUIT")
	// Distinguish a preserved timestamp from one newly generated on retry.
	time.Sleep(1100 * time.Millisecond)
	e.ServerOptions = ""
	e.writeConf()
	if out, err := exec.Command(updatenewsBin(t, e), "-c", e.Conf).CombinedOutput(); err != nil {
		t.Fatalf("updatenews: %v\n%s", err, out)
	}
	if queued := spooledIn(t, e, ".artSpool"); len(queued) != 0 {
		t.Errorf("post remains spooled after retry: %q", queued)
	}
	first, second := e.Upstream.Articles(), b.Articles()
	if len(first) != 1 || len(second) < 1 {
		t.Fatalf("after retry: A=%d B=%d", len(first), len(second))
	}
	for _, name := range []string{"Message-ID", "Date", "Injection-Date"} {
		a, original := multiPostFields(first[0], name), multiPostFields(second[0], name)
		if len(a) != 1 || len(original) != 1 || a[0] == "" || a[0] != original[0] {
			t.Errorf("%s after retry: A=%q B original=%q", name, a, original)
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
