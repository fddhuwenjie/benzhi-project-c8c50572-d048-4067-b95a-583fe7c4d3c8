package domain

// ClaimOutcome describes a review claim outcome without changing state semantics.
type ClaimOutcome string

const (
	ClaimOutcomePending  ClaimOutcome = "PENDING"
	ClaimOutcomeApproved ClaimOutcome = "APPROVED"
)
