//go:build !linux

package ble

import "github.com/vladcuciureanu/betty/internal/domain"

// UseAdapter is unsupported: only Linux has several adapters to choose from.
func (b *TinyGo) UseAdapter(string) error {
	return domain.NewError(domain.CodeUnsupported, "only Linux can choose a Bluetooth adapter")
}
