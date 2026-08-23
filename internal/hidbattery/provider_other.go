//go:build !darwin

package hidbattery

import "context"

type platformProvider struct{}

func newPlatformProvider() Provider { return platformProvider{} }
func (platformProvider) Batteries(context.Context) ([]Observation, error) {
	return nil, nil
}
