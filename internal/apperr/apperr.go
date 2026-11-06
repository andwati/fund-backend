// Package apperr is the error type handlers turn into HTTP responses. It mirrors the web
// app's HttpError(status, message).
package apperr

import (
	"errors"
	"fmt"
)

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

func New(status int, msg string) *Error { return &Error{Status: status, Message: msg} }

func Newf(status int, format string, args ...any) *Error {
	return &Error{Status: status, Message: fmt.Sprintf(format, args...)}
}

// As extracts an *Error from err, if any.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

var (
	ErrUnauthorized = func() *Error { return New(401, "Sign in required.") }
)
