package application

import "ground-clock-qualification/internal/domain"

// EnvironmentCorrelation returns a read-only environment/deviation analysis for a campaign.
func (s *Service) EnvironmentCorrelation(id, deviceID, environmentField, deviationMetric string) (domain.EnvironmentCorrelation, error) {
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
	return result, nil
}
