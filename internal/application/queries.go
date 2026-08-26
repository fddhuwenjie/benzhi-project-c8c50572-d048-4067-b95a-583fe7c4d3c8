package application

// QueryContext carries read pagination metadata.
type QueryContext struct {
	Offset int
	Limit  int
}
