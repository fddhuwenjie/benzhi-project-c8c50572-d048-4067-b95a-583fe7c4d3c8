package persistence

// lockMode expresses the serialized command access policy.
type lockMode string

const lockModeCampaign lockMode = "campaign"
