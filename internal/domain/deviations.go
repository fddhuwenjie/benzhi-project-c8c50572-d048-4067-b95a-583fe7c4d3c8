package domain

// Closed reports whether a deviation has a successful retest.
func (d DeviationCase) Closed() bool { return d.Status == "CLOSED" || d.Status == "RESOLVED" }
