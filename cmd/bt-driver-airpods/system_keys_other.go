//go:build !darwin

package main

import (
	"fmt"

	protocol "github.com/vladcuciureanu/betty/driver"
)

func systemAirPodsKeys(protocol.Device) (airPodsKeys, error) {
	return airPodsKeys{}, fmt.Errorf("system AirPods keys are unavailable on this platform")
}
