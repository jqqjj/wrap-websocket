package wrap

import "errors"

var (
	ErrCanceled  = errors.New("canceled")
	ErrTimeout   = errors.New("timeout")
	ErrJsonParse = errors.New("json error")
)
