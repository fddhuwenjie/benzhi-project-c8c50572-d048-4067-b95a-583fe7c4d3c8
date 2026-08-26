package httpapi

// ErrorEnvelope is the JSON shape used for protocol errors.
type ErrorEnvelope struct {
	Error string `json:"error"`
}
