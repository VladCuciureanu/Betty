//go:build !darwin && !linux && !windows

package l2cap

import "github.com/vladcuciureanu/betty/internal/driver"

func New() driver.Host { return driver.UnsupportedHost{} }
