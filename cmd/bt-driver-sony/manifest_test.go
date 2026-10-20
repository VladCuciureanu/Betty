package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
)

// The Debian package installs a static copy of the manifest; it must not
// drift from the one the driver reports.
func TestPackagedManifestMatchesTheDriver(t *testing.T) {
	raw, err := os.ReadFile("../../packaging/linux/sony-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var packaged protocol.Manifest
	if err := json.Unmarshal(raw, &packaged); err != nil {
		t.Fatal(err)
	}
	packaged.Version = manifest.Version
	if !reflect.DeepEqual(packaged, manifest) {
		t.Fatalf("packaged=%+v\ndriver=%+v", packaged, manifest)
	}
}
