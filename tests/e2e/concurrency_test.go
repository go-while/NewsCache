package e2e

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit"
	"github.com/go-while/nntp-testkit/client"
	"github.com/go-while/nntp-testkit/fakeserver"
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

// upstreamLine counts the lines the fake upstream received that are line
// (case-insensitive), e.g. "article 1" or "over 2-30".
func upstreamLine(e *env, line string) int {
	n := 0
	for _, l := range e.Upstream.Lines() {
		if strings.EqualFold(strings.TrimSpace(l), line) {
			n++
		}
	}
	return n
}

// asyncReply is a multi-line command's reply read in a goroutine (Cmd
// would call t.Fatal there).
type asyncReply struct {
	code  int
	line  string
	lines []string
	err   error
	at    time.Time
}

func sendAsync(c *client.Conn, cmd string, ok int) <-chan asyncReply {
	ch := make(chan asyncReply, 1)
	go func() {
		var r asyncReply
		r.code, r.line, r.err = c.TryCmd("%s", cmd)
		if r.err == nil && r.code == ok {
			r.lines, r.err = c.TryReadDotLines()
		}
		r.at = time.Now()
		ch <- r
	}()
	return ch
}

func waitReply(t *testing.T, what string, ch <-chan asyncReply, ok, records int) asyncReply {
	t.Helper()
	select {
	case r := <-ch:
		if r.err != nil || r.code != ok || (records >= 0 && len(r.lines) != records) {
			t.Fatalf("%s: code %d %q, %d lines, err %v; want %d with %d lines",
				what, r.code, r.line, len(r.lines), r.err, ok, records)
		}
		return r
	case <-time.After(60 * time.Second):
		t.Fatalf("%s: no reply within 60 s", what)
	}
	return asyncReply{}
}

// TestFreshenNoGroupLockWhileFetching: a session's XOVER on a chunk the
// spool hasn't synchronized asks the news server for it. While it waits
// for a slow news server, another session that reads an article the
// spool holds gets it at once. The overview fetch used to hold the
// group's exclusive lock (GROUP, LISTGROUP and OVER upstream all under
// it), so every session that read the group waited for the news server:
// up to 2 minutes per chunk.
func TestFreshenNoGroupLockWhileFetching(t *testing.T) {
	const group = "test.alpha"
	e := newEnv(t)
	e.addArticles(group, 30)
	// 33 lines (status, 29 records, ".") at 200 ms: OVER takes ~6.5 s
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		LineDelay: map[string]time.Duration{"OVER": 200 * time.Millisecond},
	}})

	// article 1 into the spool (no overview fetch: ARTICLE doesn't sync)
	sp, a := e.sessionProcess()
	if code, line := a.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("seed GROUP: %q", line)
	}
	if code, line := a.Cmd("ARTICLE 1"); code != 220 {
		t.Fatalf("seed ARTICLE 1: %q", line)
	}
	a.ReadDotLines()
	quitSession(t, sp, a)
	if n := e.Upstream.Count("OVER"); n != 0 {
		t.Fatalf("seeding sent %d OVER upstream, want 0", n)
	}

	// The reader is up (a session takes about a second to start) and in
	// the group before the slow fetch begins.
	b := e.session()
	b.Deadline = 10 * time.Second
	if code, line := b.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("reader GROUP: %q", line)
	}
	r := e.session()
	if code, line := r.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("fetcher GROUP: %q", line)
	}
	over := sendAsync(r, "XOVER 1-30", 224)
	waitFor(t, "the news server receives the OVER", func() bool {
		return upstreamLine(e, "over 2-30") == 1
	})
	t.Logf("upstream so far: %q", e.Upstream.Lines())

	start := time.Now()
	code, line, err := b.TryCmd("ARTICLE 1")
	elapsed := time.Since(start)
	var art []string
	if err == nil && code == 220 {
		art, err = b.TryReadDotLines()
	}
	fetching := len(over) == 0
	t.Logf("the reader's ARTICLE 1 took %v (the OVER still running: %v)",
		elapsed.Round(time.Millisecond), fetching)
	if err != nil || code != 220 || len(art) == 0 {
		t.Fatalf("reader ARTICLE 1: code %d %q, %d lines, err %v", code, line, len(art), err)
	}
	// (a reader held up for the whole fetch is answered only after it)
	if elapsed > 1500*time.Millisecond {
		t.Errorf("a session reading a stored article waited %v for another session's overview fetch",
			elapsed.Round(time.Millisecond))
	} else if !fetching {
		t.Fatalf("the OVER ended before the reader's ARTICLE was answered: nothing measured")
	}
	if n := upstreamLine(e, "article 1"); n != 1 {
		t.Errorf("the news server got ARTICLE 1 %d times, want once (the reader's copy comes from the spool)", n)
	}
	rep := waitReply(t, "fetcher XOVER 1-30", over, 224, 30)
	t.Logf("the XOVER ended %v after the reader's answer", rep.at.Sub(start.Add(elapsed)).Round(time.Millisecond))
	if n := e.Upstream.Count("OVER"); n != 1 {
		t.Errorf("%d OVER upstream, want 1", n)
	}
}

