package domain

import "testing"

func TestExitCodesAreStable(t *testing.T) {
	cases := map[ErrorCode]int{CodeUsage: 2, CodeNotFound: 3, CodeAmbiguous: 4, CodeUnsupported: 5, CodePermission: 6, CodeUnavailable: 7, CodeAuthentication: 8, CodeTimeout: 9}
	for code, want := range cases {
		if got := ExitCode(NewError(code, "test")); got != want {
			t.Errorf("%s: got %d want %d", code, got, want)
		}
	}
}
