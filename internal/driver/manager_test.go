package driver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func TestDriverStorageMigratesFromLegacyID(t *testing.T) {
	m := NewAt(t.TempDir())
	legacy := protocol.Manifest{ID: "official/airpods"}
	current := protocol.Manifest{ID: "official/apple-audio", LegacyIDs: []string{legacy.ID}}
	if err := m.storageSet(legacy, "airpods/keyring-v1", "secret"); err != nil {
		t.Fatal(err)
	}
	value, found, err := m.storageGet(current, "airpods/keyring-v1")
	if err != nil || !found || value != "secret" {
		t.Fatalf("value=%q found=%t err=%v", value, found, err)
	}
	if err := m.storageSet(current, "apple-audio/keyring-v1", "new"); err != nil {
		t.Fatal(err)
	}
	values, err := m.readStorage(current.ID)
	if err != nil || values["airpods/keyring-v1"] != "secret" || values["apple-audio/keyring-v1"] != "new" {
		t.Fatalf("values=%#v err=%v", values, err)
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
	if err := m.storageSet(protocol.Manifest{ID: id}, "airpods/keyring-v1", "secret"); err != nil {
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
	if err := m.storageSet(protocol.Manifest{ID: id}, "airpods/keyring-v1", "changed"); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportState(id, export); err != nil {
		t.Fatal(err)
	}
	value, found, err := m.storageGet(protocol.Manifest{ID: id}, "airpods/keyring-v1")
	if err != nil || !found || value != "secret" {
		t.Fatalf("value=%q found=%t err=%v", value, found, err)
	}
}

func TestBundledDriversAreDiscoveredButNotRemoved(t *testing.T) {
	m := NewAt(t.TempDir())
	m.bundled = t.TempDir()
	id := "official/airpods"
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
	id := "official/airpods"
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
