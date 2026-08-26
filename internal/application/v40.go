package application

import (
	"database/sql"
	"encoding/json"
	"errors"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"regexp"
	"sort"
	"strings"
	"time"
)

type AmendmentInput struct {
	StationCode        string                  `json:"station_code"`
	MissionWindowStart time.Time               `json:"mission_window_start"`
	MissionWindowEnd   time.Time               `json:"mission_window_end"`
	DeviceIDs          []string                `json:"device_ids"`
	ThresholdProfile   domain.ThresholdProfile `json:"threshold_profile"`
	ExpectedRevision   int64                   `json:"expected_revision"`
	MeasurementPlan    *domain.MeasurementPlan `json:"measurement_plan,omitempty"`
}
type AmendmentResult struct {
	Campaign *domain.Campaign         `json:"campaign"`
	Coverage domain.ReferenceCoverage `json:"reference_coverage"`
}

func (s *Service) AmendCampaign(id, requestID string, in AmendmentInput) (*AmendmentResult, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	hash, key := requestHash(in), idemKey("campaign-amendment", id, requestID)
	var old AmendmentResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if err = s.check(c, in.ExpectedRevision); err != nil {
		return nil, err
	}
	if c.State != domain.Draft {
		return nil, domain.ErrState
	}
	candidate := *c
	if strings.TrimSpace(in.StationCode) != "" {
		candidate.StationCode = strings.TrimSpace(in.StationCode)
	}
	if !in.MissionWindowStart.IsZero() {
		candidate.MissionWindowStart = in.MissionWindowStart.UTC()
	}
	if !in.MissionWindowEnd.IsZero() {
		candidate.MissionWindowEnd = in.MissionWindowEnd.UTC()
	}
	if in.DeviceIDs != nil {
		candidate.DeviceIDs = append([]string(nil), in.DeviceIDs...)
	}
	if in.ThresholdProfile != (domain.ThresholdProfile{}) {
		candidate.Threshold = in.ThresholdProfile
	}
	refsForLock, e := s.Store.References(id)
	if e != nil {
		return nil, e
	}
	if in.MeasurementPlan != nil {
		if len(refsForLock) > 0 {
			return nil, domain.ErrState
		}
		plan, planErr := domain.NormalizeMeasurementPlan(*in.MeasurementPlan)
		if planErr != nil {
			return nil, planErr
		}
		candidate.MeasurementPlan, candidate.MeasurementPlanLocked = plan, true
	}
	validated, e := domain.NewCampaign(candidate.CampaignID, candidate.StationCode, candidate.MissionWindowStart, candidate.MissionWindowEnd, candidate.DeviceIDs, candidate.Threshold, candidate.CreatedBy, candidate.CreatedAt)
	if e != nil {
		return nil, e
	}
	validated.State, validated.Revision, validated.CreatedAt = c.State, c.Revision+1, c.CreatedAt
	validated.MeasurementPlan, validated.MeasurementPlanLocked = candidate.MeasurementPlan, candidate.MeasurementPlanLocked
	refs, e := s.Store.References(id)
	if e != nil {
		return nil, e
	}
	withdrawals, e := s.Store.ReferenceWithdrawals(id)
	if e != nil {
		return nil, e
	}
	active := domain.EffectiveReferences(refs, withdrawals)
	coverage := validated.ReferenceCoverage(active)
	result := &AmendmentResult{validated, coverage}
	summaryBytes, _ := json.Marshal(struct {
		Before any `json:"before"`
		After  any `json:"after"`
	}{Before: struct {
		Station   string                  `json:"station_code"`
		Start     time.Time               `json:"mission_window_start"`
		End       time.Time               `json:"mission_window_end"`
		Devices   []string                `json:"device_ids"`
		Threshold domain.ThresholdProfile `json:"threshold_profile"`
	}{c.StationCode, c.MissionWindowStart, c.MissionWindowEnd, c.DeviceIDs, c.Threshold}, After: struct {
		Station   string                  `json:"station_code"`
		Start     time.Time               `json:"mission_window_start"`
		End       time.Time               `json:"mission_window_end"`
		Devices   []string                `json:"device_ids"`
		Threshold domain.ThresholdProfile `json:"threshold_profile"`
	}{validated.StationCode, validated.MissionWindowStart, validated.MissionWindowEnd, validated.DeviceIDs, validated.Threshold}})
	summary := string(summaryBytes)
	event, e := s.newEvent(validated, "CAMPAIGN_AMENDED", c.CreatedBy, summary)
	if e != nil {
		return nil, e
	}
	conflicts, e := s.Store.CommitAmendment(validated, event, key, hash, result)
	if errors.Is(e, persistence.ErrResourceConflict) {
		return nil, &domain.ConflictError{Conflicts: conflicts}
	}
	if e != nil {
		return nil, e
	}
	return result, nil
}

