package driver

import (
	"context"
	"testing"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

type fakeHIDHost struct {
	opened  []string
	feature uint8
	closed  []string
}

func (f *fakeHIDHost) OpenHID(_ context.Context, address string, usagePage, usage uint16) (protocol.HIDCollection, error) {
	f.opened = append(f.opened, address)
	return protocol.HIDCollection{HandleID: "hid-1", VendorID: 0x054c, ProductID: 0x0ce6}, nil
}
func (*fakeHIDHost) ReadHID(context.Context, string) ([]byte, error) { return []byte{0x31, 0x01}, nil }
func (f *fakeHIDHost) GetHIDFeature(_ context.Context, _ string, reportID uint8) ([]byte, error) {
	f.feature = reportID
	return []byte{reportID, 0xff}, nil
}
func (f *fakeHIDHost) CloseHID(_ context.Context, id string) error {
	f.closed = append(f.closed, id)
	return nil
}

func hidTest(t *testing.T) (*Manager, *fakeHIDHost, *hostSession, protocol.Manifest) {
	t.Helper()
	m := NewAt(t.TempDir())
	host := &fakeHIDHost{}
	m.SetHIDHost(host)
	session := newHostSession([]protocol.Device{
		{ID: "pad", Endpoints: []protocol.Endpoint{{ID: "4C:B9:9B:32:3B:AA", Transport: "os", Address: "4C:B9:9B:32:3B:AA"}}},
		{ID: "nameless"},
	})
	return m, host, session, protocol.Manifest{ID: "example/pad", Permissions: []string{protocol.PermissionHID}}
}

func TestHIDHostCallsOpenReadAndClose(t *testing.T) {
	m, host, session, driver := hidTest(t)
	opened, err := call(t, m, driver, session, protocol.HostHIDOpen, protocol.HIDOpenParams{DeviceID: "pad", UsagePage: 1, Usage: 5})
	if err != nil || opened["handle_id"] != "hid-1" || opened["vendor_id"] != float64(0x054c) || opened["product_id"] != float64(0x0ce6) {
		t.Fatalf("opened=%v err=%v", opened, err)
	}
	if len(host.opened) != 1 || host.opened[0] != "4C:B9:9B:32:3B:AA" {
		t.Fatalf("opened address=%v", host.opened)
	}
	if read, err := call(t, m, driver, session, protocol.HostHIDRead, protocol.HIDReadParams{HandleID: "hid-1", TimeoutMS: 300}); err != nil || read["data"] != "3101" {
		t.Fatalf("read=%v err=%v", read, err)
	}
	if feature, err := call(t, m, driver, session, protocol.HostHIDGetFeature, protocol.HIDFeatureParams{HandleID: "hid-1", ReportID: 5}); err != nil || feature["data"] != "05ff" || host.feature != 5 {
		t.Fatalf("feature=%v err=%v", feature, err)
	}
	// Betty closes a handle the driver left open.
	m.closeSession(session)
	if len(host.closed) != 1 || host.closed[0] != "hid-1" {
		t.Fatalf("closed=%v", host.closed)
	}
}

func TestHIDHostCallsAreScoped(t *testing.T) {
	m, _, session, driver := hidTest(t)
	open := protocol.HIDOpenParams{DeviceID: "pad"}
	if _, err := call(t, m, protocol.Manifest{ID: "example/denied"}, session, protocol.HostHIDOpen, open); domain.AsError(err).Code != domain.CodePermission {
		t.Fatalf("a driver without the hid permission must be refused: %v", err)
	}
	if _, err := call(t, m, driver, session, protocol.HostHIDOpen, protocol.HIDOpenParams{DeviceID: "stranger"}); domain.AsError(err).Code != domain.CodePermission {
		t.Fatalf("a device outside the request must be refused: %v", err)
	}
	if _, err := call(t, m, driver, session, protocol.HostHIDOpen, protocol.HIDOpenParams{DeviceID: "nameless"}); domain.AsError(err).Code != domain.CodeUnsupported {
		t.Fatalf("a device without an address must be unsupported: %v", err)
	}
	if _, err := call(t, m, driver, session, protocol.HostHIDRead, protocol.HIDReadParams{HandleID: "hid-9"}); domain.AsError(err).Code != domain.CodePermission {
		t.Fatalf("a handle the driver didn't open must be refused: %v", err)
	}
	if _, err := call(t, m, driver, session, protocol.HostHIDOpen, open); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, m, driver, session, protocol.HostHIDRead, protocol.HIDReadParams{HandleID: "hid-1", TimeoutMS: 30001}); domain.AsError(err).Code != domain.CodeUsage {
		t.Fatalf("a timeout over 30 s must be refused: %v", err)
	}
}

func TestDriverWarningsAreRelayedInDeviceOrder(t *testing.T) {
	m := NewAt(t.TempDir())
	var got []string
	m.OnDriverWarning = func(_ context.Context, driverID, message string) { got = append(got, driverID+": "+message) }
	m.relayWarnings(context.Background(), "example/pad", map[string]protocol.Enrichment{
		"b": {Warnings: []string{"second"}},
		"a": {Warnings: []string{"first"}},
		"c": {},
	})
	if len(got) != 2 || got[0] != "example/pad: first" || got[1] != "example/pad: second" {
		t.Fatalf("warnings=%v", got)
	}
}
