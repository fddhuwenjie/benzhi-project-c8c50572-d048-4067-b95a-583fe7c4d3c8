package application

import "strings"

// ValidRequestID rejects blank idempotency identifiers.
func ValidRequestID(id string) bool { return strings.TrimSpace(id) != "" }
