package persistence

// queryName documents stable read query identifiers.
type queryName string

const (
	queryCampaign queryName = "campaign"
	queryAudit    queryName = "audit"
)
