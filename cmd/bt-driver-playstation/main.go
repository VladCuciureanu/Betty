package main

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
)

var version = "dev"

var manifest = protocol.Manifest{
	Protocol: protocol.Version,
	ID:       "official/playstation-controllers",
	Name:     "PlayStation Controllers",
	Version:  version,
	Permissions: []string{
		protocol.PermissionDeviceMetadata,
		protocol.PermissionHID,
	},
	Match: &protocol.Match{
		// DualSense, DualSense Edge and DualShock 4 all name themselves
		// "… Wireless Controller"; the vendor and product IDs confirm it.
		NameContains: []string{"Wireless Controller"},
	},
}

const (
	usagePageGenericDesktop = 0x01
	usageGamepad            = 0x05

	// readTimeout bounds one input report. A connected controller sends
	// one every few milliseconds.
	readTimeout = 300 * time.Millisecond
	// switchReads is how many reports the driver reads after switching,
	// while the controller changes over.
	switchReads = 20

	switchWarning = "Betty switched the controller to its extended reports to read the battery; games that use DirectInput may not see it until it is turned off and on"
)

func main() {
	protocol.Serve(protocol.Driver{Manifest: manifest, Inspect: inspect, Get: get})
}

// controller is an open, identified PlayStation controller.
type controller struct {
	host   *protocol.Session
	handle string
	model  string
	family family
}

// open opens a connected device's gamepad collection and reports false when
// it isn't a known PlayStation controller.
func open(host *protocol.Session, device protocol.Device) (*controller, bool, error) {
	if !device.Connected {
		return nil, false, nil
	}
	collection, err := host.OpenHID(device.ID, usagePageGenericDesktop, usageGamepad)
	if err != nil {
		return nil, false, err
	}
	known, ok := controllers[collection.ProductID]
	if collection.VendorID != sonyVendorID || !ok {
		host.CloseHID(collection.HandleID)
		return nil, false, nil
	}
	return &controller{host: host, handle: collection.HandleID, model: known.model, family: known.family}, true, nil
}

func (c *controller) close() { c.host.CloseHID(c.handle) }

// battery reads the next report and decodes its battery. It reports false
// when the controller sends only the basic report.
func (c *controller) battery() (protocol.BatteryComponent, bool, error) {
	report, err := c.host.ReadHID(c.handle, readTimeout)
	if err != nil {
		return protocol.BatteryComponent{}, false, err
	}
	battery, ok := c.family.extendedBattery(report)
	return battery, ok, nil
}

// switchedBattery switches the controller to its extended reports and reads
// the battery from the first one.
func (c *controller) switchedBattery() (protocol.BatteryComponent, error) {
	if _, err := c.host.GetHIDFeature(c.handle, c.family.switchReport); err != nil {
		return protocol.BatteryComponent{}, err
	}
	for range switchReads {
		battery, ok, err := c.battery()
		if err != nil {
			return protocol.BatteryComponent{}, err
		}
		if ok {
			return battery, nil
		}
	}
	return protocol.BatteryComponent{}, protocol.Errorf(protocol.CodeUnsupported, "the controller did not report its battery after switching to extended reports")
}

func identification(model string, confidence string) *protocol.Identification {
	return &protocol.Identification{Vendor: "Sony", Family: "PlayStation controller", Model: model, Confidence: confidence}
}

var batteryCapability = map[string]protocol.Capability{
	protocol.PropertyBattery: {Status: protocol.StatusAvailable, Kind: protocol.KindBattery, Readable: true},
}

func batteryProperty(battery protocol.BatteryComponent) protocol.Property {
	value := protocol.SingleBattery(battery)
	raw, _ := json.Marshal(value)
	return protocol.Property{Value: raw, Unit: "%", Display: value.Display(), Estimated: true, Resolution: 10}
}

// inspect identifies connected controllers by their vendor and product IDs,
// and adds the battery only if the controller already sends extended
// reports: listing devices never switches it. A disconnected controller is
// identified by name when the name says DualSense.
func inspect(host *protocol.Session, request protocol.InspectRequest) (map[string]protocol.Enrichment, error) {
	enrichments := map[string]protocol.Enrichment{}
	for _, device := range request.Devices {
		if !strings.Contains(device.Name, "Wireless Controller") {
			continue
		}
		c, ok, err := open(host, device)
		if err != nil || !ok {
			if !device.Connected && strings.Contains(device.Name, "DualSense") {
				enrichments[device.ID] = protocol.Enrichment{Identification: identification("DualSense", protocol.ConfidenceName)}
			}
			continue
		}
		enrichment := protocol.Enrichment{Identification: identification(c.model, protocol.ConfidenceModel), Capabilities: batteryCapability}
		if battery, ok, err := c.battery(); err == nil && ok {
			enrichment.Properties = map[string]protocol.Property{protocol.PropertyBattery: batteryProperty(battery)}
		}
		c.close()
		enrichments[device.ID] = enrichment
	}
	return enrichments, nil
}

// get reads the battery, switching the controller to its extended reports
// if it isn't sending them yet, with a warning.
func get(host *protocol.Session, request protocol.PropertyRequest) (*protocol.Enrichment, error) {
	if request.Property != protocol.PropertyBattery || !strings.Contains(request.Device.Name, "Wireless Controller") {
		return nil, nil
	}
	if !request.Device.Connected {
		return nil, protocol.Errorf(protocol.CodeDeviceDisconnected, "the controller's battery can be read only while it is connected over Bluetooth")
	}
	c, ok, err := open(host, request.Device)
	if err != nil {
		return nil, protocol.Wrap(err, protocol.CodeBluetoothUnavailable)
	}
	if !ok {
		return nil, nil
	}
	defer c.close()
	enrichment := &protocol.Enrichment{Identification: identification(c.model, protocol.ConfidenceModel), Capabilities: batteryCapability}
	battery, ok, err := c.battery()
	if err != nil && !isTimeout(err) {
		return nil, protocol.Wrap(err, protocol.CodeBluetoothUnavailable)
	}
	if !ok {
		if battery, err = c.switchedBattery(); err != nil {
			return nil, protocol.Wrap(err, protocol.CodeBluetoothUnavailable)
		}
		enrichment.Warnings = []string{switchWarning}
	}
	enrichment.Properties = map[string]protocol.Property{protocol.PropertyBattery: batteryProperty(battery)}
	return enrichment, nil
}

// isTimeout reports whether a host call failed only because no report came:
// a controller in basic mode may send nothing while it is idle.
func isTimeout(err error) bool {
	var remote *protocol.RemoteError
	return errors.As(err, &remote) && remote.Code == string(protocol.CodeTimeout)
}
