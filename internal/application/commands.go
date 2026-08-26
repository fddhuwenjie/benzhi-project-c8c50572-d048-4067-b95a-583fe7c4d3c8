package application

// CommandContext carries metadata common to mutating use cases.
type CommandContext struct {
	RequestID        string
	ExpectedRevision int64
	Actor            string
}
