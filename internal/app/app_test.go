package app

import (
	"bytes"
	"testing"
	"time"

	"github.com/vladcuciureanu/betty/internal/output"
)

func TestParseGlobal(t *testing.T) {
	o, args, err := parseGlobal([]string{"--json", "--timeout", "3s", "scan"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if o.mode != output.JSON || o.timeout != 3*time.Second || len(args) != 1 || args[0] != "scan" {
		t.Fatalf("unexpected parse: %+v %v", o, args)
	}
}
func TestParseGlobalRejectsConflictingOutput(t *testing.T) {
	_, _, err := parseGlobal([]string{"--json", "--quiet", "scan"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error")
	}
}
func TestVersion(t *testing.T) {
	var out bytes.Buffer
	if code := Main([]string{"--version"}, bytes.NewBuffer(nil), &out, &bytes.Buffer{}, "1.2.3"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if out.String() != "bt 1.2.3\n" {
		t.Fatalf("output=%q", out.String())
	}
}
