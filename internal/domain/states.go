package domain

// IsTerminal reports whether a campaign can no longer transition.
func (s State) IsTerminal() bool { return s == Archived || s == Cancelled }
