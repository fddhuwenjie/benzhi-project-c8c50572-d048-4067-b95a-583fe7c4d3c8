package application

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type SuccessorInput struct {
	CampaignID         string    `json:"campaign_id"`
	MissionWindowStart time.Time `json:"mission_window_start"`
	MissionWindowEnd   time.Time `json:"mission_window_end"`
	CreatedBy          string    `json:"created_by"`
}

func (s *Service) CreateSuccessor(sourceID, requestID string, in SuccessorInput) (*domain.Campaign, error) {
	if requestID == "" {
		return nil, domain.ErrInvalid
	}
	hash, key := requestHash(in), idemKey("campaign-successor", sourceID, requestID)
	var old domain.Campaign
	if ok, err := s.replay(key, hash, &old); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return &old, nil
	}
	source, err := s.get(sourceID)
	if err != nil {
		return nil, err
	}
	if source.State != domain.Archived {
		return nil, domain.ErrState
	}
	artifact, err := s.Store.GetArtifact(sourceID)
	if err != nil {
		return nil, err
	}
	verification := audit.VerifyArtifactPayload(artifact.Payload, "")
	if !verification.Valid || artifact.PayloadDigest != verification.PayloadDigest {
		return nil, domain.ErrIntegrity
	}
	var payload audit.ArtifactPayload
	var sealed []domain.Campaign
	if json.Unmarshal(artifact.Payload, &payload) != nil || json.Unmarshal(payload.Sections["campaign"], &sealed) != nil || len(sealed) != 1 || sealed[0].State != domain.Archived || requestHash(sealed[0]) != requestHash(*source) {
		return nil, domain.ErrIntegrity
	}
	events, err := s.Store.Audits(sourceID)
	if err != nil {
		return nil, err
	}
	report := audit.ValidateDetailed(events, source.Revision)
	if !report.Valid || report.HeadDigest != artifact.AuditHeadDigest {
		return nil, domain.ErrIntegrity
	}
	source = &sealed[0]
	c, err := domain.NewCampaign(in.CampaignID, source.StationCode, in.MissionWindowStart, in.MissionWindowEnd, source.DeviceIDs, source.Threshold, strings.TrimSpace(in.CreatedBy), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	c.MeasurementPlan, c.MeasurementPlanLocked = source.MeasurementPlan, source.MeasurementPlanLocked
	c.PredecessorCampaignID = sourceID
	summary := map[string]any{"campaign_id": sourceID, "artifact_digest": artifact.PayloadDigest, "audit_head_digest": artifact.AuditHeadDigest, "archived_revision": source.Revision}
	b, _ := json.Marshal(summary)
	c.PredecessorSummary = string(b)
	event := audit.NewEventWithSummary(c.CampaignID, 1, "CREATE_SUCCESSOR", c.CreatedBy, "", c.PredecessorSummary, time.Now().UTC())
	conflicts, err := s.Store.CreateCampaignAtomic(c, event, key, hash)
	if errors.Is(err, persistence.ErrResourceConflict) {
		return nil, &domain.ConflictError{Conflicts: conflicts}
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, domain.ErrAlreadyExists
		}
		return nil, err
	}
	return c, nil
}

