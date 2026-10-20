// Command gattprobe is a test driver for the end-to-end suite. It reads a BLE
// device through Betty's GATT host calls and reports what it saw as
// properties, so a test can check the host calls against a scripted device:
//
//	probe.services  the number of services gatt.services lists
//	probe.battery   the Battery Level characteristic, read with gatt.read
//	probe.notify    the first value gatt.next returns after gatt.subscribe
package main

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/vladcuciureanu/betty/driver"
)

// notifyCharacteristic is the scripted sensor's notifying characteristic.
const notifyCharacteristic = "b0e7e2e0-0000-4000-8000-000000000002"

func main() {
	driver.Serve(driver.Driver{
		Manifest: driver.Manifest{ID: "e2e/gatt-probe", Name: "GATT probe", Version: "1", Permissions: []string{driver.PermissionGATT}},
		Get:      get,
	})
}

func get(host *driver.Session, request driver.PropertyRequest) (*driver.Enrichment, error) {
	var value string
	switch request.Property {
	case "probe.services":
		services, err := host.GATTServices(request.Device.ID)
		if err != nil {
			return nil, err
		}
		value = strconv.Itoa(len(services))
	case "probe.battery":
		data, err := host.ReadGATT(request.Device.ID, "", "2a19")
		if err != nil {
			return nil, err
		}
		value = hex.EncodeToString(data)
	case "probe.notify":
		subscription, err := host.SubscribeGATT(request.Device.ID, "", notifyCharacteristic)
		if err != nil {
			return nil, err
		}
		data, err := host.NextGATT(subscription, 25*time.Second)
		if err != nil {
			return nil, err
		}
		value = hex.EncodeToString(data)
	default:
		return nil, nil
	}
	raw, _ := json.Marshal(value)
	return &driver.Enrichment{Properties: map[string]driver.Property{request.Property: {Value: raw}}}, nil
}
