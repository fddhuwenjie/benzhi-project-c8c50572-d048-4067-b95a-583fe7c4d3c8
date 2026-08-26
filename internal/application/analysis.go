package application

import (
	"ground-clock-qualification/internal/domain"
	"sort"
	"strings"
	"time"
)

func (s *Service) ReferenceResilience(id, kind string) (domain.ReferenceResilience, error) {
	c, err := s.get(id)
	if err != nil {
		return domain.ReferenceResilience{}, err
	}
	refs, err := s.Store.References(id)
	if err != nil {
		return domain.ReferenceResilience{}, err
	}
	ws, err := s.Store.ReferenceWithdrawals(id)
	if err != nil {
		return domain.ReferenceResilience{}, err
	}
	return domain.BuildReferenceResilience(c, refs, ws, strings.TrimSpace(kind))
}

func (s *Service) RemediationEffectiveness(id, deviceID, metric, status string) (domain.RemediationEffectiveness, error) {
	c, err := s.get(id)
	if err != nil {
		return domain.RemediationEffectiveness{}, err
	}
	ds, err := s.Store.Deviations(id)
	if err != nil {
		return domain.RemediationEffectiveness{}, err
	}
	plans, err := s.Store.Plans(id)
	if err != nil {
		return domain.RemediationEffectiveness{}, err
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return domain.RemediationEffectiveness{}, err
	}
	evidence, err := s.Store.RemediationEvidence(id)
	if err != nil {
		return domain.RemediationEffectiveness{}, err
	}
	return domain.BuildRemediationEffectiveness(c, ds, plans, rounds, evidence, strings.TrimSpace(deviceID), strings.TrimSpace(metric), strings.TrimSpace(status)), nil
}

type ReviewerEligibility struct {
	ReviewerID string             `json:"reviewer_id"`
	Status     string             `json:"status"`
	Conflicts  []ReviewerConflict `json:"conflicts,omitempty"`
}
type ReviewerConflict struct {
	RoundID    string    `json:"round_id"`
	Purpose    string    `json:"purpose"`
	OperatorID string    `json:"operator_id"`
	CapturedAt time.Time `json:"captured_at"`
}
type ReviewerEligibilityResult struct {
	AnalyzedRevision int64                 `json:"analyzed_revision"`
	Candidates       []ReviewerEligibility `json:"candidates"`
}

func (s *Service) ReviewerEligibility(id string, reviewerIDs []string) (*ReviewerEligibilityResult, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if len(reviewerIDs) == 0 || len(reviewerIDs) > 50 {
		return nil, domain.ErrInvalid
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, id := range reviewerIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return nil, domain.ErrInvalid
		}
		seen[id] = true
		ids = append(ids, id)
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return nil, err
	}
	by := map[string][]ReviewerConflict{}
	for _, r := range rounds {
		for _, rid := range ids {
			if r.OperatorID == rid {
				p := r.Purpose
				if p == "" {
					p = "original"
				}
				by[rid] = append(by[rid], ReviewerConflict{r.RoundID, p, r.OperatorID, r.CapturedAt.UTC()})
			}
		}
	}
	now := time.Now().UTC()
	claim, ce := s.Store.CurrentReviewClaim(id)
	occupied := ce == nil && claim.Status == "ACTIVE" && now.Before(claim.ExpiresAt)
	out := &ReviewerEligibilityResult{AnalyzedRevision: c.Revision, Candidates: []ReviewerEligibility{}}
	for _, rid := range ids {
		status := "ELIGIBLE"
		if c.State != domain.ReviewPending {
			status = "CAMPAIGN_NOT_REVIEWABLE"
		} else if len(by[rid]) > 0 {
			status = "INDEPENDENCE_CONFLICT"
		} else if occupied && claim.ReviewerID != rid {
			status = "CLAIM_OCCUPIED"
		}
		sort.Slice(by[rid], func(i, j int) bool { return by[rid][i].RoundID < by[rid][j].RoundID })
		out.Candidates = append(out.Candidates, ReviewerEligibility{rid, status, by[rid]})
	}
	sort.Slice(out.Candidates, func(i, j int) bool { return out.Candidates[i].ReviewerID < out.Candidates[j].ReviewerID })
	return out, nil
}

func (s *Service) ReviewerEligibilityPreflight(id string, reviewerIDs []string) (*ReviewerEligibilityResult, error) {
	return s.ReviewerEligibility(id, reviewerIDs)
}
