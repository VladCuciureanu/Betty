package driver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

type fakeHost struct {
	openedDevice string
	written      []byte
}

type fakeRFCOMMHost struct {
	openedDevice  string
	openedService string
	written       []byte
}

func (f *fakeRFCOMMHost) OpenRFCOMM(_ context.Context, device, service string) (string, error) {
	f.openedDevice, f.openedService = device, service
	return "rfcomm-1", nil
}
func (*fakeRFCOMMHost) ReadRFCOMM(context.Context, string, int) ([]byte, error) {
	return []byte{0x3e, 0x3c}, nil
}
func (f *fakeRFCOMMHost) WriteRFCOMM(_ context.Context, _ string, data []byte) (int, error) {
	f.written = append([]byte(nil), data...)
	return len(data), nil
}
func (*fakeRFCOMMHost) CloseRFCOMM(context.Context, string) error { return nil }

func (f *fakeHost) OpenL2CAP(_ context.Context, device string, _ uint16) (string, error) {
	f.openedDevice = device
	return "channel-1", nil
}
func (f *fakeHost) ReadL2CAP(context.Context, string, int) ([]byte, error) {
	return []byte{1, 2, 3}, nil
}
func (f *fakeHost) WriteL2CAP(_ context.Context, _ string, data []byte) (int, error) {
	f.written = append([]byte(nil), data...)
	return len(data), nil
}
func (f *fakeHost) CloseL2CAP(context.Context, string) error { return nil }

func TestHostL2CAPIsScopedToSelectedDevicesAndChannels(t *testing.T) {
	m := NewAt(t.TempDir())
	host := &fakeHost{}
	m.SetHost(host)
	manifest := protocol.Manifest{ID: "official/test", Permissions: []string{"l2cap"}}
	allowed := map[string]bool{"device": true}
	channels := map[string]string{}
	params, _ := json.Marshal(protocol.L2CAPOpenParams{DeviceID: "device", PSM: 25})
	result, err := m.handleHost(context.Background(), manifest, protocol.Message{Method: protocol.HostL2CAPOpen, Params: params}, allowed, channels)
	if err != nil {
		t.Fatal(err)
	}
	if host.openedDevice != "device" || channels["channel-1"] != "l2cap" {
		t.Fatalf("result=%s host=%#v channels=%v", result, host, channels)
	}
	write, _ := json.Marshal(protocol.L2CAPWriteParams{ChannelID: "channel-1", Data: "aabb"})
	if _, err = m.handleHost(context.Background(), manifest, protocol.Message{Method: protocol.HostL2CAPWrite, Params: write}, allowed, channels); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(host.written) != "aabb" {
		t.Fatalf("written=%x", host.written)
	}
	foreign, _ := json.Marshal(protocol.L2CAPOpenParams{DeviceID: "other", PSM: 25})
	if _, err = m.handleHost(context.Background(), manifest, protocol.Message{Method: protocol.HostL2CAPOpen, Params: foreign}, allowed, channels); err == nil {
		t.Fatal("expected device scope error")
	}
}

