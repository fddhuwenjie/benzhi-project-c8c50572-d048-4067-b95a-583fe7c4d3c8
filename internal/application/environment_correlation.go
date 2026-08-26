package application

import "ground-clock-qualification/internal/domain"

func environmentCorrelationKey(id, deviceID, environmentField, deviationMetric string) string {
	return id + "\x00" + deviceID + "\x00" + environmentField + "\x00" + deviationMetric
}

func cloneEnvironmentCorrelation(in domain.EnvironmentCorrelation) domain.EnvironmentCorrelation {
	out := in
	out.Results = append([]domain.EnvironmentCorrelationItem(nil), in.Results...)
	for i := range out.Results {
		out.Results[i].Issues = append([]domain.EnvironmentCorrelationIssue(nil), in.Results[i].Issues...)
	}
	return out
}

// EnvironmentCorrelation returns a read-only environment/deviation analysis for a campaign.
func (s *Service) EnvironmentCorrelation(id, deviceID, environmentField, deviationMetric string) (domain.EnvironmentCorrelation, error) {
	key := environmentCorrelationKey(id, deviceID, environmentField, deviationMetric)
	s.environmentCorrelationMu.RLock()
	cached, ok := s.environmentCorrelationCache[key]
	s.environmentCorrelationMu.RUnlock()
	if ok {
		return cloneEnvironmentCorrelation(cached), nil
	}
	c, err := s.get(id)
	if err != nil {
		return domain.EnvironmentCorrelation{}, err
	}
	switch c.State {
	case domain.Measured, domain.RemediationRequired, domain.ReviewPending, domain.ReviewApproved, domain.Archived:
	default:
		return domain.EnvironmentCorrelation{}, domain.ErrState
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return domain.EnvironmentCorrelation{}, err
	}
	voids, err := s.Store.RoundVoids(id)
	if err != nil {
		return domain.EnvironmentCorrelation{}, err
	}
	exclusions, err := s.Store.SampleExclusions(id)
	if err != nil {
		return domain.EnvironmentCorrelation{}, err
	}
	result, err := domain.BuildEnvironmentCorrelation(c, domain.EffectiveRoundsWithExclusions(rounds, voids, exclusions), deviceID, environmentField, deviationMetric)
	if err != nil {
		return domain.EnvironmentCorrelation{}, err
	}
	s.environmentCorrelationMu.Lock()
	s.environmentCorrelationCache[key] = cloneEnvironmentCorrelation(result)
	s.environmentCorrelationMu.Unlock()
	return result, nil
}
