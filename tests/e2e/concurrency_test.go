package e2e

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit"
	"github.com/go-while/nntp-testkit/client"
)

type concurrentFetchGate struct {
	address  string
	entered  <-chan struct{}
	arm      func()
	release  func()
	stop     func()
	problems func() []string
}

// Hold the first armed ARTICLE/OVER status, not a guessed delay. The Store
// still handles and records the real upstream protocol. Fixture data lines
// do not begin with a 220/224/500/501 status prefix.
func newConcurrentFetchGate(t *testing.T, upstream string, status int) *concurrentFetchGate {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered, proceed := make(chan struct{}), make(chan struct{})
	var armed, triggered atomic.Bool
	var releaseOnce, stopOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(proceed) }) }
	var mu sync.Mutex
	var links []net.Conn
	var problems []string
	stopping := false
	note := func(problem string) {
		mu.Lock()
		defer mu.Unlock()
		if !stopping && len(problems) < 16 {
			problems = append(problems, problem)
		}
	}
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			down, err := ln.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer down.Close()
				up, err := net.DialTimeout("tcp", upstream, testkit.Deadline())
				if err != nil {
					note("proxy dial: " + err.Error())
					return
				}
				defer up.Close()
				mu.Lock()
				if stopping {
					mu.Unlock()
					return
				}
				links = append(links, down, up)
				mu.Unlock()
				copied := make(chan struct{})
				go func() {
					_, _ = io.Copy(up, down)
					_ = up.Close()
					close(copied)
				}()
				defer func() {
					_ = down.Close()
					_ = up.Close()
					<-copied
				}()
				r := bufio.NewReader(up)
				for {
					line, readErr := r.ReadString('\n')
					if line != "" {
						if strings.HasPrefix(line, "500 ") || strings.HasPrefix(line, "501 ") {
							note("upstream rejected a command: " + strings.TrimSpace(line))
						}
						if armed.Load() && strings.HasPrefix(line, strconv.Itoa(status)+" ") && triggered.CompareAndSwap(false, true) {
							close(entered)
							<-proceed
						}
						if _, err := io.WriteString(down, line); err != nil {
							return
						}
					}
					if readErr != nil {
						return
					}
				}
			}()
		}
	}()
	stop := func() {
		stopOnce.Do(func() {
			release()
			mu.Lock()
			stopping = true
			connections := append([]net.Conn(nil), links...)
			mu.Unlock()
			_ = ln.Close()
			for _, c := range connections {
				_ = c.Close()
			}
			workers.Wait()
		})
	}
	t.Cleanup(stop)
	return &concurrentFetchGate{
		address: ln.Addr().String(), entered: entered,
		arm: func() { armed.Store(true) }, release: release, stop: stop,
		problems: func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), problems...)
		},
	}
}

