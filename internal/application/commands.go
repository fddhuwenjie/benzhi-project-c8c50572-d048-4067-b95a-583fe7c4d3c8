package application

import "errors"

// CommandContext carries metadata common to mutating use cases.
type CommandContext struct {
	RequestID        string
	ExpectedRevision int64
	Actor            string
}

// ErrRequestCancelled is returned when a mutating use case is invoked with a
// request context that has already been cancelled. Returning it before any
// persistence work begins guarantees that no campaign, audit or idempotency
// records are written for a request that the caller has already abandoned, so
// a later retry of the same request is not mistaken for an idempotent replay.
var ErrRequestCancelled = errors.New("request cancelled")
