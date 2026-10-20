//go:build !linux

package ble

import "github.com/vladcuciureanu/betty/internal/domain"

func platformCode(error) (domain.ErrorCode, bool) { return "", false }
