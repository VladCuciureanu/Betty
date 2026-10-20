package driver

import (
	"context"
	"path/filepath"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
)

func checkTestDriver(t *testing.T, mode string, permissions ...string) []CheckResult {
	t.Helper()
	manifest := protocol.Manifest{ID: "example/checked", Name: "Checked", Version: "1", Permissions: permissions}
	path := writeTestDriver(t, filepath.Join(t.TempDir(), "driver"), manifest)
	t.Setenv(testDriverEnv, manifest.ID+"="+mode)
	results, err := Check(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

func failures(results []CheckResult) map[string]string {
	failed := map[string]string{}
	for _, result := range results {
		if !result.Passed {
			failed[result.Name] = result.Detail
		}
	}
	return failed
}

func TestAConformingDriverPassesEveryCheck(t *testing.T) {
	results := checkTestDriver(t, "sdk")
	if failed := failures(results); len(failed) != 0 || len(results) < 6 {
		t.Fatalf("%d results, failures: %v", len(results), failed)
	}
}

func TestADriverThatAnswersOnceFailsTheSessionChecks(t *testing.T) {
	// The "ok" test driver answers one request and then ignores its input.
	failed := failures(checkTestDriver(t, "ok"))
	if _, ok := failed["keeps reading after a response, and answers an unknown method with unsupported"]; !ok {
		t.Fatalf("failures: %v", failed)
	}
	if _, ok := failed["answers inspect with a response carrying the request's ID"]; ok {
		t.Fatalf("the first request was answered correctly: %v", failed)
	}
}

func TestCheckReportsAWrongResponseID(t *testing.T) {
	failed := failures(checkTestDriver(t, "wrong-id"))
	if detail := failed["answers inspect with a response carrying the request's ID"]; detail == "" {
		t.Fatalf("failures: %v", failed)
	}
}