type CancelInput struct {
	CancelledBy      string `json:"cancelled_by"`
	ReasonCode       string `json:"reason_code"`
	Reason           string `json:"reason"`
	RequestID        string `json:"request_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func (s *Service) CancelCampaign(id, idem string, in CancelInput) (*domain.Campaign, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	hash, key := requestHash(in), idemKey("campaign-cancel", id, idem)
	var old domain.Campaign
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		if e != nil {
			return nil, e
		}
		return &old, nil
	}
	if idem == "" || in.RequestID == "" {
		return nil, domain.ErrInvalid
	}
	if in.ExpectedRevision > 0 && c.Revision != in.ExpectedRevision {
		return nil, domain.ErrConflict
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return nil, err
	}
	if err = domain.ValidateCancellation(c, len(rounds), in.ReasonCode, in.Reason, in.CancelledBy, in.RequestID); err != nil {
		return nil, err
	}
	c.Revision++
	c.State = domain.Cancelled
	c.Cancellation = &domain.Cancellation{ReasonCode: in.ReasonCode, Reason: strings.TrimSpace(in.Reason), CancelledBy: strings.TrimSpace(in.CancelledBy), CancelledAt: time.Now().UTC(), RequestID: in.RequestID}
	event, err := s.newEvent(c, "CAMPAIGN_CANCELLED", c.Cancellation.CancelledBy, requestHash(c.Cancellation))
	if err != nil {
		return nil, err
	}
	if err = s.Store.Commit(persistence.Mutation{Campaign: c, Event: &event, IdemKey: key, IdemHash: hash, Response: c}); err != nil {
		return nil, err
	}
	return c, nil
}

type WithdrawalInput struct {
	EvidenceID       string `json:"evidence_id"`
	ReasonCode       string `json:"reason_code"`
	Reason           string `json:"reason"`
	WithdrawnBy      string `json:"withdrawn_by"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type WithdrawalResult struct {
	Campaign   *domain.Campaign           `json:"campaign"`
	Withdrawal domain.ReferenceWithdrawal `json:"withdrawal"`
	Coverage   domain.ReferenceCoverage   `json:"coverage"`
}

func (s *Service) WithdrawReference(id, requestID string, in WithdrawalInput) (*WithdrawalResult, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	hash, key := requestHash(in), idemKey("reference-withdrawal", id, requestID)
	var old WithdrawalResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if requestID == "" {
		return nil, domain.ErrInvalid
	}
	if in.ExpectedRevision > 0 && c.Revision != in.ExpectedRevision {
		return nil, domain.ErrConflict
	}
	if c.State != domain.Draft && c.State != domain.ReferenceVerified {
		return nil, domain.ErrState
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return nil, err
	}
	if len(rounds) > 0 {
		return nil, domain.ErrState
	}
	if !reasonCodePattern.MatchString(in.ReasonCode) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 5 || strings.TrimSpace(in.WithdrawnBy) == "" {
		return nil, domain.ErrInvalid
	}
	refs, err := s.Store.References(id)
	if err != nil {
		return nil, err
	}
	found, replaced := false, false
	for _, r := range refs {
		if r.EvidenceID == in.EvidenceID {
			found, replaced = true, r.Replaced
		}
	}
	if !found {
		return nil, sql.ErrNoRows
	}
	if replaced {
		return nil, domain.ErrState
	}
	withdrawals, err := s.Store.ReferenceWithdrawals(id)
	if err != nil {
		return nil, err
	}
	for _, w := range withdrawals {
		if w.EvidenceID == in.EvidenceID {
			return nil, domain.ErrDuplicate
		}
	}
	c.Revision++
	w := domain.ReferenceWithdrawal{CampaignID: id, EvidenceID: in.EvidenceID, ReasonCode: in.ReasonCode, Reason: strings.TrimSpace(in.Reason), WithdrawnBy: strings.TrimSpace(in.WithdrawnBy), WithdrawnAt: time.Now().UTC(), Revision: c.Revision}
	coverage := c.ReferenceCoverage(domain.EffectiveReferences(refs, append(withdrawals, w)))
	if coverage.Complete {
		c.State = domain.ReferenceVerified
	} else {
		c.State = domain.Draft
	}
	result := &WithdrawalResult{c, w, coverage}
	event, err := s.newEvent(c, "REFERENCE_WITHDRAWN", w.WithdrawnBy, requestHash(result))
	if err != nil {
		return nil, err
	}
	if err = s.Store.Commit(persistence.Mutation{Campaign: c, ReferenceWithdrawals: []domain.ReferenceWithdrawal{w}, Event: &event, IdemKey: key, IdemHash: hash, Response: result}); err != nil {
		return nil, err
	}
	return result, nil
}

type SimulationDevice struct {
	DeviceID            string                     `json:"device_id"`
	BaselineConclusion  string                     `json:"baseline_conclusion"`
	CandidateConclusion string                     `json:"candidate_conclusion"`
	Change              string                     `json:"change"`
	Metrics             []domain.MetricAttribution `json:"metrics"`
	NearestMetric       string                     `json:"nearest_metric"`
	NearestMargin       float64                    `json:"nearest_margin"`
}
type SimulationResult struct {
	LockedThreshold    domain.ThresholdProfile `json:"locked_threshold"`
	CandidateThreshold domain.ThresholdProfile `json:"candidate_threshold"`
	AlgorithmVersion   string                  `json:"algorithm_version"`
	Devices            []SimulationDevice      `json:"devices"`
	Passed             int                     `json:"passed"`
	Total              int                     `json:"total"`
}

func (s *Service) SimulateEvaluation(id string, candidate domain.ThresholdProfile, version string) (*SimulationResult, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if c.State == domain.Draft || c.State == domain.ReferenceVerified || c.State == domain.Archived || c.State == domain.Cancelled {
		return nil, domain.ErrState
	}
	if !positiveThreshold(candidate) {
		return nil, domain.ErrInvalid
	}
	if version == "" {
		version = "timesync-eval-v2"
	}
	if version != "timesync-eval-v2" {
		return nil, errors.New("unknown algorithm version")
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return nil, err
	}
	voids, err := s.Store.RoundVoids(id)
	if err != nil {
		return nil, err
	}
	exclusions, _ := s.Store.SampleExclusions(id)
	rounds = effectiveWithoutExclusions(rounds, voids, exclusions)
	completion, _ := domain.MeasurementPlanCompliance(c, rounds)
	if !completion.Complete {
		return nil, domain.ErrCoverage
	}
	baseProbe, candidateProbe := *c, *c
	baseProbe.State, candidateProbe.State = domain.Measured, domain.Measured
	candidateProbe.Threshold = candidate
	base, _, err := baseProbe.BuildEvaluation(rounds, time.Unix(0, 0).UTC())
	if err != nil {
		return nil, err
	}
	sim, _, err := candidateProbe.BuildEvaluation(rounds, time.Unix(0, 0).UTC())
	if err != nil {
		return nil, err
	}
	out := &SimulationResult{LockedThreshold: c.Threshold, CandidateThreshold: candidate, AlgorithmVersion: version, Devices: []SimulationDevice{}, Total: len(sim.Devices)}
	baseBy := map[string]string{}
	for _, d := range base.Devices {
		baseBy[d.DeviceID] = d.Conclusion
	}
	metricBy := map[string][]domain.MetricAttribution{}
	for _, m := range sim.Metrics {
		metricBy[m.DeviceID] = append(metricBy[m.DeviceID], m)
	}
	for _, d := range sim.Devices {
		change := "UNCHANGED"
		if baseBy[d.DeviceID] == "PASS" && d.Conclusion == "FAIL" {
			change = "PASS_TO_FAIL"
		} else if baseBy[d.DeviceID] == "FAIL" && d.Conclusion == "PASS" {
			change = "FAIL_TO_PASS"
		}
		nearest, margin := "", math.MaxFloat64
		for _, m := range metricBy[d.DeviceID] {
			if math.Abs(m.Margin) < math.Abs(margin) {
				nearest, margin = m.Metric, m.Margin
			}
		}
		if d.Conclusion == "PASS" {
			out.Passed++
		}
		out.Devices = append(out.Devices, SimulationDevice{d.DeviceID, baseBy[d.DeviceID], d.Conclusion, change, metricBy[d.DeviceID], nearest, margin})
	}
	sort.Slice(out.Devices, func(i, j int) bool { return out.Devices[i].DeviceID < out.Devices[j].DeviceID })
	return out, nil
}
func positiveThreshold(t domain.ThresholdProfile) bool {
	return t.MaxAbsDeviation > 0 && t.MaxFrequencyDeviation > 0 && t.MaxDriftSlope > 0 && !math.IsNaN(t.MaxAbsDeviation) && !math.IsInf(t.MaxAbsDeviation, 0) && !math.IsNaN(t.MaxFrequencyDeviation) && !math.IsInf(t.MaxFrequencyDeviation, 0) && !math.IsNaN(t.MaxDriftSlope) && !math.IsInf(t.MaxDriftSlope, 0)
}

type QueueFilter struct {
	StationCode, DeviceID, Metric, Owner, RiskStatus string
	Offset, Limit                                    int
}
type QueueItem struct {
	CampaignID        string    `json:"campaign_id"`
	StationCode       string    `json:"station_code"`
	DeviationID       string    `json:"deviation_id"`
	DeviceID          string    `json:"device_id"`
	Metric            string    `json:"metric"`
	ObservedValue     float64   `json:"observed_value"`
	LimitValue        float64   `json:"limit_value"`
	Owner             string    `json:"owner,omitempty"`
	PlanVersion       int       `json:"plan_version"`
	TargetAt          time.Time `json:"target_at,omitempty"`
	FailedRetestCount int       `json:"failed_retest_count"`
	NextAction        string    `json:"next_action"`
	RiskStatus        string    `json:"risk_status"`
}
type QueueResult struct {
	Items       []QueueItem    `json:"items"`
	Total       int            `json:"total"`
	RiskCounts  map[string]int `json:"risk_counts"`
	EvaluatedAt time.Time      `json:"evaluated_at"`
	Offset      int            `json:"offset"`
	Limit       int            `json:"limit"`
}

func (s *Service) RemediationQueue(q QueueFilter, now time.Time) (*QueueResult, error) {
	if q.Offset < 0 || q.Limit < 1 || q.Limit > 100 {
		return nil, domain.ErrInvalid
	}
	now = now.UTC()
	all := []QueueItem{}
	counts := map[string]int{"UNPLANNED": 0, "IN_PROGRESS": 0, "DUE_SOON": 0, "OVERDUE": 0}
	campaigns, err := s.Store.ListCampaigns()
	if err != nil {
		return nil, err
	}
	for _, c := range campaigns {
		if c.State != domain.RemediationRequired || q.StationCode != "" && c.StationCode != q.StationCode {
			continue
		}
		deviations, e := s.Store.Deviations(c.CampaignID)
		if e != nil {
			return nil, e
		}
		plans, e := s.Store.Plans(c.CampaignID)
		if e != nil {
			return nil, e
		}
		latest := map[string]domain.RemediationPlan{}
		for _, p := range plans {
			if p.Version >= latest[p.DeviationID].Version {
				latest[p.DeviationID] = p
			}
		}
		for _, d := range deviations {
			if d.Status != "OPEN" {
				continue
			}
			p, planned := latest[d.DeviationID]
			riskStatus := "UNPLANNED"
			if planned {
				riskStatus = "IN_PROGRESS"
				if p.TargetAt.Before(now) {
					riskStatus = "OVERDUE"
				} else if !p.TargetAt.After(now.Add(24 * time.Hour)) {
					riskStatus = "DUE_SOON"
				}
			}
			failed := 0
			for _, a := range d.Attempts {
				if a.Result == "FAIL" {
					failed++
				}
			}
			next := "READY_FOR_RETEST"
			if !planned {
				next = "MISSING_PLAN"
			} else if strings.TrimSpace(p.RootCause) == "" || strings.TrimSpace(p.Containment) == "" || strings.TrimSpace(p.CorrectiveAction) == "" {
				next = "MATERIAL_INCOMPLETE"
			} else if failed == 0 {
				next = "RETEST_REQUIRED"
			}
			item := QueueItem{c.CampaignID, c.StationCode, d.DeviationID, d.DeviceID, d.Metric, d.ObservedValue, d.LimitValue, p.Owner, p.Version, p.TargetAt, failed, next, riskStatus}
			if q.DeviceID != "" && item.DeviceID != q.DeviceID || q.Metric != "" && item.Metric != q.Metric || q.Owner != "" && item.Owner != q.Owner || q.RiskStatus != "" && item.RiskStatus != q.RiskStatus {
				continue
			}
			all = append(all, item)
			counts[riskStatus]++
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CampaignID != all[j].CampaignID {
			return all[i].CampaignID < all[j].CampaignID
		}
		return all[i].DeviationID < all[j].DeviationID
	})
	total := len(all)
	start := q.Offset
	if start > total {
		start = total
	}
	end := start + q.Limit
	if end > total {
		end = total
	}
	return &QueueResult{all[start:end], total, counts, now, q.Offset, q.Limit}, nil
}

type ResolutionInput struct {
	FindingID       string `json:"finding_id"`
	Resolution      string `json:"resolution"`
	ResolvedBy      string `json:"resolved_by"`
	EvidenceSummary string `json:"evidence_summary"`
	RetestRoundID   string `json:"retest_round_id,omitempty"`
}
type ResolutionRequest struct {
	Resolutions      []ResolutionInput `json:"resolutions"`
	ExpectedRevision int64             `json:"expected_revision"`
}
type ResolutionResult struct {
	Campaign            *domain.Campaign           `json:"campaign"`
	Resolutions         []domain.FindingResolution `json:"resolutions"`
	RemainingFindingIDs []string                   `json:"remaining_finding_ids"`
}

func (s *Service) ResolveReviewFindings(id, requestID string, in ResolutionRequest) (*ResolutionResult, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	hash, key := requestHash(in), idemKey("review-finding-resolution", id, requestID)
	var old ResolutionResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if requestID == "" {
		return nil, domain.ErrInvalid
	}
	if in.ExpectedRevision > 0 && c.Revision != in.ExpectedRevision {
		return nil, domain.ErrConflict
	}
	if c.State != domain.RemediationRequired || len(in.Resolutions) == 0 {
		return nil, domain.ErrState
	}
	findings, err := s.Store.ReviewFindings(id)
	if err != nil {
		return nil, err
	}
	existing, err := s.Store.FindingResolutions(id)
	if err != nil {
		return nil, err
	}
	resolved := map[string]bool{}
	for _, x := range existing {
		resolved[x.FindingID] = true
	}
	by := map[string]domain.ReviewFinding{}
	for _, f := range findings {
		by[f.FindingID] = f
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return nil, err
	}
	roundBy := map[string]domain.MeasurementRound{}
	for _, r := range rounds {
		roundBy[r.RoundID] = r
	}
	seen := map[string]bool{}
	out := []domain.FindingResolution{}
	for _, x := range in.Resolutions {
		f, ok := by[x.FindingID]
		if !ok {
			return nil, sql.ErrNoRows
		}
		if resolved[x.FindingID] || seen[x.FindingID] {
			return nil, domain.ErrDuplicate
		}
		seen[x.FindingID] = true
		if strings.TrimSpace(x.Resolution) == "" || strings.TrimSpace(x.ResolvedBy) == "" || utf8.RuneCountInString(strings.TrimSpace(x.EvidenceSummary)) < 5 {
			return nil, domain.ErrInvalid
		}
		if f.RequiresRetest {
			r, ok := roundBy[x.RetestRoundID]
			if !ok || r.Purpose != "retest" || !r.CapturedAt.After(f.CreatedAt) {
				return nil, domain.ErrInvalid
			}
			passed := false
			for _, sample := range r.Samples {
				if (f.DeviceID == "" || sample.DeviceID == f.DeviceID) && math.Abs(sample.TimeOffset) <= c.Threshold.MaxAbsDeviation && math.Abs(sample.FrequencyOffset) <= c.Threshold.MaxFrequencyDeviation {
					passed = true
				}
			}
			if !passed {
				return nil, domain.ErrInvalid
			}
		} else if x.RetestRoundID != "" {
			return nil, domain.ErrInvalid
		}
		out = append(out, domain.FindingResolution{FindingID: x.FindingID, Resolution: strings.TrimSpace(x.Resolution), ResolvedBy: strings.TrimSpace(x.ResolvedBy), EvidenceSummary: strings.TrimSpace(x.EvidenceSummary), RetestRoundID: x.RetestRoundID, ResolvedAt: time.Now().UTC()})
	}
	c.Revision++
	for i := range out {
		out[i].Revision = c.Revision
		resolved[out[i].FindingID] = true
	}
	remaining := []string{}
	for _, f := range findings {
		if !resolved[f.FindingID] {
			remaining = append(remaining, f.FindingID)
		}
	}
	sort.Strings(remaining)
	if len(remaining) == 0 {
		c.State = domain.ReviewPending
	}
	result := &ResolutionResult{c, out, remaining}
	actor := out[0].ResolvedBy
	event, err := s.newEvent(c, "REVIEW_FINDING_RESOLVED", actor, requestHash(out))
	if err != nil {
		return nil, err
	}
	if err = s.Store.Commit(persistence.Mutation{Campaign: c, FindingResolutions: out, Event: &event, IdemKey: key, IdemHash: hash, Response: result}); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) QualificationCheck(id string, station string, start, end time.Time, devices []string) (*domain.QualificationCheck, error) {
	campaign, artifact, failed, err := s.qualificationContext(id)
	if err != nil {
		return nil, err
	}
	result, err := domain.BuildQualificationCheck(campaign, station, start.UTC(), end.UTC(), devices, failed)
	if err != nil {
		return nil, err
	}
	result.ReviewerID = artifact.ReviewerID
	result.EvidenceDigest = artifact.PayloadDigest
	result.AuditHeadDigest = artifact.AuditHeadDigest
	return &result, nil
}

// qualificationContext validates the immutable archived artifact once and returns
// the campaign snapshot plus devices with unresolved deviations.
func (s *Service) qualificationContext(id string) (domain.Campaign, *domain.Artifact, map[string]bool, error) {
	current, err := s.get(id)
	if err != nil {
		return domain.Campaign{}, nil, nil, err
	}
	if current.State != domain.Archived {
		return domain.Campaign{}, nil, nil, domain.ErrState
	}
	artifact, err := s.Store.GetArtifact(id)
	if err != nil {
		return domain.Campaign{}, nil, nil, err
	}
	verification := audit.VerifyArtifactPayload(artifact.Payload, "")
	if !verification.Valid || verification.PayloadDigest != artifact.PayloadDigest {
		return domain.Campaign{}, nil, nil, domain.ErrIntegrity
	}
	var payload audit.ArtifactPayload
	if json.Unmarshal(artifact.Payload, &payload) != nil {
		return domain.Campaign{}, nil, nil, domain.ErrIntegrity
	}
	expected := []string{"campaign", "references", "rounds", "evaluations", "deviations", "reviews", "audit"}
	if len(payload.Manifest) != len(expected) || len(payload.Sections) != len(expected) {
		return domain.Campaign{}, nil, nil, domain.ErrIntegrity
	}
	seen := map[string]bool{}
	for i, item := range payload.Manifest {
		if item.SectionName != expected[i] || !containsSectionName(expected, item.SectionName) || seen[item.SectionName] {
			return domain.Campaign{}, nil, nil, domain.ErrIntegrity
		}
		seen[item.SectionName] = true
	}
	for _, name := range expected {
		if !seen[name] {
			return domain.Campaign{}, nil, nil, domain.ErrIntegrity
		}
	}
	var campaignSection []domain.Campaign
	if json.Unmarshal(payload.Sections["campaign"], &campaignSection) != nil || len(campaignSection) != 1 || campaignSection[0].CampaignID != id || campaignSection[0].Revision != current.Revision {
		return domain.Campaign{}, nil, nil, domain.ErrIntegrity
	}
	var events []audit.Event
	if json.Unmarshal(payload.Sections["audit"], &events) != nil {
		return domain.Campaign{}, nil, nil, domain.ErrIntegrity
	}
	for _, event := range events {
		if event.CampaignID != "" && event.CampaignID != id {
			return domain.Campaign{}, nil, nil, domain.ErrIntegrity
		}
	}
	report := audit.ValidateDetailed(events, campaignSection[0].Revision)
	if !report.Valid || report.HeadDigest != artifact.AuditHeadDigest {
		return domain.Campaign{}, nil, nil, domain.ErrIntegrity
	}
	var deviationSection struct {
		Deviations []domain.DeviationCase `json:"deviations"`
	}
	if json.Unmarshal(payload.Sections["deviations"], &deviationSection) != nil {
		return domain.Campaign{}, nil, nil, domain.ErrIntegrity
	}
	failed := map[string]bool{}
	for _, item := range deviationSection.Deviations {
		if item.Status != "CLOSED" && item.DeviceID != "" {
			failed[item.DeviceID] = true
		}
	}
	return campaignSection[0], artifact, failed, nil
}

func containsSectionName(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

type QualificationBatchCheck struct {
	QueryID     string    `json:"query_id"`
	StationCode string    `json:"station_code"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	DeviceIDs   []string  `json:"device_ids"`
}

type QualificationBatchInput struct {
	Checks []QualificationBatchCheck `json:"checks"`
}

type QualificationBatchItem struct {
	QueryID          string                             `json:"query_id"`
	WindowConclusion string                             `json:"window_conclusion"`
	OverallQualified bool                               `json:"overall_qualified"`
	Devices          []domain.QualificationDeviceResult `json:"devices"`
}

type QualificationBatchSummary struct {
	TotalQueries        int            `json:"total_queries"`
	QualifiedQueries    int            `json:"qualified_queries"`
	FailedQueries       int            `json:"failed_queries"`
	DeviceFailureCounts map[string]int `json:"device_failure_counts"`
	OverallQualified    bool           `json:"overall_qualified"`
}

type QualificationBatchResult struct {
	AnalyzedRevision int64                     `json:"analyzed_revision"`
	Checks           []QualificationBatchItem  `json:"checks"`
	Summary          QualificationBatchSummary `json:"summary"`
	EvidenceDigest   string                    `json:"evidence_digest"`
	AuditHeadDigest  string                    `json:"audit_head_digest"`
}

func (s *Service) QualificationBatch(id string, in QualificationBatchInput) (*QualificationBatchResult, error) {
	if len(in.Checks) == 0 || len(in.Checks) > 32 {
		return nil, domain.ErrInvalid
	}
	campaign, artifact, failed, err := s.qualificationContext(id)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i := range in.Checks {
		q := &in.Checks[i]
		q.QueryID = strings.TrimSpace(q.QueryID)
		if q.QueryID == "" || seen[q.QueryID] {
			return nil, domain.ErrInvalid
		}
		seen[q.QueryID] = true
		if q.WindowStart.IsZero() || q.WindowEnd.IsZero() || len(q.DeviceIDs) == 0 {
			return nil, domain.ErrInvalid
		}
		devices := make(map[string]bool, len(q.DeviceIDs))
		for j := range q.DeviceIDs {
			q.DeviceIDs[j] = strings.TrimSpace(q.DeviceIDs[j])
			if q.DeviceIDs[j] == "" || devices[q.DeviceIDs[j]] {
				return nil, domain.ErrInvalid
			}
			devices[q.DeviceIDs[j]] = true
		}
	}
	sort.Slice(in.Checks, func(i, j int) bool {
		if !in.Checks[i].WindowStart.Equal(in.Checks[j].WindowStart) {
			return in.Checks[i].WindowStart.Before(in.Checks[j].WindowStart)
		}
		if !in.Checks[i].WindowEnd.Equal(in.Checks[j].WindowEnd) {
			return in.Checks[i].WindowEnd.Before(in.Checks[j].WindowEnd)
		}
		return in.Checks[i].QueryID < in.Checks[j].QueryID
	})
	out := &QualificationBatchResult{AnalyzedRevision: campaign.Revision, Checks: make([]QualificationBatchItem, 0, len(in.Checks)), EvidenceDigest: artifact.PayloadDigest, AuditHeadDigest: artifact.AuditHeadDigest}
	out.Summary.DeviceFailureCounts = map[string]int{}
	out.Summary.TotalQueries = len(in.Checks)
	out.Summary.OverallQualified = true
	for _, q := range in.Checks {
		check, err := domain.BuildQualificationCheck(campaign, strings.TrimSpace(q.StationCode), q.WindowStart.UTC(), q.WindowEnd.UTC(), q.DeviceIDs, failed)
		if err != nil {
			return nil, err
		}
		item := QualificationBatchItem{QueryID: strings.TrimSpace(q.QueryID), WindowConclusion: check.WindowConclusion, OverallQualified: check.OverallQualified, Devices: check.Devices}
		out.Checks = append(out.Checks, item)
		if check.OverallQualified {
			out.Summary.QualifiedQueries++
		} else {
			out.Summary.FailedQueries++
			out.Summary.OverallQualified = false
		}
		for _, d := range check.Devices {
			if d.Conclusion != "QUALIFIED" {
				out.Summary.DeviceFailureCounts[d.DeviceID]++
			}
		}
	}
	return out, nil
}

func BuildReviewFindings(campaignID string, review domain.Review, revision int64, now time.Time) []domain.ReviewFinding {
	out := []domain.ReviewFinding{}
	for _, item := range review.Checklist {
		if item.Result != "FAIL" {
			continue
		}
		severity := item.Severity
		if severity == "" {
			severity = "MAJOR"
		}
		id := fmt.Sprintf("%s-review-%d-%s", campaignID, revision, item.CheckCode)
		if item.DeviceID != "" {
			id += "-" + item.DeviceID
		}
		out = append(out, domain.ReviewFinding{FindingID: id, CampaignID: campaignID, ReviewRevision: revision, CheckCode: item.CheckCode, Description: strings.TrimSpace(item.Note), DeviceID: item.DeviceID, Severity: severity, RequiresRetest: item.RequiresRetest, CreatedAt: now.UTC()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FindingID < out[j].FindingID })
	return out
}