func TestConcurrentSpoolFetches(t *testing.T) {
	const group = "test.concurrent"
	for _, tc := range []struct {
		name            string
		large, overview bool
	}{
		{"inline article", false, false},
		{"external article", true, false},
		{"overview refresh", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			for n := int64(1); n <= 3; n++ {
				body := []string{fmt.Sprintf("article %d body", n), ".dot-stuffed", "article tail"}
				if tc.large && n == 1 {
					body = append(bigBody(), ".dot-stuffed", "large article tail")
				}
				e.Store.AddArticle(group, groupArticle(group, n, body))
			}
			command, code, count := "ARTICLE 1", 220, 3
			if tc.overview {
				command, code = "OVER 1-4", 224
			}
			gate := newConcurrentFetchGate(t, e.Upstream.Addr(), code)
			var err error
			e.ServerHost, e.ServerPort, err = net.SplitHostPort(gate.address)
			if err != nil {
				t.Fatal(err)
			}
			verifyOverview := func(lines []string, want int) {
				t.Helper()
				if len(lines) != want {
					t.Fatalf("overview has %d records, want %d", len(lines), want)
				}
				for i, line := range lines {
					a, ok := e.Store.Article(group, int64(i+1))
					if !ok {
						t.Fatalf("missing overview fixture article %d", i+1)
					}
					a = a.WithDefaults(group)
					size := 2 // header/body separator; dot-stuffing is not article data
					for _, header := range a.Headers {
						size += len(header) + 2
					}
					for _, body := range a.Body {
						size += len(body) + 2
					}
					wantLine := strings.Join([]string{strconv.Itoa(i + 1), a.Header("Subject"), a.Header("From"),
						a.Header("Date"), a.MessageID, a.Header("References"), strconv.Itoa(size),
						strconv.Itoa(len(a.Body)), "Xref: " + a.Header("Xref")}, "\t")
					if line != wantLine {
						t.Errorf("overview record %d: %q, want complete ordered fixture data %q", i+1, line, wantLine)
					}
				}
			}
			if tc.overview {
				seed, c := e.sessionProcess()
				if got, line := c.Cmd("GROUP %s", group); got != 211 {
					t.Fatalf("seed GROUP: %q, want 211", line)
				}
				if got, line := c.Cmd("OVER 1-3"); got != 224 {
					t.Fatalf("seed OVER: %q, want 224", line)
				}
				verifyOverview(c.ReadDotLines(), 3)
				quitSession(t, seed, c)
				e.Store.AddArticle(group, groupArticle(group, 4, []string{"new fourth article"}))
				count = 4
				// Required product semantics: GroupTimeout 0 is due only
				// after the next clock second, not immediately.
				time.Sleep(1100 * time.Millisecond)
			}
			p1, c1 := e.sessionProcess()
			p2, c2 := e.sessionProcess()
			for _, c := range []*client.Conn{c1, c2} {
				if got, line := c.Cmd("GROUP %s", group); got != 211 {
					t.Fatalf("GROUP before concurrent fetch: %q, want 211", line)
				}
			}
			if !tc.overview && e.Upstream.Count("ARTICLE") != 0 {
				t.Fatal("article fixture was fetched before the concurrent requests")
			}
			a, ok := e.Store.Article(group, 1)
			if !ok {
				t.Fatal("missing article fixture")
			}
			a = a.WithDefaults(group)
			wantArticle := append(append(append([]string(nil), a.Headers...), ""), a.Body...)
			verify := func(lines []string) {
				t.Helper()
				if tc.overview {
					verifyOverview(lines, count)
					return
				}
				if len(lines) != len(wantArticle) {
					t.Fatalf("ARTICLE has %d lines, want %d", len(lines), len(wantArticle))
				}
				for i := range wantArticle {
					if lines[i] != wantArticle[i] {
						t.Fatalf("ARTICLE line %d: %.100q, want %.100q", i, lines[i], wantArticle[i])
					}
				}
			}
			type reply struct {
				code   int
				status string
				lines  []string
				err    error
			}
			replies := make(chan reply, 2)
			read := func(c *client.Conn) {
				r := reply{}
				r.code, r.status, r.err = c.ReadStatus()
				if r.err == nil && r.code == code {
					r.lines, r.err = c.TryReadDotLines()
				}
				replies <- r
			}
			// Release before process/proxy cleanup even on an assertion
			// failure. Both commands are outstanding while response 1 is
			// held; a correct spool lock may serialize their upstream work.
			t.Cleanup(gate.release)
			gate.arm()
			if err := c1.Send(command + "\r\n"); err != nil {
				t.Fatal(err)
			}
			go read(c1)
			select {
			case <-gate.entered:
			case <-time.After(testkit.Deadline()):
				t.Fatal("first fetch never reached the held upstream response")
			}
			if err := c2.Send(command + "\r\n"); err != nil {
				t.Fatal(err)
			}
			go read(c2)
			gate.release()
			for range 2 {
				select {
				case r := <-replies:
					if r.err != nil || r.code != code {
						t.Fatalf("concurrent %s: %q, %v; want %d and complete data", command, r.status, r.err, code)
					}
					verify(r.lines)
				case <-time.After(testkit.Deadline()):
					t.Fatal("concurrent fetch did not finish")
				}
			}
			quitSession(t, p1, c1)
			quitSession(t, p2, c2)
			// A new reader checks the persisted spool, not either writer's
			// in-memory state. Keep this read inside the cache lifetime.
			e.GroupTimeout = 3600
			fetched := e.Upstream.Count("ARTICLE")
			if tc.overview {
				fetched = e.Upstream.Count("OVER") + e.Upstream.Count("XOVER")
			}
			p3, c3 := e.sessionProcess()
			if got, line := c3.Cmd("GROUP %s", group); got != 211 {
				t.Fatalf("persisted spool GROUP: %q, want 211", line)
			}
			if got, line := c3.Cmd("%s", command); got != code {
				t.Fatalf("persisted spool %s: %q, want %d", command, line, code)
			}
			verify(c3.ReadDotLines())
			quitSession(t, p3, c3)
			after := e.Upstream.Count("ARTICLE")
			if tc.overview {
				after = e.Upstream.Count("OVER") + e.Upstream.Count("XOVER")
			}
			if after != fetched {
				t.Errorf("persisted cache fetched %s again: target commands %d -> %d", command, fetched, after)
			}
			if tc.large {
				if _, err := os.Stat(filepath.Join(e.Spool, "test", "concurrent", ".art1")); err != nil {
					t.Errorf("external-article fixture was not stored separately: %v", err)
				}
			}
			if bad, err := badSpoolFiles(e.Spool); err != nil || len(bad) != 0 {
				t.Errorf("spool after concurrent fetches: bad=%v, error=%v", bad, err)
			}
			gate.stop()
			if problems := gate.problems(); len(problems) != 0 {
				t.Errorf("upstream protocol errors: %q", problems)
			}
			for _, line := range e.Upstream.Lines() {
				if len(strings.Fields(line)) == 0 || strings.ContainsAny(line, "\x00\r\n") {
					t.Errorf("garbage in upstream command stream: %q", line)
				}
			}
		})
	}
}
