//go:build linux

package l2cap

import "time"

func timeUntil(deadline time.Time) time.Duration { return time.Until(deadline) }
