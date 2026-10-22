//go:build !darwin && !windows

package hidbattery

import (
	"context"
	"io"
)

type platformProvider struct{}

func newPlatformProvider() Provider { return platformProvider{} }
func (platformProvider) Batteries(context.Context) ([]Observation, error) {
	return nil, nil
}

// runHelper does nothing: only Windows reads batteries in a helper.
func runHelper(io.Writer) {}
