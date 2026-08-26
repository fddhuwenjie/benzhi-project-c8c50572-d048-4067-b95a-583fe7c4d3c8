package application

// HealthStatus is returned by readiness-oriented callers.
type HealthStatus struct {
	Status string `json:"status"`
}
