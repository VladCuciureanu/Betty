//go:build !linux && !darwin && !windows

package rfcomm

import (
	"github.com/vladcuciureanu/betty/internal/driver"
)

func New() driver.RFCOMMHost { return driver.UnsupportedRFCOMMHost{} }
