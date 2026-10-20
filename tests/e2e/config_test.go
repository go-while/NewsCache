package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPrintServerOptions: newscache -p prints every server option it parsed.
// ssl, starttls and dontgenmsgid were parsed but left out of the output.
func TestPrintServerOptions(t *testing.T) {
	e := newEnv(t)
	e.ServerOptions = "setpostflag dontgenmsgid ssl starttls"
	e.writeConf()
	out, err := exec.Command(e.Bin, "-c", e.Conf, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("newscache -p: %v\n%s", err, out)
	}
	var options string
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == "Options" {
			options = l
			break
		}
	}
	for _, want := range []string{"setpostflag", "dontgenmsgid", "ssl", "starttls"} {
		if !strings.Contains(" "+options+" ", " "+want+" ") {
			t.Errorf("newscache -p Options line %q lacks %q", strings.TrimSpace(options), want)
		}
	}
}
