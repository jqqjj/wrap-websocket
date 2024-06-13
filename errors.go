package wrap

import "errors"

var (
	ErrDuplicate = errors.New("duplicate write")
)