func TestHostRFCOMMIsPermissionAndDeviceScoped(t *testing.T) {
	m := NewAt(t.TempDir())
	host := &fakeRFCOMMHost{}
	m.SetRFCOMMHost(host)
	manifest := protocol.Manifest{ID: "official/sony-headphones", Permissions: []string{"rfcomm"}}
	allowed := map[string]bool{"sony": true}
	channels := map[string]string{}
	params, _ := json.Marshal(protocol.RFCOMMOpenParams{DeviceID: "sony", ServiceUUID: "956c"})
	result, err := m.handleHost(context.Background(), manifest, protocol.Message{Method: protocol.HostRFCOMMOpen, Params: params}, allowed, channels)
	if err != nil {
		t.Fatal(err)
	}
	if host.openedDevice != "sony" || host.openedService != "956c" || channels["rfcomm-1"] != "rfcomm" {
		t.Fatalf("result=%s host=%#v channels=%v", result, host, channels)
	}
	write, _ := json.Marshal(protocol.RFCOMMWriteParams{ChannelID: "rfcomm-1", Data: "aabb"})
	if _, err := m.handleHost(context.Background(), manifest, protocol.Message{Method: protocol.HostRFCOMMWrite, Params: write}, allowed, channels); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(host.written) != "aabb" {
		t.Fatalf("written=%x", host.written)
	}
	foreign, _ := json.Marshal(protocol.RFCOMMOpenParams{DeviceID: "other", ServiceUUID: "956c"})
	if _, err := m.handleHost(context.Background(), manifest, protocol.Message{Method: protocol.HostRFCOMMOpen, Params: foreign}, allowed, channels); err == nil {
		t.Fatal("expected device scope error")
	}
	if _, err := m.handleHost(context.Background(), protocol.Manifest{ID: "official/denied"}, protocol.Message{Method: protocol.HostRFCOMMOpen, Params: params}, allowed, channels); err == nil {
		t.Fatal("expected RFCOMM permission error")
	}
}

func TestHostPermissionsAndNamespacedStorage(t *testing.T) {
	m := NewAt(t.TempDir())
	manifest := protocol.Manifest{ID: "official/test", Permissions: []string{"driver-storage"}}
	if err := os.MkdirAll(filepath.Join(m.root, safeID(manifest.ID)), 0o755); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(protocol.StorageSetParams{Key: "keys/airpods", Value: "secret"})
	if _, err := m.handleHost(context.Background(), manifest, protocol.Message{Method: protocol.HostStorageSet, Params: params}, nil, nil); err != nil {
		t.Fatal(err)
	}
	value, found, err := m.storageGet(manifest, "keys/airpods")
	if err != nil || !found || value != "secret" {
		t.Fatalf("value=%q found=%t err=%v", value, found, err)
	}
	denied := protocol.Manifest{ID: "official/denied"}
	if _, err := m.handleHost(context.Background(), denied, protocol.Message{Method: protocol.HostStorageSet, Params: params}, nil, nil); err == nil {
		t.Fatal("expected permission error")
	}
}

