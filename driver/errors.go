package driver

import (
	"errors"
	"fmt"
)

// Error is a structured failure. Handlers return it to give Betty a stable
// error code; Serve forwards Code, Message, and Details to the client.
type Error struct {
	Code    ErrorCode
	Message string
	Details any
	cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }

// Errorf returns an *Error with the given code and formatted message.
func Errorf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap converts err into an *Error. A code and details carried by err, either
// from an *Error or from a failed host call, are preserved; otherwise fallback
// is used. Wrap returns nil when err is nil.
func Wrap(err error, fallback ErrorCode) *Error {
	if err == nil {
		return nil
	}
	code, details := CodeOf(err, fallback)
	return &Error{Code: code, Message: err.Error(), Details: details, cause: err}
}

// CodeOf reports the code and details carried by err, or fallback when err
// carries none.
func CodeOf(err error, fallback ErrorCode) (ErrorCode, any) {
	var structured *Error
	if errors.As(err, &structured) && structured.Code != "" {
		return structured.Code, structured.Details
	}
	var remote *RemoteError
	if errors.As(err, &remote) && remote.Code != "" {
		return ErrorCode(remote.Code), remote.Details
	}
	return fallback, nil
}
