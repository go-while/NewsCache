package e2e

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-while/nntp-testkit/proc"
)

// reloadLog waits for the requested reload and returns only its log lines.
func reloadLog(t *testing.T, e *env, p *proc.Process) string {
	t.Helper()
	path := filepath.Join(e.LogDir, "newscache.log")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil && len(b) >= len(before) {
			log := string(b[len(before):])
			if strings.Contains(log, "reread "+e.Conf) {
				return log
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for SIGHUP reload")
	return ""
}

func replaceConfigLine(t *testing.T, conf, key, value string) string {
	t.Helper()
	lines := strings.Split(conf, "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, key+" ") {
			if found {
				t.Fatalf("duplicate %s in test config", key)
			}
			lines[i], found = key+" "+value, true
		}
	}
	if !found {
		t.Fatalf("%s not in test config", key)
	}
	return strings.Join(lines, "\n")
}

func TestSIGHUPRestartNotices(t *testing.T) {
	e := newEnv(t)
	e.Extra = "CachePort #19001"
	e.addArticles("test.alpha", 1)
	requireFileLog(e)
	p := e.standalone()
	original, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	changed := string(original)
	for _, c := range [][2]string{
		{"Username", "restart-test-user"}, {"Groupname", "restart-test-group"},
		{"PidFile", filepath.Join(e.Dir, "new.pid")},
		{"LogDirectory", filepath.Join(e.Dir, "new-log")},
		{"ServerType", "inetd"}, {"ListenTo", freeAddr(t)},
		{"CachePort", "#19002"}, {"Hostname", "after-reload.test"},
		{"NiceServer", "3"},
	} {
		changed = replaceConfigLine(t, changed, c[0], c[1])
	}
	if err := os.WriteFile(e.Conf, []byte(changed), 0644); err != nil {
		t.Fatal(err)
	}
	keys := []string{"Username", "Groupname", "PidFile", "LogDirectory", "ServerType", "ListenTo", "CachePort", "Hostname", "NiceServer"}
	for i := 0; i < 2; i++ {
		log := reloadLog(t, e, p)
		for _, key := range keys {
			if !strings.Contains(log, key+" changed (running: ") {
				t.Errorf("reload %d missing %s restart notice:\n%s", i+1, key, log)
			}
		}
	}
	// A new session still uses the startup Hostname when generating an ID.
	c := e.dial()
	if code, line := c.Cmd("POST"); code != 340 {
		t.Fatalf("POST: %q", line)
	}
	if err := c.Send("Newsgroups: test.alpha\r\nFrom: client@newscache.test\r\nSubject: reload\r\n\r\nhello\r\n.\r\n"); err != nil {
		t.Fatal(err)
	}
	if code, line, err := c.ReadStatus(); err != nil || code != 240 {
		t.Fatalf("POST result: %q %v", line, err)
	}
	posted := e.Store.Posted()
	if len(posted) != 1 {
		t.Fatalf("upstream received %d posts", len(posted))
	}
	mid := posted[0].MessageID
	for _, h := range posted[0].Headers {
		if strings.HasPrefix(strings.ToLower(h), "message-id:") {
			mid = strings.TrimSpace(h[len("message-id:"):])
		}
	}
	if !strings.HasSuffix(mid, "@newscache.test>") {
		t.Errorf("generated Message-ID after reload: %q", mid)
	}
	c.Cmd("QUIT")
	if err := os.WriteFile(e.Conf, original, 0644); err != nil {
		t.Fatal(err)
	}
	log := reloadLog(t, e, p)
	for _, key := range keys {
		if strings.Contains(log, key+" changed (running: ") {
			t.Errorf("reverted %s still has restart notice:\n%s", key, log)
		}
	}
}

func TestSIGHUPRestartNoticeListenToSet(t *testing.T) {
	e := newEnv(t)
	e.NoListenTo = true
	_, port, err := net.SplitHostPort(e.Listen)
	if err != nil {
		t.Fatal(err)
	}
	e.Extra = "CachePort #" + port
	requireFileLog(e)
	p := e.standalone()
	e.NoListenTo = false
	e.ListenTo = "DEFAULT" // same value as the old default, but now explicit
	e.writeConf()
	log := reloadLog(t, e, p)
	if !strings.Contains(log, "ListenTo changed (running: (not set), config: DEFAULT)") {
		t.Errorf("missing ListenTo restart notice for an explicit ListenTo DEFAULT:\n%s", log)
	}
	c := e.dial()
	c.Cmd("QUIT")
}

func TestSIGHUPRestartNoticeTLSListenTo(t *testing.T) {
	f := newRobustTLSFixture(t)
	requireFileLog(f.e)
	p := f.e.standalone()
	newAddr := freeAddr(t)
	for newAddr == f.addr || newAddr == f.e.Listen {
		newAddr = freeAddr(t)
	}
	f.e.Extra = strings.Replace(f.e.Extra, "TLSListenTo "+f.addr, "TLSListenTo "+newAddr, 1)
	f.e.writeConf()
	for i := 0; i < 2; i++ {
		log := reloadLog(t, f.e, p)
		if !strings.Contains(log, "TLSListenTo changed (running: "+f.addr+", config: "+newAddr+")") {
			t.Errorf("reload %d missing TLSListenTo restart notice:\n%s", i+1, log)
		}
	}
	c, conn := robustTLSSession(t, f.addr, f.ca)
	defer conn.Close()
	c.Cmd("QUIT")
}

func TestSIGHUPNiceServerRestartOnly(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("session PID discovery uses Linux /proc")
	}
	e := newEnv(t)
	requireFileLog(e)
	p := e.standalone()
	pidBytes, err := os.ReadFile(filepath.Join(e.Dir, "newscache.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid := strings.TrimSpace(string(pidBytes)) // this test's own daemon
	childrenPath := "/proc/" + pid + "/task/" + pid + "/children"
	nice := func(pid string) int {
		t.Helper()
		b, err := os.ReadFile("/proc/" + pid + "/stat")
		if err != nil {
			t.Fatal(err)
		}
		end := strings.LastIndexByte(string(b), ')')
		if end < 0 {
			t.Fatalf("bad /proc stat for %s", pid)
		}
		fields := strings.Fields(string(b[end+1:]))
		if len(fields) < 17 {
			t.Fatalf("short /proc stat for %s", pid)
		}
		n, err := strconv.Atoi(fields[16]) // nice is field 19
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	masterNice := nice(pid)
	b, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	conf := replaceConfigLine(t, string(b), "NiceServer", "3")
	if err := os.WriteFile(e.Conf, []byte(conf), 0644); err != nil {
		t.Fatal(err)
	}
	log := reloadLog(t, e, p)
	if !strings.Contains(log, "NiceServer changed (running: 0, config: 3)") {
		t.Errorf("missing NiceServer restart notice:\n%s", log)
	}
	oldChildren, err := os.ReadFile(childrenPath)
	if err != nil {
		t.Fatal(err)
	}
	old := make(map[string]bool)
	for _, child := range strings.Fields(string(oldChildren)) {
		old[child] = true
	}
	c := e.dial()
	defer c.Close()
	var session string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(childrenPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, child := range strings.Fields(string(b)) {
			if !old[child] {
				session = child
			}
		}
		if session != "" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if session == "" {
		t.Fatal("could not identify the new session child")
	}
	if got := nice(session); got != masterNice {
		t.Errorf("new session nice = %d, want startup master nice %d", got, masterNice)
	}
	c.Cmd("QUIT")
}

// TestSIGHUPDefaultHostname: without a Hostname line the reload does not
// look up the host's name (Hostname is restart-only), so it compares "not
// set" with "not set" and gives no notice; adding a Hostname gives one.
func TestSIGHUPDefaultHostname(t *testing.T) {
	e := newEnv(t)
	requireFileLog(e)
	e.writeConf()
	conf, err := os.ReadFile(e.Conf)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, l := range strings.Split(string(conf), "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == "Hostname" {
			continue
		}
		kept = append(kept, l)
	}
	noHostname := strings.Join(kept, "\n")
	if err := os.WriteFile(e.Conf, []byte(noHostname), 0644); err != nil {
		t.Fatal(err)
	}
	p := proc.StartTCP(t, e.procOpts("-n"), e.Listen)

	if log := reloadLog(t, e, p); strings.Contains(log, "Hostname changed") {
		t.Errorf("reload without Hostname gave a notice:\n%s", log)
	}
	withHostname := strings.Replace(noHostname, "ServerType standalone\n",
		"ServerType standalone\nHostname after-reload.test\n", 1)
	if withHostname == noHostname {
		t.Fatal("no ServerType line in the test config")
	}
	if err := os.WriteFile(e.Conf, []byte(withHostname), 0644); err != nil {
		t.Fatal(err)
	}
	want := "Hostname changed (running: (not set), config: after-reload.test)"
	if log := reloadLog(t, e, p); !strings.Contains(log, want) {
		t.Errorf("reload with a new Hostname: want %q in:\n%s", want, log)
	}
}
