package application

import (
	"database/sql"
	"errors"
	"fmt"
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CorrectionInput struct {
	EvidenceID          string                   `json:"evidence_id"`
	Reason              string                   `json:"reason"`
	Replacement         domain.ReferenceEvidence `json:"replacement"`
	NewEvidence         domain.ReferenceEvidence `json:"new_evidence"`
	ReplacementEvidence domain.ReferenceEvidence `json:"replacement_evidence"`
	ExpectedRevision    int64                    `json:"expected_revision"`
}

func (s *Service) CorrectReference(id, requestID string, in CorrectionInput) (*ReferenceResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	hash, key := requestHash(in), idemKey("reference-correction", id, requestID)
	var old ReferenceResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if e = s.check(c, in.ExpectedRevision); e != nil {
		return nil, e
	}
	if c.State != domain.Draft {
		return nil, domain.ErrState
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, domain.ErrInvalid
	}
	refs, e := s.Store.References(id)
	if e != nil {
		return nil, e
	}
	withdrawals, e := s.Store.ReferenceWithdrawals(id)
	if e != nil {
		return nil, e
	}
	for _, item := range withdrawals {
		if item.EvidenceID == in.EvidenceID {
			return nil, domain.ErrState
		}
	}
	idx := -1
	for i := range refs {
		if refs[i].EvidenceID == in.EvidenceID {
			idx = i
		}
	}
	if idx < 0 || refs[idx].Replaced {
		return nil, sql.ErrNoRows
	}
	n := in.Replacement
	if n.EvidenceID == "" {
		n = in.NewEvidence
	}
	if n.EvidenceID == "" {
		n = in.ReplacementEvidence
	}
	n.CampaignID = id
	if n.SubmittedAt.IsZero() {
		n.SubmittedAt = time.Now().UTC()
	}
	if e = c.AddReference(n, time.Now()); e != nil {
		return nil, e
	}
	sources, e := s.certificateSources(n)
	if e != nil {
		return nil, e
	}
	if n.ReferenceKind != refs[idx].ReferenceKind || strings.EqualFold(n.CertificateDigest, refs[idx].CertificateDigest) {
		return nil, domain.ErrInvalid
	}
	for _, r := range refs {
		if r.EvidenceID == n.EvidenceID || strings.EqualFold(r.CertificateDigest, n.CertificateDigest) {
			return nil, domain.ErrDuplicate
		}
	}
	refs[idx].Replaced = true
	refs[idx].CorrectionReason = in.Reason
	refs[idx].ReplacementEvidenceID = n.EvidenceID
	active := []domain.ReferenceEvidence{n}
	for i, r := range refs {
		withdrawn := false
		for _, item := range withdrawals {
			if item.EvidenceID == r.EvidenceID {
				withdrawn = true
				break
			}
		}
		if i != idx && !r.Replaced && !withdrawn {
			active = append(active, r)
		}
	}
	coverage := c.ReferenceCoverage(active)
	c.Revision++
	if coverage.Complete {
		c.State = domain.ReferenceVerified
	}
	result := &ReferenceResult{Campaign: c, Coverage: coverage, Fingerprint: domain.CertificateFingerprint(n), SourceCampaignIDs: sources}
	ev, e := s.newEvent(c, "CORRECT_REFERENCE", n.SubmittedBy, requestHash(map[string]any{"input": in, "certificate_fingerprint": domain.CertificateFingerprint(n), "source_campaign_count": len(sources)}))
	if e != nil {
		return nil, e
	}
	if e = s.Store.SaveReferenceReplacement(refs[idx], n); e != nil {
		return nil, e
	}
	e = s.Store.Commit(persistence.Mutation{
		Campaign: c,
		Event:    &ev,
		IdemKey:  key,
		IdemHash: hash,
		Response: result,
	})
	return result, e
}

func (s *Service) MeasurePreflight(id string, batch []domain.MeasurementRound) (domain.PreflightResult, error) {
	c, e := s.get(id)
	if e != nil {
		return domain.PreflightResult{}, e
	}
	rounds, e := s.Store.Rounds(id)
	if e != nil {
		return domain.PreflightResult{}, e
	}
	for i := range batch {
		batch[i].CampaignID = id
	}
	voids, e := s.Store.RoundVoids(id)
	if e != nil {
		return domain.PreflightResult{}, e
	}
	exclusions, _ := s.Store.SampleExclusions(id)
	effective := effectiveWithoutExclusions(rounds, voids, exclusions)
	result := domain.MeasurementPreflight(c, effective, batch)
	completion, planIssues := domain.MeasurementPlanCompliance(c, append(effective, batch...))
	result.Issues = append(result.Issues, planIssues...)
	if c.MeasurementPlanLocked && len(planIssues) > 0 {
		result.Submittable = false
	}
	if completion.Complete {
		result.EffectiveSpanSeconds = 0
		for _, span := range completion.DeviceSpanSeconds {
			if result.EffectiveSpanSeconds == 0 || span < result.EffectiveSpanSeconds {
				result.EffectiveSpanSeconds = span
			}
		}
	}
	return result, nil
}

type EvaluationHistory struct {
	Items      []domain.Evaluation           `json:"items"`
	Total      int                           `json:"total"`
	Offset     int                           `json:"offset"`
	Limit      int                           `json:"limit"`
	Comparison []domain.DeviceEvaluationDiff `json:"comparison,omitempty"`
}

func (s *Service) EvaluationHistory(id string, offset, limit int, from, to int64) (*EvaluationHistory, error) {
	if offset < 0 || limit < 0 || limit > 100 || from < 0 || to < 0 || (from > 0 && (to == 0 || from >= to)) {
		return nil, domain.ErrInvalid
	}
	if limit == 0 {
		limit = 50
	}
	if _, e := s.get(id); e != nil {
		return nil, e
	}
	all, e := s.Store.Evaluations(id)
	if e != nil {
		return nil, e
	}
	out := &EvaluationHistory{Total: len(all), Offset: offset, Limit: limit, Items: []domain.Evaluation{}}
	if offset < len(all) {
		end := offset + limit
		if end > len(all) {
			end = len(all)
		}
		out.Items = all[offset:end]
	}
	if from > 0 {
		var a, b *domain.Evaluation
		for i := range all {
			if all[i].Revision == from {
				a = &all[i]
			}
			if all[i].Revision == to {
				b = &all[i]
			}
		}
		if a == nil || b == nil {
			return nil, sql.ErrNoRows
		}
		out.Comparison = domain.EvaluationDiff(*a, *b)
	}
	return out, nil
}

type PlanQuery struct {
	Owner, Risk string
	Now         time.Time
}

func risk(p domain.RemediationPlan, now time.Time) string {
	if now.After(p.TargetAt) {
		return "OVERDUE"
	}
	if p.TargetAt.Sub(now) <= 24*time.Hour {
		return "DUE_SOON"
	}
	return "IN_PROGRESS"
}
func (s *Service) AddPlans(id, requestID string, revision int64, plans []domain.RemediationPlan) ([]domain.RemediationPlan, *domain.Campaign, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, nil, e
	}
	hash, key := requestHash(plans), idemKey("remediation-plans", id, requestID)
	var old struct {
		Plans    []domain.RemediationPlan `json:"plans"`
		Campaign *domain.Campaign         `json:"campaign"`
	}
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return old.Plans, old.Campaign, e
	}
	if e = s.check(c, revision); e != nil {
		return nil, nil, e
	}
	if c.State != domain.RemediationRequired || len(plans) == 0 {
		return nil, nil, domain.ErrState
	}
	ds, e := s.Store.Deviations(id)
	if e != nil {
		return nil, nil, e
	}
	open := map[string]bool{}
	for _, d := range ds {
		if d.Status == "OPEN" {
			open[d.DeviationID] = true
		}
	}
	existing, _ := s.Store.Plans(id)
	versions := map[string]int{}
	for _, p := range existing {
		if p.Version > versions[p.DeviationID] {
			versions[p.DeviationID] = p.Version
		}
	}
	seen := map[string]bool{}
	now := time.Now().UTC()
	for i := range plans {
		p := &plans[i]
		if !open[p.DeviationID] || seen[p.DeviationID] || strings.TrimSpace(p.Owner) == "" || strings.TrimSpace(p.RootCause) == "" || strings.TrimSpace(p.Containment) == "" || strings.TrimSpace(p.CorrectiveAction) == "" || !p.TargetAt.After(now) || p.TargetAt.After(c.MissionWindowEnd) {
			return nil, nil, domain.ErrInvalid
		}
		seen[p.DeviationID] = true
		p.PlannedAt = now
		p.Version = versions[p.DeviationID] + 1
		p.RiskStatus = risk(*p, now)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].DeviationID < plans[j].DeviationID })
	c.Revision++
	ev, e := s.newEvent(c, "PLAN_REMEDIATION", plans[0].Owner, requestHash(plans))
	if e != nil {
		return nil, nil, e
	}
	result := struct {
		Plans    []domain.RemediationPlan `json:"plans"`
		Campaign *domain.Campaign         `json:"campaign"`
	}{plans, c}
	e = s.Store.Commit(persistence.Mutation{Campaign: c, Plans: plans, Event: &ev, IdemKey: key, IdemHash: hash, Response: result})
	return plans, c, e
}
func (s *Service) Plans(id string, q PlanQuery) ([]domain.RemediationPlan, error) {
	if _, e := s.get(id); e != nil {
		return nil, e
	}
	all, e := s.Store.Plans(id)
	if e != nil {
		return nil, e
	}
	latest := map[string]domain.RemediationPlan{}
	now := q.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, p := range all {
		latest[p.DeviationID] = p
	}
	out := []domain.RemediationPlan{}
	for _, p := range latest {
		p.RiskStatus = risk(p, now)
		if (q.Owner == "" || p.Owner == q.Owner) && (q.Risk == "" || p.RiskStatus == q.Risk) {
			out = append(out, p)
		}
	}
	deviations, _ := s.Store.Deviations(id)
	if q.Owner == "" && (q.Risk == "" || q.Risk == "UNPLANNED") {
		for _, d := range deviations {
			if d.Status == "OPEN" {
				if _, ok := latest[d.DeviationID]; !ok {
					out = append(out, domain.RemediationPlan{DeviationID: d.DeviationID, RiskStatus: "UNPLANNED"})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviationID < out[j].DeviationID })
	return out, nil
}

type RetestResult struct {
	Campaign  *domain.Campaign       `json:"campaign"`
	Attempts  []domain.RetestAttempt `json:"attempts"`
	Remaining []domain.DeviationCase `json:"remaining"`
}

func (s *Service) RetestAttempt(id, requestID string, revision int64, deviationIDs []string, round domain.MeasurementRound) (*RetestResult, error) {
	c, e := s.get(id)
	if e != nil {
		return nil, e
	}
	hash, key := requestHash(struct {
		DeviationIDs []string                `json:"deviation_ids"`
		Round        domain.MeasurementRound `json:"round"`
	}{deviationIDs, round}), idemKey("retest-attempt", id, requestID)
	var old RetestResult
	if ok, e := s.replay(key, hash, &old); ok || e != nil {
		return &old, e
	}
	if e = s.check(c, revision); e != nil {
		return nil, e
	}
	if c.State != domain.RemediationRequired {
		return nil, domain.ErrState
	}
	round.CampaignID = id
	round.Purpose = "retest"
	if round.CapturedAt.IsZero() {
		round.CapturedAt = time.Now().UTC()
	}
	rounds, e := s.Store.Rounds(id)
	if e != nil {
		return nil, e
	}
	if len(deviationIDs) == 0 {
		findings, findErr := s.Store.ReviewFindings(id)
		if findErr != nil {
			return nil, findErr
		}
		resolutions, resolutionErr := s.Store.FindingResolutions(id)
		if resolutionErr != nil {
			return nil, resolutionErr
		}
		resolved := map[string]bool{}
		for _, item := range resolutions {
			resolved[item.FindingID] = true
		}
		requiredDevices := map[string]bool{}
		needsRetest := false
		for _, finding := range findings {
			if finding.RequiresRetest && !resolved[finding.FindingID] {
				needsRetest = true
				if finding.DeviceID != "" {
					requiredDevices[finding.DeviceID] = true
				}
			}
		}
		if !needsRetest {
			return nil, domain.ErrState
		}
		probe := *c
		if e = probe.AddMeasurementBatch([]domain.MeasurementRound{round}, rounds); e != nil {
			return nil, e
		}
		seen := map[string]bool{}
		for _, sample := range round.Samples {
			if math.Abs(sample.TimeOffset) > c.Threshold.MaxAbsDeviation || math.Abs(sample.FrequencyOffset) > c.Threshold.MaxFrequencyDeviation {
				return nil, errors.New("retest threshold not satisfied")
			}
			seen[sample.DeviceID] = true
		}
		for deviceID := range requiredDevices {
			if !seen[deviceID] {
				return nil, domain.ErrInvalid
			}
		}
		c.Revision++
		c.State = domain.RemediationRequired
		ev, eventErr := s.newEvent(c, "REVIEW_FINDING_RETEST_CAPTURED", round.OperatorID, requestHash(round))
		if eventErr != nil {
			return nil, eventErr
		}
		result := &RetestResult{Campaign: c, Attempts: []domain.RetestAttempt{}, Remaining: []domain.DeviationCase{}}
		if e = s.Store.Commit(persistence.Mutation{Campaign: c, Rounds: []domain.MeasurementRound{round}, Event: &ev, IdemKey: key, IdemHash: hash, Response: result}); e != nil {
			return nil, e
		}
		return result, nil
	}
	probe := *c
	if e = probe.AddMeasurementBatch([]domain.MeasurementRound{round}, rounds); e != nil {
		return nil, e
	}
	ds, e := s.Store.Deviations(id)
	if e != nil {
		return nil, e
	}
	if blocking, dependencyErr := s.blockingDeviations(id, deviationIDs); dependencyErr != nil {
		return nil, dependencyErr
	} else if len(blocking) > 0 {
		return nil, &RemediationBlockedError{BlockingDeviationIDs: blocking}
	}
	by := map[string]domain.DeviationCase{}
	for _, d := range ds {
		by[d.DeviationID] = d
	}
	plans, e := s.Store.Plans(id)
	if e != nil {
		return nil, e
	}
	latest := map[string]int{}
	for _, p := range plans {
		if p.Version > latest[p.DeviationID] {
			latest[p.DeviationID] = p.Version
		}
	}
	ledger, e := s.Store.RemediationEvidence(id)
	if e != nil {
		return nil, e
	}
	complete := map[string]map[string]bool{}
	for _, item := range ledger {
		if item.PlanVersion == latest[item.DeviationID] {
			if complete[item.DeviationID] == nil {
				complete[item.DeviationID] = map[string]bool{}
			}
			complete[item.DeviationID][item.EvidenceType] = true
		}
	}
	attempts := []domain.RetestAttempt{}
	updates := []domain.DeviationCase{}
	seen := map[string]bool{}
	for _, did := range deviationIDs {
		d, ok := by[did]
		if !ok || d.Status != "OPEN" || seen[did] {
			return nil, domain.ErrInvalid
		}
		seen[did] = true
		if !complete[did]["ROOT_CAUSE"] || !complete[did]["CONTAINMENT"] || !complete[did]["CORRECTIVE_ACTION"] {
			return nil, domain.ErrCoverage
		}
		samp, ok := sampleFor(round, d.DeviceID)
		if !ok {
			return nil, domain.ErrInvalid
		}
		pass := passesRetest(d, samp, round.Sequence, rounds)
		obs := mathObserved(d, samp, round.Sequence, rounds)
		res := "FAIL"
		reason := "仍超过锁定门限"
		if pass {
			res = "PASS"
			reason = "已满足锁定门限"
			d.Status = "CLOSED"
			d.RetestRoundID = round.RoundID
		}
		a := domain.RetestAttempt{DeviationID: did, RoundID: round.RoundID, Metric: d.Metric, ObservedValue: obs, LimitValue: d.LimitValue, Result: res, Reason: reason, AttemptedAt: time.Now().UTC()}
		d.Attempts = append(d.Attempts, a)
		attempts = append(attempts, a)
		by[did] = d
		updates = append(updates, d)
	}
	remaining := []domain.DeviationCase{}
	for _, d := range by {
		if d.Status == "OPEN" {
			remaining = append(remaining, d)
		}
	}
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].DeviationID < remaining[j].DeviationID })
	c.Revision++
	if len(remaining) == 0 {
		c.State = domain.ReviewPending
	}
	action := "RETEST_PASSED"
	for _, a := range attempts {
		if a.Result == "FAIL" {
			action = "RETEST_FAILED"
		}
	}
	ev, e := s.newEvent(c, action, round.OperatorID, requestHash(attempts))
	if e != nil {
		return nil, e
	}
	result := &RetestResult{c, attempts, remaining}
	e = s.Store.Commit(persistence.Mutation{Campaign: c, Rounds: []domain.MeasurementRound{round}, Deviations: updates, Event: &ev, IdemKey: key, IdemHash: hash, Response: result})
	return result, e
}
func mathObserved(d domain.DeviationCase, s domain.Sample, seq int, rounds []domain.MeasurementRound) float64 {
	switch d.Metric {
	case "time_deviation":
		if s.TimeOffset < 0 {
			return -s.TimeOffset
		}
		return s.TimeOffset
	case "frequency_deviation":
		if s.FrequencyOffset < 0 {
			return -s.FrequencyOffset
		}
		return s.FrequencyOffset
	default:
		latest := -1
		v := 0.0
		var sampledAt time.Time
		for _, r := range rounds {
			if r.Sequence < seq && r.Sequence > latest {
				if x, ok := sampleFor(r, d.DeviceID); ok {
					latest = r.Sequence
					v = x.TimeOffset
					sampledAt = x.SampledAt
				}
			}
		}
		if latest < 0 {
			return 0
		}
		seconds := s.SampledAt.Sub(sampledAt).Seconds()
		if seconds <= 0 {
			return math.Inf(1)
		}
		x := (s.TimeOffset - v) / seconds
		if x < 0 {
			return -x
		}
		return x
	}
}

