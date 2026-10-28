package publishing

import (
	"errors"
)

var ErrUnavailable = errors.New("social platform approval and account authorization required")
