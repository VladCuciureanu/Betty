package domain

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	CodeUsage          ErrorCode = "usage"
	CodeNotFound       ErrorCode = "device_not_found"
	CodeAmbiguous      ErrorCode = "device_ambiguous"
	CodeUnsupported    ErrorCode = "unsupported"
	CodePermission     ErrorCode = "permission_denied"
	CodeUnavailable    ErrorCode = "bluetooth_unavailable"
	CodeAuthentication ErrorCode = "authentication_failed"
	CodeTimeout        ErrorCode = "timeout"
	CodeInternal       ErrorCode = "internal"
)

type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Details any       `json:"details,omitempty"`
	Cause   error     `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

func NewError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeInternal, Message: err.Error(), Cause: err}
}

func ExitCode(err error) int {
	switch AsError(err).Code {
	case CodeUsage:
		return 2
	case CodeNotFound:
		return 3
	case CodeAmbiguous:
		return 4
	case CodeUnsupported:
		return 5
	case CodePermission:
		return 6
	case CodeUnavailable:
		return 7
	case CodeAuthentication:
		return 8
	case CodeTimeout:
		return 9
	default:
		return 1
	}
}
