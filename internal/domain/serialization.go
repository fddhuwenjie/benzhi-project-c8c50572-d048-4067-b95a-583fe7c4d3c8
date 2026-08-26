package domain

import "encoding/json"

// MarshalStable delegates to encoding/json for canonical structure projection.
func MarshalStable(value any) ([]byte, error) { return json.Marshal(value) }
