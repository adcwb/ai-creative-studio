package media

import (
	"errors"
	"fmt"
)

var ErrInput = errors.New("invalid media input")

func Invalid(message string) error { return fmt.Errorf("%w: %s", ErrInput, message) }

var ErrVersion = errors.New("media version conflict")

var ErrKey = errors.New("media idempotency conflict")

var ErrLimit = errors.New("media resource limit")
