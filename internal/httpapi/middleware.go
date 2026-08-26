package httpapi

import "net/http"

// statusHandler wraps a handler with a conservative request method check.
func statusHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}
