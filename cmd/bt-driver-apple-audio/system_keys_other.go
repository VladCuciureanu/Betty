//go:build !darwin

package main

import (
	"fmt"

	protocol "github.com/vladcuciureanu/betty/driver"
)

func systemAppleAudioKeys(protocol.Device) (appleAudioKeys, error) {
	return appleAudioKeys{}, fmt.Errorf("system Apple audio keys are unavailable on this platform")
}
