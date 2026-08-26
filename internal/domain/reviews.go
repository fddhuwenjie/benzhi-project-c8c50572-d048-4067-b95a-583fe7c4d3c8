package domain

// ReviewDecision is the normalized decision vocabulary used by review records.
type ReviewDecision string

const (
	ReviewDecisionApprove ReviewDecision = "APPROVE"
	ReviewDecisionReturn  ReviewDecision = "RETURN"
)
