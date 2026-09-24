package server

import (
	"errors"

	"github.com/go-feature-flag/studio/internal/storage"
)

var (
	ErrForbidden = errors.New("you do not have permission to do that")
	ErrNotFound  = errors.New("not found")
)

var errStorageConflict = storage.ErrConflict

var ErrStaleView = errors.New("your view of this flag is out of date")

var ErrInvalid = errors.New("invalid request")
