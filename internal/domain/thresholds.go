package domain

// ThresholdValid reports whether all configured limits are finite and positive.
func (t ThresholdProfile) ThresholdValid() bool {
	return positiveFinite(t.MaxAbsDeviation) && positiveFinite(t.MaxFrequencyDeviation) && positiveFinite(t.MaxDriftSlope)
}
