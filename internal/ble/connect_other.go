//go:build !linux

package ble

import "context"

// preconnect has nothing to do outside Linux: tinygo's Connect there doesn't
// block past the call that makes the connection.
func preconnect(context.Context, string) error { return nil }