type MaterialCheck struct {
	Code    string `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}
type ArchivePreflight struct {
	CampaignID     string          `json:"campaign_id"`
	Revision       int64           `json:"revision"`
	Checks         []MaterialCheck `json:"checks"`
	Ready          bool            `json:"ready"`
	ReadinessToken string          `json:"readiness_token,omitempty"`
	ExpiresAt      time.Time       `json:"expires_at"`
	Counts         map[string]int  `json:"counts"`
	SchemaVersion  string          `json:"schema_version"`
}

func (s *Service) ArchivePreflight(id string) (*ArchivePreflight, error) {
	snap, e := s.Snapshot(id, "all")
	if e != nil {
		return nil, e
	}
	checks := []MaterialCheck{}
	add := func(code string, ok bool) {
		st := "READY"
		if !ok {
			st = "BLOCKED"
		}
		checks = append(checks, MaterialCheck{Code: code, Status: st})
	}
	add("CAMPAIGN_REVIEW_APPROVED", snap.Campaign.State == domain.ReviewApproved)
	add("REFERENCE_COVERAGE", snap.Coverage.Complete)
	add("MEASUREMENT_ROUNDS", len(snap.Rounds) > 0)
	add("LATEST_EVALUATION", snap.Evaluation != nil)
	closed := true
	for _, d := range snap.Deviations {
		if d.Status != "CLOSED" {
			closed = false
		}
	}
	add("DEVIATIONS_CLOSED", closed)
	approved := false
	for _, r := range snap.Reviews {
		if r.Approved {
			approved = true
		}
	}
	add("INDEPENDENT_REVIEW", approved)
	report := audit.ValidateDetailed(snap.Audit, snap.Campaign.Revision)
	add("AUDIT_CHAIN", report.Valid)
	ready := true
	for _, c := range checks {
		if c.Status == "BLOCKED" {
			ready = false
		}
	}
	exp := time.Now().UTC().Add(5 * time.Minute)
	input := ""
	if snap.Evaluation != nil {
		input = snap.Evaluation.InputSummary
	}
	token := strconv.FormatInt(exp.Unix(), 10) + "." + domain.Hash([]byte(fmt.Sprintf("%s|%d|%s|%s|%d", id, snap.Campaign.Revision, input, report.HeadDigest, exp.Unix())))
	evaluationCount := 0
	if all, err := s.Store.Evaluations(id); err == nil {
		evaluationCount = len(all)
	}
	out := &ArchivePreflight{id, snap.Campaign.Revision, checks, ready, "", exp, map[string]int{"references": len(snap.References), "rounds": len(snap.Rounds), "round_voids": len(snap.RoundVoids), "evaluations": evaluationCount, "deviations": len(snap.Deviations), "reviews": len(snap.Reviews), "review_claims": len(snap.ReviewClaims), "audit_events": len(snap.Audit)}, audit.ArtifactSchemaVersion}
	if ready {
		out.ReadinessToken = token
	}
	return out, nil
}
func (s *Service) ArchiveWithToken(id string, revision int64, token string) (*domain.Artifact, error) {
	p, e := s.ArchivePreflight(id)
	if e != nil {
		return nil, e
	}
	if !p.Ready {
		return nil, errors.New("archive materials blocked")
	}
	if token != "" {
		parts := strings.Split(token, ".")
		if len(parts) != 2 {
			return nil, errors.New("readiness token expired or stale")
		}
		unix, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil || time.Now().UTC().Unix() > unix {
			return nil, errors.New("readiness token expired or stale")
		}
		snap, snapErr := s.Snapshot(id, "all")
		if snapErr != nil {
			return nil, snapErr
		}
		input := ""
		if snap.Evaluation != nil {
			input = snap.Evaluation.InputSummary
		}
		report := audit.ValidateDetailed(snap.Audit, snap.Campaign.Revision)
		expected := domain.Hash([]byte(fmt.Sprintf("%s|%d|%s|%s|%d", id, snap.Campaign.Revision, input, report.HeadDigest, unix)))
		if parts[1] != expected {
			return nil, errors.New("readiness token expired or stale")
		}
	}
	return s.Archive(id, revision)
}
