package domain

import (
	"errors"
	"time"
)

var (
	ErrMissingTime      = errors.New("time is required")
	ErrInvalidTimeRange = errors.New("time range is inverted")
)

// NormalizeTime converts a time to UTC while preserving the represented instant.
func NormalizeTime(value time.Time) time.Time {
	return value.UTC()
}

// ValidateTimeRange rejects missing and inverted time windows. A zero-duration
// window is valid because some observations represent a single instant.
func ValidateTimeRange(start, end time.Time) error {
	if start.IsZero() || end.IsZero() {
		return ErrMissingTime
	}
	if end.Before(start) {
		return ErrInvalidTimeRange
	}
	return nil
}
