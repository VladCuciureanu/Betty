//go:build !linux

package ble

// watchLinkLoss is a no-op where tinygo reports disconnects through the
// adapter's connect handler (macOS and Windows).
func watchLinkLoss(string, func()) func() { return func() {} }
