package domain

import "strings"

// HasText is shared validation for identifiers and operator names.
func HasText(value string) bool { return strings.TrimSpace(value) != "" }
