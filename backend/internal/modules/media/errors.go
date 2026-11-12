package media

import (
	"errors"
)

var ErrInput = errors.New("invalid media input")

type inputError struct{ message string }

func (e *inputError) Error() string { return ErrInput.Error() + ": " + e.message }
func (*inputError) Unwrap() error   { return ErrInput }
func Invalid(message string) error  { return &inputError{message: message} }

// InputMessage exposes only the domain's controlled validation message, never
// the text of an arbitrary wrapper (which may contain upstream private data).
func InputMessage(err error) string {
	var e *inputError
	if errors.As(err, &e) {
		return e.Error()
	}
	return ErrInput.Error()
}

var ErrVersion = errors.New("media version conflict")

var ErrKey = errors.New("media idempotency conflict")

var ErrLimit = errors.New("media resource limit")
