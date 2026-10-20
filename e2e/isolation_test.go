//go:build e2e

package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// inetSocket matches a socket(2) or connect(2) call on an IPv4 or IPv6
// socket in strace's output.
var inetSocket = regexp.MustCompile(`(socket|connect)\(.*AF_INET6?\b`)

// TestOrdinaryCommandsUseNoNetworkAndLeaveNoProcess checks two design rules:
// bt makes no network requests, and neither bt nor its drivers outlive a
// command. It traces every process each command starts, drivers included.
func TestOrdinaryCommandsUseNoNetworkAndLeaveNoProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("traces system calls with strace")
	}
	strace, err := exec.LookPath("strace")
	if err != nil {
		t.Skip("needs strace")
	}
	p := startPeer(t, "sensor:Betty Sensor")
	name := p.device(0).Name
	// Install both official drivers, so that every command also runs them.
	env := []string{"BETTY_DRIVER_DIR=" + t.TempDir()}
	for _, driver := range []string{"bt-driver-sony", "bt-driver-apple-audio"} {
		if r := bt(t, env, "driver", "install", filepath.Join(binDir, executable(driver))); r.code != 0 {
			t.Fatalf("install %s: %s", driver, r.stderr)
		}
	}
	for _, args := range [][]string{
		{"scan"},
		{"list"},
		{"inspect", name},
		{"get", name, "battery"},
		{"gatt", name, "services"},
		{"driver", "list"},
	} {
		trace := filepath.Join(t.TempDir(), "trace")
		full := append([]string{"-f", "-qq", "-e", "trace=socket,connect", "-o", trace, btPath, "--json", "--non-interactive", "--timeout", commandTimeout}, args...)
		cmd := exec.Command(strace, full...)
		cmd.Env = append(os.Environ(), env...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("bt %v: %v\n%s", args, err, stderr.Bytes())
		}
		calls, err := os.ReadFile(trace)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(calls), "\n") {
			if inetSocket.MatchString(line) {
				t.Errorf("bt %v used the network: %s", args, line)
			}
		}
		// Anything still running from the build directory outlived bt.
		if out, err := exec.Command("pgrep", "-a", "-f", binDir).Output(); err == nil && len(bytes.TrimSpace(out)) > 0 {
			t.Errorf("bt %v left processes running:\n%s", args, out)
		}
	}
}
