package app

import (
	"strings"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/domain"
	"github.com/vladcuciureanu/betty/internal/output"
)

func TestFlagsMayAppearAnywhere(t *testing.T) {
	o, args, err := parseArgs([]string{"get", "--json", "kb", "battery", "--timeout=3s", "--non-interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if o.mode != output.JSON || !o.nonInteractive || o.timeout != 3*time.Second || !o.timeoutSet || strings.Join(args, " ") != "get kb battery" {
		t.Fatalf("o=%+v args=%v", o, args)
	}
}

func TestNegativeNumbersAndDoubleDashArePositional(t *testing.T) {
	_, args, err := parseArgs([]string{"set", "dev", "level", "-3", "--", "--json"})
	if err != nil || strings.Join(args, " ") != "set dev level -3 --json" {
		t.Fatalf("args=%v err=%v", args, err)
	}
}

func TestParseArgsRejectsBadFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "--quiet", "scan"},
		{"--timeout", "0s", "scan"},
		{"--timeout", "-1s", "scan"},
		{"--timeout"},
		{"--frobnicate", "scan"},
		{"--json=yes", "scan"},
		{"--with-response", "--without-response", "gatt", "x", "write", "2a19", "01"},
	} {
		o, _, err := parseArgs(args)
		if domain.AsError(err).Code != domain.CodeUsage {
			t.Fatalf("%v: err=%v", args, err)
		}
		if args[0] == "--json" && o.mode != output.JSON {
			t.Fatalf("%v: a usage error after --json must render as JSON", args)
		}
	}
}

func TestHelpIsNeverAnError(t *testing.T) {
	for _, args := range [][]string{{}, {"help"}, {"-h"}, {"--help"}, {"--json", "--help"}, {"get", "-h"}, {"help", "gatt"}} {
		h := newHarness()
		if code := h.run(args...); code != 0 || h.stdout.Len() == 0 {
			t.Fatalf("bt %v: code=%d stdout=%q stderr=%q", args, code, h.stdout.String(), h.stderr.String())
		}
	}
	h := newHarness()
	h.run("help", "gatt")
	if !strings.Contains(h.stdout.String(), "--without-response") {
		t.Fatalf("gatt help lacks its subcommands:\n%s", h.stdout.String())
	}
}

func TestUnknownCommandIsAUsageErrorWithASuggestion(t *testing.T) {
	h := newHarness()
	connected := false
	code := run([]string{"lsit"}, &h.stdout, &h.stderr, "test", func(*application) { connected = true })
	if code != domain.ExitCode(domain.NewError(domain.CodeUsage, "")) || !strings.Contains(h.stderr.String(), `did you mean "list"`) {
		t.Fatalf("code=%d stderr=%q", code, h.stderr.String())
	}
	if connected {
		t.Fatal("an unknown command must not touch Bluetooth")
	}
}

func TestWrongArgumentCountShowsUsage(t *testing.T) {
	h := newHarness()
	if code := h.run("get", "kb"); code != 2 || !strings.Contains(h.stderr.String(), "usage: bt get <device> <property>") {
		t.Fatalf("code=%d stderr=%q", code, h.stderr.String())
	}
}

func TestCommandFlagsAreCheckedAgainstTheCommand(t *testing.T) {
	h := newHarness()
	if code := h.run("get", "kb", "battery", "--interval", "1s"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

func TestVersion(t *testing.T) {
	h := newHarness()
	if code := h.run("--version"); code != 0 || h.stdout.String() != "bt test\n" {
		t.Fatalf("code=%d stdout=%q", code, h.stdout.String())
	}
	if code := h.run("--json", "version"); code != 0 || strings.TrimSpace(h.stdout.String()) != `{"schema_version":1,"data":{"version":"test"}}` {
		t.Fatalf("code=%d stdout=%q", code, h.stdout.String())
	}
}

func TestPairWaitsLongerForAPerson(t *testing.T) {
	pair := findCommand("pair")
	if got := (&application{opts: options{timeout: defaultTimeout}}).commandTimeout(pair); got != defaultPairTimeout {
		t.Fatalf("pair timeout=%s", got)
	}
	for _, o := range []options{{timeout: 5 * time.Second, timeoutSet: true}, {timeout: defaultTimeout, nonInteractive: true}} {
		if got := (&application{opts: o}).commandTimeout(pair); got != o.timeout {
			t.Fatalf("opts=%+v timeout=%s", o, got)
		}
	}
}

func TestPropertyInputValidatesTypeAndRange(t *testing.T) {
	minimum, maximum, step := -10.0, 20.0, 2.0
	capability := domain.DeviceCapability{Kind: domain.KindInteger, Minimum: &minimum, Maximum: &maximum, Step: &step}
	for input, want := range map[string]string{"12": "12", "-4": "-4"} {
		if value, err := propertyInput(capability, "level", input); err != nil || string(value) != want {
			t.Fatalf("%s: value=%s err=%v", input, value, err)
		}
	}
	for _, input := range []string{"21", "-12", "3", "loud"} {
		if _, err := propertyInput(capability, "level", input); domain.AsError(err).Code != domain.CodeUsage {
			t.Fatalf("%s: err=%v", input, err)
		}
	}
	noMinimum := domain.DeviceCapability{Kind: domain.KindInteger, Step: &step}
	if _, err := propertyInput(noMinimum, "level", "-4"); err != nil {
		t.Fatalf("a negative multiple of the step was rejected: %v", err)
	}
	enum := domain.DeviceCapability{Kind: domain.KindEnum, Values: []string{"off", "anc"}}
	if value, err := propertyInput(enum, "noise_control", "anc"); err != nil || string(value) != `"anc"` {
		t.Fatalf("value=%s err=%v", value, err)
	}
}

func TestMergeJoinsEndpointsThatShareAnAddress(t *testing.T) {
	devices := mergeDevices(
		[]domain.Device{classicDevice("windows:04:E4", `55" OLED`, "04:E4:B6:1F:F5:8D")},
		[]domain.Device{bleDevice("04:E4:B6:1F:F5:8D", `55" OLED`), bleDevice("4C:57:39:63:28:65", "4C:57:39:63:28:65")},
	)
	if len(devices) != 2 || devices[0].ID != "windows:04:E4" || len(devices[0].Endpoints) != 2 {
		t.Fatalf("devices=%+v", devices)
	}
}

func TestMergeNeverUsesTheNameAsIdentity(t *testing.T) {
	devices := mergeDevices([]domain.Device{bleDevice("AA:AA:AA:AA:AA:AA", "Fridge")}, []domain.Device{bleDevice("BB:BB:BB:BB:BB:BB", "Fridge")})
	if len(devices) != 2 {
		t.Fatalf("merged distinct same-name devices: %+v", devices)
	}
}

func TestMergePrefersARealNameOverAnAddress(t *testing.T) {
	devices := mergeDevices([]domain.Device{bleDevice("AA:AA:AA:AA:AA:AA", "AA:AA:AA:AA:AA:AA")}, []domain.Device{bleDevice("AA:AA:AA:AA:AA:AA", "Heart Rate")})
	if len(devices) != 1 || devices[0].Name != "Heart Rate" {
		t.Fatalf("devices=%+v", devices)
	}
}
