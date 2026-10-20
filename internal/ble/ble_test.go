package ble

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"tinygo.org/x/bluetooth"

	"github.com/vladcuciureanu/betty/internal/domain"
)

type fakePayload struct {
	bluetooth.AdvertisementPayload
	name         string
	services     []bluetooth.UUID
	manufacturer []bluetooth.ManufacturerDataElement
}

func (p fakePayload) LocalName() string                                     { return p.name }
func (p fakePayload) ServiceUUIDs() []bluetooth.UUID                        { return p.services }
func (p fakePayload) ManufacturerData() []bluetooth.ManufacturerDataElement { return p.manufacturer }
func (p fakePayload) ServiceData() []bluetooth.ServiceDataElement           { return nil }

func TestAdvertisementMergeKeepsEarlierFields(t *testing.T) {
	a := &advertisement{address: "AA:BB:CC:DD:EE:FF", manufacturer: map[uint16]string{}, serviceData: map[string]string{}}
	heart := bluetooth.ServiceUUIDHeartRate
	a.merge(bluetooth.ScanResult{RSSI: -40, AdvertisementPayload: fakePayload{name: "Sensor", services: []bluetooth.UUID{heart}}}, time.Unix(1, 0))
	a.merge(bluetooth.ScanResult{RSSI: -60, AdvertisementPayload: fakePayload{manufacturer: []bluetooth.ManufacturerDataElement{{CompanyID: 76, Data: []byte{1, 2}}}}}, time.Unix(2, 0))
	d := a.device()
	if d.Name != "Sensor" {
		t.Errorf("name=%q, want the name from the earlier packet", d.Name)
	}
	if len(d.Services) != 1 || d.Services[0] != heart.String() {
		t.Errorf("services=%v", d.Services)
	}
	if len(d.ManufacturerData) != 1 || d.ManufacturerData[0].Data != "0102" {
		t.Errorf("manufacturer=%v", d.ManufacturerData)
	}
	if *d.RSSI != -60 || !d.LastSeenAt.Equal(time.Unix(2, 0)) {
		t.Errorf("rssi=%d last_seen=%v, want the latest packet", *d.RSSI, d.LastSeenAt)
	}
}

func TestAdvertisementWithoutNameUsesAddress(t *testing.T) {
	a := &advertisement{address: "AA:BB:CC:DD:EE:FF", manufacturer: map[uint16]string{}, serviceData: map[string]string{}}
	a.merge(bluetooth.ScanResult{AdvertisementPayload: fakePayload{}}, time.Now())
	if got := a.device().Name; got != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("name=%q", got)
	}
}

func TestTranslate(t *testing.T) {
	cases := []struct {
		err  error
		want domain.ErrorCode
	}{
		{bluetooth.ErrAttInsufficientAuthentication, domain.CodeAuthentication},
		{fmt.Errorf("wrapped: %w", bluetooth.ErrAttWriteNotPermitted), domain.CodeUnsupported},
		{errors.New("timeout on Read()"), domain.CodeTimeout},
		{errors.New("bluetooth: adaptor is not powered"), domain.CodeUnavailable},
		{errors.New("something odd"), domain.CodeDisconnected},
	}
	for _, c := range cases {
		if got := domain.AsError(translate("op", c.err, domain.CodeDisconnected)).Code; got != c.want {
			t.Errorf("translate(%v) = %q, want %q", c.err, got, c.want)
		}
	}
	usage := domain.NewError(domain.CodeUsage, "bad")
	if got := translate("op", usage, domain.CodeInternal); got != usage {
		t.Errorf("domain errors must pass through, got %v", got)
	}
}

func TestConnectRejectsMalformedAddress(t *testing.T) {
	b := &TinyGo{}
	_, err := b.connect(t.Context(), "not-an-address")
	if domain.AsError(err).Code != domain.CodeUsage {
		t.Fatalf("err=%v, want usage error", err)
	}
}

func TestWatchDisconnectSignalsOnlyThatDevice(t *testing.T) {
	b := &TinyGo{}
	lost, stop := b.watchDisconnect("aa:bb:cc:dd:ee:ff")
	defer stop()
	other, stopOther := b.watchDisconnect("11:22:33:44:55:66")
	defer stopOther()
	b.notifyDisconnect("AA:BB:CC:DD:EE:FF")
	b.notifyDisconnect("AA:BB:CC:DD:EE:FF")
	select {
	case <-lost:
	default:
		t.Fatal("watcher was not notified")
	}
	select {
	case <-other:
		t.Fatal("unrelated watcher was notified")
	default:
	}
}
