package store

import "errors"

var (
	ErrNotFound          = errors.New("record not found")
	ErrConflict          = errors.New("record conflict")
	ErrInvalidTransition = errors.New("invalid investigation transition")
	ErrInvalidInput      = errors.New("invalid store input")
)
