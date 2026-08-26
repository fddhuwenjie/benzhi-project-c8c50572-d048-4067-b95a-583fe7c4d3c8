package domain

// CanWrite reports whether business commands may mutate a campaign.
func (c Campaign) CanWrite() bool { return !c.State.IsTerminal() }