// TestFreshenOneFetchPerChunk: two sessions that want the same chunk at
// the same time fetch it once. The second one waits for the first one's
// fetch (the chunk's fetch lock, not the group's lock) and then serves
// what it stored.
func TestFreshenOneFetchPerChunk(t *testing.T) {
	const group = "test.alpha"
	e := newEnv(t)
	e.GroupTimeout = 300
	e.addArticles(group, 30)
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		LineDelay: map[string]time.Duration{"OVER": 100 * time.Millisecond},
	}})
	r1 := e.session()
	r2 := e.session()
	for _, c := range []*client.Conn{r1, r2} {
		if code, line := c.Cmd("GROUP %s", group); code != 211 {
			t.Fatalf("GROUP: %q", line)
		}
	}
	first := sendAsync(r1, "XOVER 1-30", 224)
	waitFor(t, "the first session's OVER reaches the news server", func() bool {
		return e.Upstream.Count("OVER") == 1
	})
	second := sendAsync(r2, "XOVER 1-30", 224)
	a := waitReply(t, "first XOVER", first, 224, 30)
	b := waitReply(t, "second XOVER", second, 224, 30)
	for i := range a.lines {
		if a.lines[i] != b.lines[i] {
			t.Errorf("record %d differs: %q and %q", i+1, a.lines[i], b.lines[i])
		}
	}
	t.Logf("upstream: %q", e.Upstream.Lines())
	if n := e.Upstream.Count("OVER"); n != 1 {
		t.Errorf("the chunk was fetched with %d OVER, want 1", n)
	}
	if n := e.Upstream.Count("LISTGROUP"); n != 1 {
		t.Errorf("%d LISTGROUP upstream, want 1", n)
	}
}

// TestFreshenSyncedChunkNoFetch: a session that finds its chunk
// synchronized asks the news server nothing, and doesn't wait for
// another session that fetches another chunk of the same group.
func TestFreshenSyncedChunkNoFetch(t *testing.T) {
	const group = "test.alpha"
	e := newEnv(t)
	e.GroupTimeout = 300
	// two chunks: 1-9999 and 10000-19999
	e.addArticles(group, 10030)
	upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
		LineDelay: map[string]time.Duration{"OVER": 200 * time.Millisecond},
	}})
	sp, s := e.sessionProcess()
	if code, line := s.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("seed GROUP: %q", line)
	}
	if code, line := s.Cmd("XOVER 1-5"); code != 224 {
		t.Fatalf("seed XOVER: %q", line)
	}
	if got := s.ReadDotLines(); len(got) != 5 {
		t.Fatalf("seed XOVER: %d records, want 5", len(got))
	}
	quitSession(t, sp, s)

	v := e.session()
	v.Deadline = 10 * time.Second
	if code, line := v.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("reader GROUP: %q", line)
	}
	r := e.session()
	if code, line := r.Cmd("GROUP %s", group); code != 211 {
		t.Fatalf("fetcher GROUP: %q", line)
	}
	over := sendAsync(r, "XOVER 10001-10030", 224)
	waitFor(t, "the news server receives the second chunk's OVER", func() bool {
		return e.Upstream.Count("OVER") == 2
	})
	overs, listgroups := e.Upstream.Count("OVER"), e.Upstream.Count("LISTGROUP")

	start := time.Now()
	code, line, err := v.TryCmd("XOVER 1-5")
	var recs []string
	if err == nil && code == 224 {
		recs, err = v.TryReadDotLines()
	}
	elapsed := time.Since(start)
	fetching := len(over) == 0
	t.Logf("XOVER 1-5 (synchronized) took %v (the other chunk's OVER still running: %v)",
		elapsed.Round(time.Millisecond), fetching)
	if err != nil || code != 224 || len(recs) != 5 {
		t.Fatalf("reader XOVER 1-5: code %d %q, %d records, err %v", code, line, len(recs), err)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("XOVER of a synchronized chunk waited %v for another chunk's fetch",
			elapsed.Round(time.Millisecond))
	} else if !fetching {
		t.Fatalf("the other chunk's OVER ended before the reader's XOVER: nothing measured")
	}
	if n := e.Upstream.Count("OVER"); n != overs {
		t.Errorf("XOVER of a synchronized chunk sent %d OVER upstream", n-overs)
	}
	if n := e.Upstream.Count("LISTGROUP"); n != listgroups {
		t.Errorf("XOVER of a synchronized chunk sent %d LISTGROUP upstream", n-listgroups)
	}
	waitReply(t, "fetcher XOVER 10001-10030", over, 224, 30)
}