func TestExportImportDriverState(t *testing.T) {
	root := t.TempDir()
	m := NewAt(root)
	id := "official/test"
	dir := filepath.Join(root, safeID(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(protocol.Manifest{Protocol: protocol.Version, ID: id})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.storageSet(protocol.Manifest{ID: id}, "keys/example", "secret"); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(t.TempDir(), "state.json")
	if err := m.ExportState(id, export); err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permission bits; Go always reports 0666 there.
	if info, _ := os.Stat(export); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	if err := m.storageSet(protocol.Manifest{ID: id}, "keys/example", "changed"); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportState(id, export); err != nil {
		t.Fatal(err)
	}
	value, found, err := m.storageGet(protocol.Manifest{ID: id}, "keys/example")
	if err != nil || !found || value != "secret" {
		t.Fatalf("value=%q found=%t err=%v", value, found, err)
	}
}

func TestBundledDriversAreDiscoveredButNotRemoved(t *testing.T) {
	m := NewAt(t.TempDir())
	m.bundled = t.TempDir()
	id := "official/bundled-test"
	dir := filepath.Join(m.bundled, safeID(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(protocol.Manifest{Protocol: protocol.Version, ID: id, Name: "AirPods"})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := m.List()
	if err != nil || len(items) != 1 || items[0].ID != id {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	if err := m.Remove(id); err == nil {
		t.Fatal("expected bundled driver removal to be rejected")
	}
	if err := m.storageSet(protocol.Manifest{ID: id}, "keys/test", "secret"); err != nil {
		t.Fatalf("bundled driver state should remain user-writable: %v", err)
	}
}

func TestSystemBundledDriverIsDiscoveredAndExecutable(t *testing.T) {
	m := NewAt(t.TempDir())
	m.systemBundled = t.TempDir()
	id := "official/bundled-test"
	dir := filepath.Join(m.systemBundled, safeID(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(protocol.Manifest{Protocol: protocol.Version, ID: id, Name: "AirPods"})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, executableName())
	if err := os.WriteFile(driver, []byte("driver"), 0o755); err != nil {
		t.Fatal(err)
	}
	items, err := m.List()
	if err != nil || len(items) != 1 || items[0].ID != id {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	if got := m.driverPath(id); got != driver {
		t.Fatalf("driverPath=%q want=%q", got, driver)
	}
	if err := m.Remove(id); err == nil {
		t.Fatal("expected system-bundled driver removal to be rejected")
	}
}

func TestDriverErrorCodesAreRestrictedToBettyCategories(t *testing.T) {
	if got := driverErrorCode(string(domain.CodeDisconnected)); got != domain.CodeDisconnected {
		t.Fatalf("known code=%q", got)
	}
	if got := driverErrorCode("vendor_surprise"); got != domain.CodeInternal {
		t.Fatalf("unknown code=%q", got)
	}
}

func TestOfficialPathDriverNamesAreStable(t *testing.T) {
	drivers := officialPathDrivers()
	if drivers["official/apple-audio"] == "" || drivers["official/sony-headphones"] == "" {
		t.Fatalf("drivers=%v", drivers)
	}
}

func TestPublicErrorCodesMatchDomainCodes(t *testing.T) {
	for _, code := range protocol.ErrorCodes {
		if got := driverErrorCode(string(code)); string(got) != string(code) {
			t.Fatalf("public code %q maps to %q", code, got)
		}
	}
	if len(protocol.ErrorCodes) != 12 {
		t.Fatalf("public error codes changed; update driverErrorCode and docs/DRIVER_PROTOCOL.md")
	}
}

func TestConcurrentStorageWritesAreAllKept(t *testing.T) {
	m := NewAt(t.TempDir())
	driver := protocol.Manifest{ID: "example/storage"}
	const writers = 16
	errs := make(chan error, writers)
	for i := range writers {
		go func() { errs <- m.storageSet(driver, fmt.Sprintf("key/%d", i), "value") }()
	}
	for range writers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	values, err := m.readStorage(driver.ID)
	if err != nil || len(values) != writers {
		t.Fatalf("kept %d of %d keys, err=%v", len(values), writers, err)
	}
}

func TestStorageLimits(t *testing.T) {
	m := NewAt(t.TempDir())
	driver := protocol.Manifest{ID: "example/limits"}
	for i := range maxStorageKeys {
		if err := m.storageSet(driver, fmt.Sprintf("k%d", i), "v"); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.storageSet(driver, "one-too-many", "v"); err == nil {
		t.Fatal("a key beyond the limit was stored")
	}
	if err := m.storageSet(driver, "k0", "replaced"); err != nil {
		t.Fatalf("replacing an existing key must still work: %v", err)
	}
	big := strings.Repeat("x", maxStorageValue)
	m = NewAt(t.TempDir())
	for i := range maxStorageBytes / maxStorageValue {
		err := m.storageSet(driver, fmt.Sprintf("big%d", i), big)
		if err != nil && i < maxStorageBytes/maxStorageValue-1 {
			t.Fatalf("value %d: %v", i, err)
		}
		if err != nil {
			return
		}
	}
	t.Fatal("storage grew past 1 MiB")
}

func TestImportStateRejectsOversizedFiles(t *testing.T) {
	m := NewAt(t.TempDir())
	id := "official/test"
	dir := filepath.Join(m.root, safeID(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(protocol.Manifest{Protocol: protocol.Version, ID: id})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(source, []byte(`{"k":"`+strings.Repeat("x", maxStorageBytes)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportState(id, source); err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("err=%v", err)
	}
}
