package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
)

// The Debian package and the Windows installer ship static copies of the
// manifest; they must not drift from the one the driver reports.
func TestPackagedManifestsMatchTheDriver(t *testing.T) {
	for _, path := range []string{"../../packaging/linux/apple-audio-manifest.json", "../../packaging/windows/apple-audio-manifest.json"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var packaged protocol.Manifest
		if err := json.Unmarshal(raw, &packaged); err != nil {
			t.Fatal(err)
		}
		packaged.Version = manifest.Version
		if !reflect.DeepEqual(packaged, manifest) {
			t.Fatalf("%s=%+v\ndriver=%+v", path, packaged, manifest)
		}
	}
}
