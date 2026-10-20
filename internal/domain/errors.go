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
	CodeInteraction    ErrorCode = "interaction_required"
	CodeDisconnected   ErrorCode = "device_disconnected"
	CodeDriverRequired ErrorCode = "driver_required"
	CodeTimeout        ErrorCode = "timeout"
	CodeCanceled       ErrorCode = "canceled"
	CodeInternal       ErrorCode = "internal"
)

type Error struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
	Details   any       `json:"details,omitempty"`
	Cause     error     `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

func NewError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Retryable: retryable(code)}
}

// AsError returns err as an *Error, classifying anything else as internal.
// It returns nil for a nil error.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeInternal, Message: err.Error(), Cause: err}
}

func retryable(code ErrorCode) bool {
	switch code {
	case CodeUnavailable, CodeDisconnected, CodeTimeout:
		return true
	default:
		return false
	}
}

// ExitCode maps err to the process exit status. A nil error exits 0.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	switch AsError(err).Code {
	case CodeUsage:
		return 2
	case CodeNotFound:
		return 3
	case CodeAmbiguous:
		return 4
	case CodeUnsupported, CodeDriverRequired:
		return 5
	case CodePermission:
		return 6
	case CodeUnavailable, CodeDisconnected:
		return 7
	case CodeAuthentication, CodeInteraction:
		return 8
	case CodeTimeout:
		return 9
	case CodeCanceled:
		// The shell convention for termination by SIGINT.
		return 130
	default:
		return 1
	}
}