func (s *Service) ReferencePreflight(id string, candidates []domain.ReferenceEvidence) (domain.ReferencePreflightResult, error) {
	c, e := s.get(id)
	if e != nil {
		return domain.ReferencePreflightResult{}, e
	}
	refs, e := s.Store.References(id)
	if e != nil {
		return domain.ReferencePreflightResult{}, e
	}
	withdrawals, e := s.Store.ReferenceWithdrawals(id)
	if e != nil {
		return domain.ReferencePreflightResult{}, e
	}
	refs = domain.EffectiveReferences(refs, withdrawals)
	for i := range candidates {
		candidates[i].CampaignID = id
	}
	return domain.ReferencePreflight(c, refs, candidates), nil
}

type RoundVoidInput struct {
	RoundID          string `json:"round_id"`
	ReasonCode       string `json:"reason_code"`
	Reason           string `json:"reason"`
	VoidedBy         string `json:"voided_by"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type RoundVoidResult struct {
	Campaign *domain.Campaign           `json:"campaign"`
	Void     domain.RoundVoid           `json:"void"`
	Coverage domain.MeasurementCoverage `json:"coverage"`
}

var reasonCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

func (s *Service) VoidRound(id, requestID string, in RoundVoidInput) (*RoundVoidResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	hash, key := requestHash(in), idemKey("round-void", id, requestID)
	var old RoundVoidResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if e = s.check(c, in.ExpectedRevision); e != nil {
		return nil, e
	}
	if c.State != domain.ReferenceVerified && c.State != domain.Measured {
		return nil, domain.ErrState
	}
	if strings.TrimSpace(in.RoundID) == "" || !reasonCodePattern.MatchString(in.ReasonCode) || strings.TrimSpace(in.Reason) == "" || strings.TrimSpace(in.VoidedBy) == "" {
		return nil, domain.ErrInvalid
	}
	if evaluations, e := s.Store.Evaluations(id); e != nil {
		return nil, e
	} else if len(evaluations) > 0 {
		return nil, domain.ErrState
	}
	rounds, e := s.Store.Rounds(id)
	if e != nil {
		return nil, e
	}
	var target *domain.MeasurementRound
	for i := range rounds {
		if rounds[i].RoundID == in.RoundID {
			target = &rounds[i]
			break
		}
	}
	if target == nil {
		return nil, sql.ErrNoRows
	}
	if target.Purpose == "retest" || target.Purpose == "remediation" {
		return nil, domain.ErrState
	}
	voids, e := s.Store.RoundVoids(id)
	if e != nil {
		return nil, e
	}
	for _, v := range voids {
		if v.RoundID == in.RoundID {
			return nil, domain.ErrDuplicate
		}
	}
	c.Revision++
	v := domain.RoundVoid{CampaignID: id, RoundID: in.RoundID, ReasonCode: in.ReasonCode, Reason: strings.TrimSpace(in.Reason), VoidedBy: strings.TrimSpace(in.VoidedBy), VoidedAt: time.Now().UTC(), Revision: c.Revision}
	effective := domain.EffectiveRounds(rounds, append(voids, v))
	completion, _ := domain.MeasurementPlanCompliance(c, effective)
	coverage := domain.MeasurementReadinessFor(c, effective)
	coverage.Complete = completion.Complete
	if coverage.Complete {
		c.State = domain.Measured
	} else {
		c.State = domain.ReferenceVerified
	}
	result := &RoundVoidResult{c, v, coverage}
	event, e := s.newEvent(c, "MEASUREMENT_ROUND_VOIDED", v.VoidedBy, requestHash(v))
	if e != nil {
		return nil, e
	}
	if e = s.Store.Commit(persistence.Mutation{Campaign: c, RoundVoids: []domain.RoundVoid{v}, Event: &event, IdemKey: key, IdemHash: hash, Response: result}); e != nil {
		return nil, e
	}
	return result, nil
}

func (s *Service) MeasurementSummary(id, deviceID, purpose string) (domain.MeasurementSummary, error) {
	c, e := s.get(id)
	if e != nil {
		return domain.MeasurementSummary{}, e
	}
	if deviceID != "" && !containsString(c.DeviceIDs, deviceID) {
		return domain.MeasurementSummary{}, domain.ErrInvalid
	}
	if purpose != "" && purpose != "original" && purpose != "retest" && purpose != "remediation" {
		return domain.MeasurementSummary{}, domain.ErrInvalid
	}
	rounds, voids, exclusions := s.measurementSummaryMaterials(id)
	return domain.BuildMeasurementSummary(c, effectiveWithoutExclusions(rounds, voids, exclusions), deviceID, purpose), nil
}

func (s *Service) measurementSummaryMaterials(id string) ([]domain.MeasurementRound, []domain.RoundVoid, []domain.SampleExclusion) {
	rounds, roundsErr := s.Store.Rounds(id)
	voids, voidsErr := s.Store.RoundVoids(id)
	exclusions, exclusionsErr := s.Store.SampleExclusions(id)
	if roundsErr != nil || voidsErr != nil || exclusionsErr != nil {
		return rounds, voids, exclusions
	}
	return rounds, voids, exclusions
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

type RemediationCheck struct {
	DeviationID string `json:"deviation_id"`
	Code        string `json:"code"`
	Status      string `json:"status"`
	Message     string `json:"message,omitempty"`
}
type RemediationPrediction struct {
	DeviationID   string  `json:"deviation_id"`
	Result        string  `json:"result"`
	ObservedValue float64 `json:"observed_value,omitempty"`
	LimitValue    float64 `json:"limit_value,omitempty"`
}
type RemediationPreflightResult struct {
	Checks                  []RemediationCheck      `json:"checks"`
	Predictions             []RemediationPrediction `json:"predictions"`
	ClosedEvidence          []domain.DeviationCase  `json:"closed_evidence"`
	Blocking                []RemediationCheck      `json:"blocking"`
	ReadyToSubmit           bool                    `json:"ready_to_submit"`
	WouldEnterReviewPending bool                    `json:"would_enter_review_pending"`
}

func (s *Service) RemediationPreflight(id string, deviationIDs []string, candidate *domain.MeasurementRound) (*RemediationPreflightResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	ds, e := s.Store.Deviations(id)
	if e != nil {
		return nil, e
	}
	plans, e := s.Store.Plans(id)
	if e != nil {
		return nil, e
	}
	ledger, e := s.Store.RemediationEvidence(id)
	if e != nil {
		return nil, e
	}
	rounds, e := s.Store.Rounds(id)
	if e != nil {
		return nil, e
	}
	voids, e := s.Store.RoundVoids(id)
	if e != nil {
		return nil, e
	}
	historicalRounds := rounds
	rounds = domain.EffectiveRounds(rounds, voids)
	out := &RemediationPreflightResult{Checks: []RemediationCheck{}, Predictions: []RemediationPrediction{}, ClosedEvidence: []domain.DeviationCase{}, Blocking: []RemediationCheck{}}
	by := map[string]domain.DeviationCase{}
	for _, d := range ds {
		by[d.DeviationID] = d
		if d.Status == "CLOSED" {
			out.ClosedEvidence = append(out.ClosedEvidence, d)
		}
	}
	latest := map[string]domain.RemediationPlan{}
	for _, p := range plans {
		latest[p.DeviationID] = p
	}
	ledgerTypes := map[string]map[string]bool{}
	for _, item := range ledger {
		p, ok := latest[item.DeviationID]
		if ok && item.PlanVersion == p.Version {
			if ledgerTypes[item.DeviationID] == nil {
				ledgerTypes[item.DeviationID] = map[string]bool{}
			}
			ledgerTypes[item.DeviationID][item.EvidenceType] = true
		}
	}
	selected := map[string]bool{}
	dependencyProjection, dependencyErr := s.RemediationDependencies(id)
	if dependencyErr != nil {
		return nil, dependencyErr
	}
	dependencyNodes := map[string]domain.DependencyNode{}
	for _, node := range dependencyProjection.Nodes {
		dependencyNodes[node.DeviationID] = node
	}
	if len(deviationIDs) == 0 {
		for _, d := range ds {
			if d.Status == "OPEN" {
				deviationIDs = append(deviationIDs, d.DeviationID)
			}
		}
		sort.Strings(deviationIDs)
	}
	for _, id := range deviationIDs {
		if selected[id] {
			out.Blocking = append(out.Blocking, RemediationCheck{id, "DUPLICATE_DEVIATION_ID", "INVALID", "偏差编号重复"})
			continue
		}
		selected[id] = true
		d, ok := by[id]
		if !ok {
			out.Blocking = append(out.Blocking, RemediationCheck{id, "UNKNOWN_DEVIATION_ID", "INVALID", "偏差不存在"})
			continue
		}
		if d.Status != "OPEN" {
			continue
		}
		if node, ok := dependencyNodes[id]; ok && node.Status == "BLOCKED" {
			x := RemediationCheck{id, "DEPENDENCY_BLOCKED", "BLOCKED", "前置偏差尚未闭合：" + strings.Join(node.BlockingDeviationIDs, ",")}
			out.Checks = append(out.Checks, x)
			out.Blocking = append(out.Blocking, x)
		}
		p, ok := latest[id]
		fields := []struct{ code, value string }{{"ROOT_CAUSE", d.RootCause}, {"CONTAINMENT", d.Containment}, {"CORRECTIVE_ACTION", d.CorrectiveAction}}
		if ok {
			out.Checks = append(out.Checks, RemediationCheck{id, "RESPONSIBILITY_PLAN", "READY", ""})
			fields = []struct{ code, value string }{{"ROOT_CAUSE", p.RootCause}, {"CONTAINMENT", p.Containment}, {"CORRECTIVE_ACTION", p.CorrectiveAction}, {"OWNER", p.Owner}}
			if p.TargetAt.IsZero() || !p.TargetAt.After(p.PlannedAt) {
				x := RemediationCheck{id, "TARGET_AT", "INVALID", "目标时间无效"}
				out.Checks = append(out.Checks, x)
				out.Blocking = append(out.Blocking, x)
			} else {
				out.Checks = append(out.Checks, RemediationCheck{id, "TARGET_AT", "READY", ""})
			}
		} else {
			x := RemediationCheck{id, "RESPONSIBILITY_PLAN", "MISSING", "缺少当前责任计划"}
			out.Checks = append(out.Checks, x)
			out.Blocking = append(out.Blocking, x)
		}
		for _, typ := range []string{"ROOT_CAUSE", "CONTAINMENT", "CORRECTIVE_ACTION"} {
			if !ledgerTypes[id][typ] {
				x := RemediationCheck{id, "EVIDENCE_" + typ, "MISSING", "缺少整改执行证据"}
				out.Checks = append(out.Checks, x)
				out.Blocking = append(out.Blocking, x)
			}
		}
		for _, f := range fields {
			st := "READY"
			if strings.TrimSpace(f.value) == "" {
				st = "MISSING"
			}
			x := RemediationCheck{id, f.code, st, ""}
			out.Checks = append(out.Checks, x)
			if st != "READY" {
				out.Blocking = append(out.Blocking, x)
			}
		}
	}
	if candidate != nil {
		candidate.CampaignID = id
		candidate.Purpose = "retest"
		for _, historical := range historicalRounds {
			if historical.RoundID == candidate.RoundID || historical.Sequence == candidate.Sequence {
				out.Blocking = append(out.Blocking, RemediationCheck{"", "CANDIDATE_ROUND_DUPLICATE", "INVALID", "候选轮次编号或序号与历史重复"})
			}
		}
		probe := *c
		if e = probe.AddMeasurementBatch([]domain.MeasurementRound{*candidate}, rounds); e != nil {
			out.Blocking = append(out.Blocking, RemediationCheck{"", "CANDIDATE_RETEST_INVALID", "INVALID", e.Error()})
		} else {
			for did := range selected {
				d, ok := by[did]
				if !ok || d.Status != "OPEN" {
					continue
				}
				sample, ok := sampleFor(*candidate, d.DeviceID)
				if !ok {
					out.Predictions = append(out.Predictions, RemediationPrediction{DeviationID: did, Result: "MISSING_SAMPLE", LimitValue: d.LimitValue})
					out.Blocking = append(out.Blocking, RemediationCheck{did, "RETEST_SAMPLE_MISSING", "MISSING", "候选复测缺少关联设备样本"})
					continue
				}
				observed := mathObserved(d, sample, candidate.Sequence, rounds)
				result := "WOULD_REMAIN_OPEN"
				if passesRetest(d, sample, candidate.Sequence, rounds) {
					result = "WOULD_CLOSE"
				}
				out.Predictions = append(out.Predictions, RemediationPrediction{did, result, observed, d.LimitValue})
				if result != "WOULD_CLOSE" {
					out.Blocking = append(out.Blocking, RemediationCheck{did, "RETEST_THRESHOLD_EXCEEDED", "INVALID", "候选复测仍超限"})
				}
			}
		}
	}
	sort.Slice(out.Checks, func(i, j int) bool {
		if out.Checks[i].DeviationID != out.Checks[j].DeviationID {
			return out.Checks[i].DeviationID < out.Checks[j].DeviationID
		}
		return out.Checks[i].Code < out.Checks[j].Code
	})
	sort.Slice(out.Blocking, func(i, j int) bool {
		if out.Blocking[i].DeviationID != out.Blocking[j].DeviationID {
			return out.Blocking[i].DeviationID < out.Blocking[j].DeviationID
		}
		return out.Blocking[i].Code < out.Blocking[j].Code
	})
	sort.Slice(out.Predictions, func(i, j int) bool { return out.Predictions[i].DeviationID < out.Predictions[j].DeviationID })
	sort.Slice(out.ClosedEvidence, func(i, j int) bool { return out.ClosedEvidence[i].DeviationID < out.ClosedEvidence[j].DeviationID })
	out.ReadyToSubmit = len(out.Blocking) == 0 && candidate != nil && len(selected) > 0
	allClosed := true
	for _, d := range ds {
		if d.Status == "OPEN" && !selected[d.DeviationID] {
			allClosed = false
		}
	}
	out.WouldEnterReviewPending = out.ReadyToSubmit && allClosed
	return out, nil
}

type ReviewClaimInput struct {
	ReviewerID       string    `json:"reviewer_id"`
	Note             string    `json:"note"`
	ExpiresAt        time.Time `json:"expires_at"`
	DurationMinutes  int       `json:"duration_minutes"`
	ExpectedRevision int64     `json:"expected_revision"`
}
type ReviewClaimResult struct {
	Campaign    *domain.Campaign   `json:"campaign"`
	Claim       domain.ReviewClaim `json:"claim"`
	ClaimStatus domain.ClaimStatus `json:"claim_status"`
}

func (s *Service) ClaimReview(id, requestID string, in ReviewClaimInput) (*ReviewClaimResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	hash, key := requestHash(in), idemKey("review-claim", id, requestID)
	var old ReviewClaimResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if e = s.check(c, in.ExpectedRevision); e != nil {
		return nil, e
	}
	if c.State != domain.ReviewPending {
		return nil, domain.ErrState
	}
	if strings.TrimSpace(in.ReviewerID) == "" {
		return nil, domain.ErrInvalid
	}
	rounds, e := s.Store.Rounds(id)
	if e != nil {
		return nil, e
	}
	conflicts := []string{}
	for _, r := range rounds {
		if r.OperatorID == in.ReviewerID {
			conflicts = append(conflicts, r.RoundID)
		}
	}
	sort.Strings(conflicts)
	if len(conflicts) > 0 {
		return nil, &ReviewerIndependenceError{RoundIDs: conflicts}
	}
	now := time.Now().UTC()
	claims, e := s.Store.ReviewClaims(id)
	if e != nil {
		return nil, e
	}
	var current *domain.ReviewClaim
	if len(claims) > 0 {
		current = &claims[len(claims)-1]
	}
	if current != nil && current.Status == "ACTIVE" && now.Before(current.ExpiresAt) && current.ReviewerID != in.ReviewerID {
		return nil, ErrReviewClaimConflict
	}
	duration := time.Duration(in.DurationMinutes) * time.Minute
	if duration == 0 {
		duration = 30 * time.Minute
	}
	if duration < time.Minute || duration > 2*time.Hour {
		return nil, domain.ErrInvalid
	}
	expires := in.ExpiresAt.UTC()
	if expires.IsZero() {
		expires = now.Add(duration)
	}
	if !expires.After(now) || expires.After(now.Add(2*time.Hour)) {
		return nil, domain.ErrInvalid
	}
	version := 1
	if current != nil {
		version = current.Version + 1
	}
	claim := domain.ReviewClaim{CampaignID: id, ReviewerID: in.ReviewerID, ClaimedAt: now, ExpiresAt: expires, Note: strings.TrimSpace(in.Note), Status: "ACTIVE", Version: version, Revision: c.Revision + 1}
	c.Revision++
	action := "REVIEW_CLAIMED"
	if current != nil && current.ReviewerID == in.ReviewerID && current.Status == "ACTIVE" && now.Before(current.ExpiresAt) {
		action = "REVIEW_CLAIM_RENEWED"
		claim.ClaimedAt = current.ClaimedAt
	}
	result := &ReviewClaimResult{c, claim, domain.DerivedClaimStatus(&claim, now)}
	event, e := s.newEvent(c, action, in.ReviewerID, requestHash(claim))
	if e != nil {
		return nil, e
	}
	if e = s.Store.Commit(persistence.Mutation{Campaign: c, ReviewClaims: []domain.ReviewClaim{claim}, Event: &event, IdemKey: key, IdemHash: hash, Response: result}); e != nil {
		return nil, e
	}
	return result, nil
}

func (s *Service) ReleaseReviewClaim(id, requestID string, in ReviewClaimInput) (*ReviewClaimResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	hash, key := requestHash(in), idemKey("review-claim-release", id, requestID)
	var old ReviewClaimResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if e = s.check(c, in.ExpectedRevision); e != nil {
		return nil, e
	}
	current, e := s.Store.CurrentReviewClaim(id)
	if e != nil {
		return nil, e
	}
	now := time.Now().UTC()
	if current.Status != "ACTIVE" || !now.Before(current.ExpiresAt) || current.ReviewerID != in.ReviewerID {
		return nil, ErrReviewClaimConflict
	}
	claim := *current
	claim.Version++
	claim.Revision = c.Revision + 1
	claim.Status = "RELEASED"
	c.Revision++
	result := &ReviewClaimResult{c, claim, domain.DerivedClaimStatus(&claim, now)}
	event, e := s.newEvent(c, "REVIEW_CLAIM_RELEASED", in.ReviewerID, requestHash(claim))
	if e != nil {
		return nil, e
	}
	if e = s.Store.Commit(persistence.Mutation{Campaign: c, ReviewClaims: []domain.ReviewClaim{claim}, Event: &event, IdemKey: key, IdemHash: hash, Response: result}); e != nil {
		return nil, e
	}
	return result, nil
}

var ErrReviewClaimConflict = errors.New("review claim conflict")

type ReviewerIndependenceError struct {
	RoundIDs []string `json:"round_ids"`
}

func (e *ReviewerIndependenceError) Error() string { return "reviewer independence violation" }
