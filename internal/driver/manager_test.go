package driver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
)

type fakeHost struct {
	openedDevice string
	written      []byte
}

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
	channels := map[string]bool{}
	params, _ := json.Marshal(protocol.L2CAPOpenParams{DeviceID: "device", PSM: 25})
	result, err := m.handleHost(context.Background(), manifest, protocol.Message{Method: protocol.HostL2CAPOpen, Params: params}, allowed, channels)
	if err != nil {
		t.Fatal(err)
	}
	if host.openedDevice != "device" || !channels["channel-1"] {
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
	value, found, err := m.storageGet(manifest.ID, "keys/airpods")
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
	if err := m.storageSet(id, "airpods/keyring-v1", "secret"); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(t.TempDir(), "state.json")
	if err := m.ExportState(id, export); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(export)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	if err := m.storageSet(id, "airpods/keyring-v1", "changed"); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportState(id, export); err != nil {
		t.Fatal(err)
	}
	value, found, err := m.storageGet(id, "airpods/keyring-v1")
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
	if err := m.storageSet(id, "keys/test", "secret"); err != nil {
		t.Fatalf("bundled driver state should remain user-writable: %v", err)
	}
}
