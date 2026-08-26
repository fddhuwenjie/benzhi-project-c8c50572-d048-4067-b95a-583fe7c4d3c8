package domain

import "time"

// ValidAt reports whether evidence covers an instant.
func (e ReferenceEvidence) ValidAt(at time.Time) bool {
	return !at.Before(e.ValidFrom) && at.Before(e.ValidUntil)
}