// TestPrefetchNoGroupLockWhileFetching: updatenews -g asks the news
// server for the articles without the group's lock, with and without
// -l, so a session that reads an article the spool holds isn't held up
// by a slow news server. The prefetch held the group's lock for each
// chunk (shared, or exclusive with -l) and the exclusive lock around
// each article's fetch.
func TestPrefetchNoGroupLockWhileFetching(t *testing.T) {
	const group = "test.alpha"
	for _, args := range [][]string{{"-g"}, {"-g", "-l"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newEnv(t)
			e.addArticles(group, 5)
			upstreamWith(t, e, fakeserver.Options{Faults: fakeserver.Faults{
				Delay: map[string]time.Duration{"ARTICLE": 1500 * time.Millisecond},
			}})
			prefetch := filepath.Join(e.Dir, "prefetch")
			if err := os.WriteFile(prefetch, []byte(group+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			e.Extra = "PrefetchFile " + prefetch

			sp, a := e.sessionProcess()
			if code, line := a.Cmd("GROUP %s", group); code != 211 {
				t.Fatalf("seed GROUP: %q", line)
			}
			if code, line := a.Cmd("ARTICLE 1"); code != 220 {
				t.Fatalf("seed ARTICLE 1: %q", line)
			}
			a.ReadDotLines()
			quitSession(t, sp, a)

			v := e.session()
			v.Deadline = 10 * time.Second
			if code, line := v.Cmd("GROUP %s", group); code != 211 {
				t.Fatalf("reader GROUP: %q", line)
			}
			cmd := exec.Command(updatenewsBin(t, e), append([]string{"-c", e.Conf}, args...)...)
			var out strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			// done is closed when updatenews has ended, with its result
			// in waitErr
			done := make(chan struct{})
			var waitErr error
			go func() { waitErr = cmd.Wait(); close(done) }()
			t.Cleanup(func() {
				select {
				case <-done:
				default:
					_ = cmd.Process.Kill()
					<-done
				}
			})
			waitFor(t, "updatenews asks for article 2", func() bool {
				return upstreamLine(e, "article 2") == 1
			})

			start := time.Now()
			code, line, err := v.TryCmd("ARTICLE 1")
			var art []string
			if err == nil && code == 220 {
				art, err = v.TryReadDotLines()
			}
			elapsed := time.Since(start)
			running := true
			select {
			case <-done:
				running = false
			default:
			}
			t.Logf("the reader's ARTICLE 1 took %v (updatenews still running: %v); upstream: %q",
				elapsed.Round(time.Millisecond), running, e.Upstream.Lines())
			if err != nil || code != 220 || len(art) == 0 {
				t.Fatalf("reader ARTICLE 1: code %d %q, %d lines, err %v", code, line, len(art), err)
			}
			if elapsed > 1500*time.Millisecond {
				t.Errorf("a session reading a stored article waited %v for updatenews %v",
					elapsed.Round(time.Millisecond), args)
			} else if !running {
				t.Fatalf("updatenews ended before the reader's ARTICLE was answered: nothing measured")
			}
			select {
			case <-done:
				if waitErr != nil {
					t.Fatalf("updatenews %v: %v\n%s", args, waitErr, out.String())
				}
			case <-time.After(60 * time.Second):
				t.Fatal("updatenews did not finish")
			}
			for n := 1; n <= 5; n++ {
				if got := upstreamLine(e, fmt.Sprintf("article %d", n)); got != 1 {
					t.Errorf("article %d asked for %d times, want once", n, got)
				}
			}
			// the prefetched articles are in the spool now
			if code, line := v.Cmd("ARTICLE 5"); code != 220 {
				t.Fatalf("ARTICLE 5 after the prefetch: %q", line)
			}
			v.ReadDotLines()
			if got := upstreamLine(e, "article 5"); got != 1 {
				t.Errorf("ARTICLE 5 after the prefetch went upstream again")
			}
		})
	}
}
