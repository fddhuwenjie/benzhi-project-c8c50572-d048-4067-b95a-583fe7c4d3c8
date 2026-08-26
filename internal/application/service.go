package application

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type Service struct {
	Store                    *persistence.Store
	Audit                    *audit.Log
	reviewerEligibilityMu    sync.Mutex
	reviewerEligibilityCache map[string]*ReviewerEligibilityResult
}

func New(store *persistence.Store) *Service {
	return &Service{
		Store:                    store,
		Audit:                    &audit.Log{},
		reviewerEligibilityCache: map[string]*ReviewerEligibilityResult{},
	}
}

type Snapshot struct {
	Campaign              *domain.Campaign             `json:"campaign"`
	References            []domain.ReferenceEvidence   `json:"references"`
	Coverage              domain.ReferenceCoverage     `json:"reference_coverage"`
	Rounds                []domain.MeasurementRound    `json:"rounds"`
	RoundVoids            []domain.RoundVoid           `json:"round_voids"`
	Deviations            []domain.DeviationCase       `json:"deviations"`
	Evaluation            *domain.Evaluation           `json:"evaluation,omitempty"`
	Reviews               []domain.Review              `json:"reviews"`
	ReviewClaims          []domain.ReviewClaim         `json:"review_claims"`
	ClaimStatus           domain.ClaimStatus           `json:"claim_status"`
	Artifact              *domain.Artifact             `json:"artifact,omitempty"`
	Audit                 []audit.Event                `json:"audit"`
	Actions               []string                     `json:"actions,omitempty"`
	EvaluationHistoryURL  string                       `json:"evaluation_history_url,omitempty"`
	ReferenceWithdrawals  []domain.ReferenceWithdrawal `json:"reference_withdrawals"`
	MeasurementCompletion domain.MeasurementCompletion `json:"measurement_completion"`
	ReviewFindings        []domain.ReviewFinding       `json:"review_findings"`
	FindingResolutions    []domain.FindingResolution   `json:"finding_resolutions"`
	DeviceBaselines       []domain.DeviceBaseline      `json:"device_baselines"`
	SampleExclusions      []domain.SampleExclusion     `json:"sample_exclusions"`
	RemediationEvidence   []domain.RemediationEvidence `json:"remediation_evidence"`
}
type ReferenceResult struct {
	Campaign          *domain.Campaign         `json:"campaign"`
	Coverage          domain.ReferenceCoverage `json:"coverage"`
	Fingerprint       string                   `json:"certificate_fingerprint"`
	SourceCampaignIDs []string                 `json:"source_campaign_ids"`
}
type EvaluationResult struct {
	Campaign   *domain.Campaign       `json:"campaign"`
	Evaluation domain.Evaluation      `json:"evaluation"`
	Deviations []domain.DeviationCase `json:"deviations"`
}
type RemediationResult struct {
	Campaign  *domain.Campaign       `json:"campaign"`
	Closed    []domain.DeviationCase `json:"closed"`
	Remaining []domain.DeviationCase `json:"remaining"`
}
type ListFilter struct {
	State, Station, ReviewerID string
	CancellationReason         string
	WindowStart, WindowEnd     *time.Time
	Offset, Limit              int
}
type ListResult struct {
	Campaigns []*domain.Campaign `json:"campaigns"`
	Total     int                `json:"total"`
	Offset    int                `json:"offset"`
	Limit     int                `json:"limit"`
}
type AuditFilter struct {
	RevisionStart, RevisionEnd int64
	Action, Actor              string
	Offset, Limit              int
}
type AuditResult struct {
	Events    []audit.Event         `json:"events"`
	Total     int                   `json:"total"`
	Offset    int                   `json:"offset"`
	Limit     int                   `json:"limit"`
	Integrity audit.IntegrityReport `json:"integrity"`
}
type CreateInput struct {
	CampaignID, StationCode string
	Start, End              time.Time
	Devices                 []string
	Threshold               domain.ThresholdProfile
	By                      string
}

func requestHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func idemKey(action, campaignID, key string) string {
	if key == "" {
		return ""
	}
	return action + "|" + campaignID + "|" + key
}
func (s *Service) replay(key, hash string, out any) (bool, error) {
	if key == "" {
		return false, nil
	}
	old, ok, err := s.Store.GetIdemHash(key)
	if err != nil {
		return false, err
	}
	if ok && old != hash {
		return false, errors.New("idempotency key conflict")
	}
	if !ok {
		return false, nil
	}
	return s.Store.GetIdem(key, out)
}
func (s *Service) newEvent(c *domain.Campaign, action, actor, summary string) (audit.Event, error) {
	events, err := s.Store.Audits(c.CampaignID)
	if err != nil {
		return audit.Event{}, err
	}
	previous := ""
	if len(events) > 0 {
		previous = events[len(events)-1].Digest
	}
	return audit.NewEventWithSummary(c.CampaignID, c.Revision, action, actor, previous, summary, time.Now().UTC()), nil
}
func (s *Service) get(id string) (*domain.Campaign, error) { return s.Store.GetCampaign(id) }
func (s *Service) check(c *domain.Campaign, rev int64) error {
	if rev > 0 && c.Revision != rev {
		return domain.ErrConflict
	}
	if c.State == domain.Archived {
		return errors.New("archived")
	}
	if c.State == domain.Cancelled {
		return errors.New("cancelled")
	}
	return nil
}

func (s *Service) Create(input CreateInput, requestID string) (*domain.Campaign, error) {
	return s.createWithPlan(input, nil, requestID, false)
}

func (s *Service) CreateWithPlan(input CreateInput, measurementPlan *domain.MeasurementPlan, requestID string) (*domain.Campaign, error) {
	return s.createWithPlan(input, measurementPlan, requestID, true)
}

func (s *Service) createWithPlan(input CreateInput, measurementPlan *domain.MeasurementPlan, requestID string, useDefault bool) (*domain.Campaign, error) {
	hash, key := requestHash(struct {
		Input CreateInput             `json:"input"`
		Plan  *domain.MeasurementPlan `json:"measurement_plan,omitempty"`
	}{input, measurementPlan}), idemKey("create", input.CampaignID, requestID)
	var old domain.Campaign
	if ok, err := s.replay(key, hash, &old); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return &old, nil
	}
	c, err := domain.NewCampaign(input.CampaignID, input.StationCode, input.Start, input.End, input.Devices, input.Threshold, input.By, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	plan := domain.MeasurementPlan{}
	if useDefault {
		plan = domain.DefaultMeasurementPlan()
	}
	if measurementPlan != nil {
		plan, err = domain.NormalizeMeasurementPlan(*measurementPlan)
		if err != nil {
			return nil, err
		}
		c.MeasurementPlanLocked = true
	}
	c.MeasurementPlan = plan
	event := audit.NewEvent(c.CampaignID, c.Revision, "CREATE", input.By, "", time.Now().UTC())
	conflicts, err := s.Store.CreateCampaignAtomic(c, event, key, hash)
	if errors.Is(err, persistence.ErrResourceConflict) {
		var replayed domain.Campaign
		if ok, replayErr := s.replay(key, hash, &replayed); ok || replayErr != nil {
			if replayErr != nil {
				return nil, replayErr
			}
			return &replayed, nil
		}
		return nil, &domain.ConflictError{Conflicts: conflicts}
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: campaigns.id") {
			return nil, domain.ErrAlreadyExists
		}
		return nil, err
	}
	return c, nil
}

func (s *Service) Reference(id, requestID string, evidence domain.ReferenceEvidence, revision int64) (*domain.Campaign, error) {
	result, err := s.ReferenceDetailed(id, requestID, evidence, revision)
	if err != nil {
		return nil, err
	}
	return result.Campaign, nil
}
func (s *Service) ReferenceDetailed(id, requestID string, evidence domain.ReferenceEvidence, revision int64) (*ReferenceResult, error) {
	evidence.CampaignID = id
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if c.State == domain.Archived {
		return nil, errors.New("archived")
	}
	if c.State == domain.Cancelled {
		return nil, errors.New("cancelled")
	}
	hash, key := requestHash(evidence), idemKey("reference", id, requestID)
	var old ReferenceResult
	if ok, err := s.replay(key, hash, &old); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return &old, nil
	}
	if err = s.check(c, revision); err != nil {
		return nil, err
	}
	if evidence.SubmittedAt.IsZero() {
		evidence.SubmittedAt = time.Now().UTC()
	}
	if err = c.AddReference(evidence, time.Now().UTC()); err != nil {
		return nil, err
	}
	sources, err := s.certificateSources(evidence)
	if err != nil {
		return nil, err
	}
	refs, err := s.Store.References(id)
	if err != nil {
		return nil, err
	}
	for _, existing := range refs {
		if existing.EvidenceID == evidence.EvidenceID || strings.EqualFold(existing.CertificateDigest, evidence.CertificateDigest) {
			return nil, domain.ErrDuplicate
		}
	}
	withdrawals, err := s.Store.ReferenceWithdrawals(id)
	if err != nil {
		return nil, err
	}
	all := append(domain.EffectiveReferences(refs, withdrawals), evidence)
	if err = c.ValidateReferences(all); err != nil && !errors.Is(err, domain.ErrCoverage) {
		return nil, err
	}
	coverage := c.ReferenceCoverage(all)
	c.Revision++
	if coverage.Complete {
		c.State = domain.ReferenceVerified
	}
	result := &ReferenceResult{Campaign: c, Coverage: coverage, Fingerprint: domain.CertificateFingerprint(evidence), SourceCampaignIDs: sources}
	event, err := s.newEvent(c, "REFERENCE", evidence.SubmittedBy, requestHash(map[string]any{"coverage": coverage, "certificate_fingerprint": domain.CertificateFingerprint(evidence), "source_campaign_count": len(sources)}))
	if err != nil {
		return nil, err
	}
	if err = s.Store.Commit(persistence.Mutation{Campaign: c, References: []domain.ReferenceEvidence{evidence}, Event: &event, IdemKey: key, IdemHash: hash, Response: result}); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) Measure(id string, round domain.MeasurementRound, revision int64) (*domain.Campaign, error) {
	return s.MeasureBatch(id, []domain.MeasurementRound{round}, revision)
}
func (s *Service) MeasureBatch(id string, batch []domain.MeasurementRound, revision int64) (*domain.Campaign, error) {
	return s.MeasureIdem(id, batch, revision, "", requestHash(batch))
}
func (s *Service) MeasureIdem(id string, batch []domain.MeasurementRound, revision int64, requestID, _ string) (*domain.Campaign, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if c.State == domain.Archived {
		return nil, errors.New("archived")
	}
	if c.State == domain.Cancelled {
		return nil, errors.New("cancelled")
	}
	hash, key := requestHash(batch), idemKey("measure", id, requestID)
	var old domain.Campaign
	if ok, err := s.replay(key, hash, &old); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return &old, nil
	}
	if err = s.check(c, revision); err != nil {
		return nil, err
	}
	for i := range batch {
		batch[i].CampaignID = id
		if batch[i].Purpose == "" {
			batch[i].Purpose = "original"
		}
		if batch[i].Purpose != "original" {
			return nil, domain.ErrInvalid
		}
		if batch[i].CapturedAt.IsZero() {
			batch[i].CapturedAt = time.Now().UTC()
		}
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return nil, err
	}
	voids, err := s.Store.RoundVoids(id)
	if err != nil {
		return nil, err
	}
	replaced := map[string]bool{}
	for _, historical := range rounds {
		if historical.ReplacementForRoundID != "" {
			replaced[historical.ReplacementForRoundID] = true
		}
	}
	voidRound := map[string]domain.MeasurementRound{}
	for _, historical := range rounds {
		voidRound[historical.RoundID] = historical
	}
	for i := range batch {
		item := &batch[i]
		for _, historical := range rounds {
			if item.RoundID == historical.RoundID || item.Sequence == historical.Sequence {
				return nil, domain.ErrInvalid
			}
		}
		if item.ReplacementForRoundID == "" {
			for _, v := range voids {
				if replaced[v.RoundID] {
					continue
				}
				old := voidRound[v.RoundID]
				matched := false
				for _, sample := range item.Samples {
					for _, oldSample := range old.Samples {
						if sample.DeviceID == oldSample.DeviceID {
							matched = true
						}
					}
				}
				if matched {
					item.ReplacementForRoundID = v.RoundID
					replaced[v.RoundID] = true
					break
				}
			}
		}
		if item.ReplacementForRoundID != "" {
			found := false
			for _, v := range voids {
				if v.RoundID == item.ReplacementForRoundID {
					found = true
				}
			}
			if !found {
				return nil, domain.ErrInvalid
			}
		}
	}
	exclusions, _ := s.Store.SampleExclusions(id)
	if err = c.AddMeasurementBatch(batch, effectiveWithoutExclusions(rounds, voids, exclusions)); err != nil {
		return nil, err
	}
	allRounds := append(effectiveWithoutExclusions(rounds, voids, exclusions), batch...)
	completion, planIssues := domain.MeasurementPlanCompliance(c, allRounds)
	if c.MeasurementPlanLocked && len(planIssues) > 0 {
		return nil, &domain.ValidationError{Issues: planIssues}
	}
	if completion.Complete {
		c.State = domain.Measured
	} else {
		c.State = domain.ReferenceVerified
	}
	actor := ""
	if len(batch) > 0 {
		actor = batch[0].OperatorID
	}
	event, err := s.newEvent(c, "MEASURE", actor, requestHash(struct {
		Batch      []domain.MeasurementRound    `json:"batch"`
		Plan       domain.MeasurementPlan       `json:"measurement_plan"`
		Compliance domain.MeasurementCompletion `json:"compliance"`
	}{batch, c.MeasurementPlan, completion}))
	if err != nil {
		return nil, err
	}
	if err = s.Store.Commit(persistence.Mutation{Campaign: c, Rounds: batch, Event: &event, IdemKey: key, IdemHash: hash, Response: c}); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) Evaluate(id string, revision int64) ([]domain.DeviationCase, *domain.Campaign, error) {
	result, err := s.EvaluateIdem(id, revision, "")
	if err != nil {
		return nil, nil, err
	}
	return result.Deviations, result.Campaign, nil
}
func (s *Service) EvaluateIdem(id string, revision int64, requestID string) (*EvaluationResult, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if c.State == domain.Archived {
		return nil, errors.New("archived")
	}
	key := idemKey("evaluate", id, requestID)
	hash := requestHash(struct {
		Revision int64 `json:"expected_revision"`
	}{revision})
	var old EvaluationResult
	if ok, err := s.replay(key, hash, &old); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return &old, nil
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
	probe := *c
	if probe.State != domain.Measured && probe.State != domain.RemediationRequired {
		if latest, e := s.Store.LatestEvaluation(id); e == nil {
			deviations, _ := s.Store.Deviations(id)
			return &EvaluationResult{Campaign: c, Evaluation: *latest, Deviations: deviations}, nil
		}
	}
	evaluation, deviations, err := probe.BuildEvaluation(rounds, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if existing, e := s.Store.Evaluation(id, evaluation.InputSummary); e == nil {
		stored, _ := s.Store.Deviations(id)
		return &EvaluationResult{Campaign: c, Evaluation: *existing, Deviations: stored}, nil
	} else if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if err = s.check(c, revision); err != nil {
		return nil, err
	}
	*c = probe
	result := &EvaluationResult{Campaign: c, Evaluation: evaluation, Deviations: deviations}
	failures := 0
	for _, metric := range evaluation.Metrics {
		if metric.Result == "FAIL" {
			failures++
		}
	}
	attributionSummary, _ := json.Marshal(map[string]any{"input_summary": evaluation.InputSummary, "metrics_digest": requestHash(evaluation.Metrics), "failures": failures})
	event, err := s.newEvent(c, "EVALUATE", c.CreatedBy, string(attributionSummary))
	if err != nil {
		return nil, err
	}
	if err = s.Store.Commit(persistence.Mutation{Campaign: c, Deviations: deviations, Evaluation: &evaluation, Event: &event, IdemKey: key, IdemHash: hash, Response: result}); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) Remediate(id string, cases []domain.DeviationCase, retest domain.MeasurementRound, revision int64) (*domain.Campaign, error) {
	result, err := s.RemediateDetailed(id, cases, retest, revision)
	if err != nil {
		return nil, err
	}
	return result.Campaign, nil
}
func (s *Service) RemediateDetailed(id string, cases []domain.DeviationCase, retest domain.MeasurementRound, revision int64) (*RemediationResult, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if err = s.check(c, revision); err != nil {
		return nil, err
	}
	if c.State != domain.RemediationRequired || len(cases) == 0 {
		return nil, domain.ErrState
	}
	existingCases, err := s.Store.Deviations(id)
	if err != nil {
		return nil, err
	}
	existingRounds, err := s.Store.Rounds(id)
	if err != nil {
		return nil, err
	}
	retest.CampaignID = id
	if retest.Purpose != "" && retest.Purpose != "retest" {
		return nil, domain.ErrInvalid
	}
	retest.Purpose = "retest"
	if retest.CapturedAt.IsZero() {
		retest.CapturedAt = time.Now().UTC()
	}
	probe := *c
	if err = probe.AddMeasurementBatch([]domain.MeasurementRound{retest}, existingRounds); err != nil {
		return nil, err
	}
	byID := map[string]domain.DeviationCase{}
	for _, item := range existingCases {
		byID[item.DeviationID] = item
	}
	updates := make([]domain.DeviationCase, 0, len(cases))
	submitted := map[string]bool{}
	for _, supplied := range cases {
		original, ok := byID[supplied.DeviationID]
		if !ok || original.Status != "OPEN" || submitted[supplied.DeviationID] {
			return nil, domain.ErrInvalid
		}
		submitted[supplied.DeviationID] = true
		if strings.TrimSpace(supplied.RootCause) == "" || strings.TrimSpace(supplied.Containment) == "" || strings.TrimSpace(supplied.CorrectiveAction) == "" {
			return nil, domain.ErrInvalid
		}
		sample, ok := sampleFor(retest, original.DeviceID)
		if !ok || !passesRetest(original, sample, retest.Sequence, existingRounds) {
			return nil, errors.New("retest threshold not satisfied")
		}
		original.RootCause, original.Containment, original.CorrectiveAction = supplied.RootCause, supplied.Containment, supplied.CorrectiveAction
		original.RetestRoundID, original.Status = retest.RoundID, "CLOSED"
		byID[original.DeviationID] = original
		updates = append(updates, original)
	}
	remaining := false
	remainingCases := make([]domain.DeviationCase, 0)
	for _, item := range byID {
		if item.Status == "OPEN" {
			remaining = true
			remainingCases = append(remainingCases, item)
		}
	}
	sort.Slice(remainingCases, func(i, j int) bool { return remainingCases[i].DeviationID < remainingCases[j].DeviationID })
	sort.Slice(updates, func(i, j int) bool { return updates[i].DeviationID < updates[j].DeviationID })
	c.Revision++
	if !remaining {
		c.State = domain.ReviewPending
	}
	event, err := s.newEvent(c, "REMEDIATE", retest.OperatorID, requestHash(updates))
	if err != nil {
		return nil, err
	}
	if err = s.Store.Commit(persistence.Mutation{Campaign: c, Rounds: []domain.MeasurementRound{retest}, Deviations: updates, Event: &event}); err != nil {
		return nil, err
	}
	return &RemediationResult{Campaign: c, Closed: updates, Remaining: remainingCases}, nil
}
func sampleFor(round domain.MeasurementRound, deviceID string) (domain.Sample, bool) {
	for _, sample := range round.Samples {
		if sample.DeviceID == deviceID {
			return sample, true
		}
	}
	return domain.Sample{}, false
}
func passesRetest(deviation domain.DeviationCase, sample domain.Sample, sequence int, rounds []domain.MeasurementRound) bool {
	switch deviation.Metric {
	case "time_deviation":
		return math.Abs(sample.TimeOffset) <= deviation.LimitValue
	case "frequency_deviation":
		return math.Abs(sample.FrequencyOffset) <= deviation.LimitValue
	case "drift_slope":
		latestSequence := -1
		latestValue := 0.0
		var latestAt time.Time
		for _, round := range rounds {
			if round.Sequence >= sequence || round.Sequence <= latestSequence {
				continue
			}
			if old, ok := sampleFor(round, deviation.DeviceID); ok {
				latestSequence, latestValue, latestAt = round.Sequence, old.TimeOffset, old.SampledAt
			}
		}
		seconds := sample.SampledAt.Sub(latestAt).Seconds()
		return latestSequence > 0 && seconds > 0 && math.Abs((sample.TimeOffset-latestValue)/seconds) <= deviation.LimitValue
	default:
		return false
	}
}

func (s *Service) Review(id string, review domain.Review, revision int64) (*domain.Campaign, error) {
	return s.ReviewIdem(id, review, revision, "")
}
func (s *Service) ReviewIdem(id string, review domain.Review, revision int64, requestID string) (*domain.Campaign, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if c.State == domain.Archived {
		return nil, errors.New("archived")
	}
	hash, key := requestHash(review), idemKey("review", id, requestID)
	var old domain.Campaign
	if ok, err := s.replay(key, hash, &old); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return &old, nil
	}
	if err = s.check(c, revision); err != nil {
		return nil, err
	}
	if review.SnapshotDigest != "" {
		if review.SnapshotRevision != 0 && review.SnapshotRevision != c.Revision {
			return nil, errors.New("stale snapshot")
		}
		snap, se := s.Store.ReviewSnapshot(id, c.Revision)
		if se != nil || snap.SnapshotDigest != review.SnapshotDigest {
			return nil, errors.New("stale snapshot")
		}
		if snap.ReviewerID != review.ReviewerID {
			return nil, errors.New("reviewer mismatch")
		}
		if !time.Now().UTC().Before(snap.ExpiresAt) {
			return nil, errors.New("claim expired")
		}
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return nil, err
	}
	conflictingRounds := []string{}
	for _, round := range rounds {
		if round.OperatorID == review.ReviewerID {
			conflictingRounds = append(conflictingRounds, round.RoundID)
		}
	}
	if len(conflictingRounds) > 0 {
		sort.Strings(conflictingRounds)
		return nil, &ReviewerIndependenceError{RoundIDs: conflictingRounds}
	}
	var consumed *domain.ReviewClaim
	if claim, claimErr := s.Store.CurrentReviewClaim(id); claimErr == nil {
		now := time.Now().UTC()
		if claim.Status == "ACTIVE" && now.Before(claim.ExpiresAt) {
			if claim.ReviewerID != review.ReviewerID {
				return nil, ErrReviewClaimConflict
			}
			copy := *claim
			copy.Version++
			copy.Status = "CONSUMED"
			consumed = &copy
		}
	} else if !errors.Is(claimErr, sql.ErrNoRows) {
		return nil, claimErr
	}
	if len(review.Checklist) > 0 {
		required := map[string]bool{"REFERENCE_TRACEABILITY": false, "MEASUREMENT_COVERAGE": false, "EVALUATION_REPRODUCIBILITY": false, "REMEDIATION_CLOSURE": false}
		failed := false
		for i := range review.Checklist {
			item := review.Checklist[i]
			if item.CheckCode == "EVALUATION_REPRODUCIBLE" {
				item.CheckCode = "EVALUATION_REPRODUCIBILITY"
			}
			if item.CheckCode == "REMEDIATION_CLOSED" {
				item.CheckCode = "REMEDIATION_CLOSURE"
			}
			review.Checklist[i] = item
			if _, ok := required[item.CheckCode]; !ok || (item.Result != "PASS" && item.Result != "FAIL") {
				return nil, domain.ErrInvalid
			}
			required[item.CheckCode] = true
			if item.Result == "FAIL" {
				failed = true
				if len([]rune(strings.TrimSpace(item.Note))) < 5 {
					return nil, domain.ErrInvalid
				}
			}
		}
		for _, ok := range required {
			if !ok {
				return nil, domain.ErrInvalid
			}
		}
		sort.Slice(review.Checklist, func(i, j int) bool { return review.Checklist[i].CheckCode < review.Checklist[j].CheckCode })
		if failed {
			review.Approved = false
			if len([]rune(strings.TrimSpace(review.Reason))) < 5 {
				return nil, domain.ErrInvalid
			}
		} else if !review.Approved {
			return nil, domain.ErrInvalid
		}
	}
	previous, _ := s.Store.Reviews(id)
	if review.Approved && len(previous) > 0 {
		persistedFindings, findingErr := s.Store.ReviewFindings(id)
		if findingErr != nil {
			return nil, findingErr
		}
		if len(persistedFindings) == 0 {
			last := previous[len(previous)-1]
			for _, oldItem := range last.Checklist {
				if oldItem.Result == "FAIL" {
					resolved := false
					for _, item := range review.Checklist {
						if item.CheckCode == oldItem.CheckCode && strings.TrimSpace(item.Resolution) != "" {
							resolved = true
						}
					}
					if !resolved {
						return nil, domain.ErrInvalid
					}
				}
			}
		}
	}
	review.SignedAt = time.Now().UTC()
	if err = c.Review(review); err != nil {
		return nil, err
	}
	review.Revision = c.Revision
	findings := []domain.ReviewFinding{}
	if !review.Approved {
		findings = BuildReviewFindings(id, review, c.Revision, review.SignedAt)
	}
	if review.Approved {
		allFindings, findErr := s.Store.ReviewFindings(id)
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
		referenced := map[string]bool{}
		for _, findingID := range review.FindingIDs {
			if referenced[findingID] {
				return nil, domain.ErrInvalid
			}
			referenced[findingID] = true
		}
		for _, finding := range allFindings {
			if !resolved[finding.FindingID] || !referenced[finding.FindingID] {
				return nil, domain.ErrInvalid
			}
		}
	}
	if consumed != nil {
		consumed.Revision = c.Revision
	}
	action := "REVIEW"
	if review.SnapshotDigest != "" {
		action = "REVIEW_SNAPSHOT_SIGNED"
	}
	if consumed != nil && review.SnapshotDigest == "" {
		action = "REVIEW_CLAIM_CONSUMED"
	}
	event, err := s.newEvent(c, action, review.ReviewerID, requestHash(review))
	if err != nil {
		return nil, err
	}
	claims := []domain.ReviewClaim{}
	if consumed != nil {
		claims = append(claims, *consumed)
	}
	if err = s.Store.Commit(persistence.Mutation{Campaign: c, Review: &review, ReviewFindings: findings, ReviewClaims: claims, Event: &event, IdemKey: key, IdemHash: hash, Response: c}); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) Archive(id string, revision int64) (*domain.Artifact, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if err = s.check(c, revision); err != nil {
		return nil, err
	}
	if err = c.Archive(); err != nil {
		return nil, err
	}
	event, err := s.newEvent(c, "ARCHIVE", c.CreatedBy, "")
	if err != nil {
		return nil, err
	}
	events, err := s.Store.Audits(id)
	if err != nil {
		return nil, err
	}
	events = append(events, event)
	reviewer := ""
	if reviews, e := s.Store.Reviews(id); e == nil {
		for i := len(reviews) - 1; i >= 0; i-- {
			if reviews[i].Approved {
				reviewer = reviews[i].ReviewerID
				break
			}
		}
	}
	refs, err := s.Store.References(id)
	if err != nil {
		return nil, err
	}
	rounds, err := s.Store.Rounds(id)
	if err != nil {
		return nil, err
	}
	voids, err := s.Store.RoundVoids(id)
	if err != nil {
		return nil, err
	}
	evaluations, err := s.Store.Evaluations(id)
	if err != nil {
		return nil, err
	}
	deviations, err := s.Store.Deviations(id)
	if err != nil {
		return nil, err
	}
	plans, err := s.Store.Plans(id)
	if err != nil {
		return nil, err
	}
	reviews, err := s.Store.Reviews(id)
	if err != nil {
		return nil, err
	}
	claims, err := s.Store.ReviewClaims(id)
	if err != nil {
		return nil, err
	}
	withdrawals, err := s.Store.ReferenceWithdrawals(id)
	if err != nil {
		return nil, err
	}
	findings, err := s.Store.ReviewFindings(id)
	if err != nil {
		return nil, err
	}
	resolutions, err := s.Store.FindingResolutions(id)
	if err != nil {
		return nil, err
	}
	baselines, _ := s.Store.DeviceBaselines(id)
	remEvidence, _ := s.Store.RemediationEvidence(id)
	exclusions, _ := s.Store.SampleExclusions(id)
	artifact, err := audit.ArtifactWithSections(audit.ArtifactSource{Campaign: c, References: refs, Rounds: rounds, RoundVoids: voids, Evaluations: evaluations, Deviations: deviations, Plans: plans, Reviews: reviews, Claims: claims, Events: events, Withdrawals: withdrawals, Findings: findings, Resolutions: resolutions, Baselines: baselines, RemediationEvidence: remEvidence, Exclusions: exclusions}, reviewer, event.Digest)
	if err != nil {
		return nil, err
	}
	if err = s.Store.Commit(persistence.Mutation{Campaign: c, Artifact: &artifact, Event: &event}); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (s *Service) Snapshot(id, include string) (*Snapshot, error) {
	return s.SnapshotForDevice(id, include, "")
}
func (s *Service) SnapshotForDevice(id, include, deviceID string) (*Snapshot, error) {
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{Campaign: c, References: []domain.ReferenceEvidence{}, Rounds: []domain.MeasurementRound{}, RoundVoids: []domain.RoundVoid{}, Deviations: []domain.DeviationCase{}, Reviews: []domain.Review{}, ReviewClaims: []domain.ReviewClaim{}, Audit: []audit.Event{}, Actions: []string{}, ReferenceWithdrawals: []domain.ReferenceWithdrawal{}, ReviewFindings: []domain.ReviewFinding{}, FindingResolutions: []domain.FindingResolution{}, DeviceBaselines: []domain.DeviceBaseline{}, SampleExclusions: []domain.SampleExclusion{}, RemediationEvidence: []domain.RemediationEvidence{}}
	snap.DeviceBaselines, err = s.Store.DeviceBaselines(id)
	if err != nil {
		return nil, err
	}
	snap.SampleExclusions, err = s.Store.SampleExclusions(id)
	if err != nil {
		return nil, err
	}
	snap.RemediationEvidence, err = s.Store.RemediationEvidence(id)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, name := range strings.Split(include, ",") {
		selected[strings.TrimSpace(name)] = true
	}
	all := include == "" || selected["all"]
	if all || selected["references"] {
		snap.References, err = s.Store.References(id)
		if err != nil {
			return nil, err
		}
		snap.ReferenceWithdrawals, err = s.Store.ReferenceWithdrawals(id)
		if err != nil {
			return nil, err
		}
		active := domain.EffectiveReferences(snap.References, snap.ReferenceWithdrawals)
		snap.Coverage = c.ReferenceCoverage(active)
	}
	if all || selected["rounds"] {
		snap.Rounds, err = s.Store.Rounds(id)
		if err != nil {
			return nil, err
		}
		snap.RoundVoids, err = s.Store.RoundVoids(id)
		if err != nil {
			return nil, err
		}
		voidByID := map[string]domain.RoundVoid{}
		for _, v := range snap.RoundVoids {
			voidByID[v.RoundID] = v
		}
		for _, round := range snap.Rounds {
			if round.ReplacementForRoundID != "" {
				v := voidByID[round.ReplacementForRoundID]
				v.ReplacementRoundID = round.RoundID
				voidByID[round.ReplacementForRoundID] = v
			}
		}
		for i := range snap.RoundVoids {
			snap.RoundVoids[i] = voidByID[snap.RoundVoids[i].RoundID]
		}
		for i := range snap.Rounds {
			if v, ok := voidByID[snap.Rounds[i].RoundID]; ok {
				x := v
				snap.Rounds[i].Void = &x
			}
		}
		if deviceID != "" {
			for i := range snap.Rounds {
				samples := make([]domain.Sample, 0)
				for _, sample := range snap.Rounds[i].Samples {
					if sample.DeviceID == deviceID {
						samples = append(samples, sample)
					}
				}
				snap.Rounds[i].Samples = samples
			}
		}
		snap.MeasurementCompletion, _ = domain.MeasurementPlanCompliance(c, effectiveWithoutExclusions(snap.Rounds, snap.RoundVoids, snap.SampleExclusions))
	}
	if all || selected["deviations"] {
		snap.Deviations, err = s.Store.Deviations(id)
		if err != nil {
			return nil, err
		}
		plans, _ := s.Store.Plans(id)
		for i := range snap.Deviations {
			for _, p := range plans {
				if p.DeviationID == snap.Deviations[i].DeviationID {
					snap.Deviations[i].Plans = append(snap.Deviations[i].Plans, p)
				}
			}
		}
	}
	if all || selected["evaluation"] {
		if evaluation, e := s.Store.LatestEvaluation(id); e == nil {
			snap.Evaluation = evaluation
			snap.EvaluationHistoryURL = "/api/v1/campaigns/" + id + "/evaluations"
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	if all || selected["reviews"] {
		snap.Reviews, err = s.Store.Reviews(id)
		if err != nil {
			return nil, err
		}
		snap.ReviewClaims, err = s.Store.ReviewClaims(id)
		if err != nil {
			return nil, err
		}
		var current *domain.ReviewClaim
		if len(snap.ReviewClaims) > 0 {
			current = &snap.ReviewClaims[len(snap.ReviewClaims)-1]
		}
		snap.ClaimStatus = domain.DerivedClaimStatus(current, time.Now().UTC())
		c.ClaimStatus = &snap.ClaimStatus
		snap.ReviewFindings, err = s.Store.ReviewFindings(id)
		if err != nil {
			return nil, err
		}
		snap.FindingResolutions, err = s.Store.FindingResolutions(id)
		if err != nil {
			return nil, err
		}
	}
	if all || selected["artifact"] {
		if artifact, e := s.Store.GetArtifact(id); e == nil {
			snap.Artifact = artifact
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	snap.Audit, err = s.Store.Audits(id)
	if err != nil {
		return nil, err
	}
	report := audit.ValidateDetailed(snap.Audit, c.Revision)
	if !report.Valid {
		return nil, errors.New(report.Error)
	}
	if len(snap.Rounds) > 0 {
		snap.Actions = append(snap.Actions, "measurement-summary")
	}
	if c.State == domain.Draft {
		snap.Actions = append(snap.Actions, "amendments", "reference-preflight", "device-baselines", "reference-batches")
	}
	if c.State == domain.ReferenceVerified || c.State == domain.Measured {
		snap.Actions = append(snap.Actions, "round-voids", "sample-exclusions")
	}
	if c.State == domain.RemediationRequired {
		snap.Actions = append(snap.Actions, "remediation-preflight", "remediation-evidence")
	}
	if c.State == domain.ReviewPending {
		snap.Actions = append(snap.Actions, "review-claims", "review-snapshots")
	}
	return snap, nil
}

func (s *Service) List(state, station string, offset, limit int) ([]*domain.Campaign, error) {
	result, err := s.ListCampaigns(ListFilter{State: state, Station: station, Offset: offset, Limit: limit})
	if result == nil {
		return nil, err
	}
	return result.Campaigns, err
}
func (s *Service) ListCampaigns(filter ListFilter) (*ListResult, error) {
	if filter.Offset < 0 || filter.Limit < 0 || filter.Limit > 100 {
		return nil, domain.ErrInvalid
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	items, total, err := s.Store.QueryCampaigns(persistence.CampaignFilter{State: filter.State, Station: filter.Station, ReviewerID: filter.ReviewerID, CancellationReason: filter.CancellationReason, WindowStart: filter.WindowStart, WindowEnd: filter.WindowEnd, Offset: filter.Offset, Limit: filter.Limit})
	if err != nil {
		return nil, err
	}
	if filter.State == string(domain.ReviewPending) || filter.ReviewerID != "" {
		now := time.Now().UTC()
		for _, item := range items {
			claim, e := s.Store.CurrentReviewClaim(item.CampaignID)
			if e == nil {
				status := domain.DerivedClaimStatus(claim, now)
				item.ClaimStatus = &status
			} else if errors.Is(e, sql.ErrNoRows) {
				status := domain.DerivedClaimStatus(nil, now)
				item.ClaimStatus = &status
			} else {
				return nil, e
			}
		}
	}
	return &ListResult{Campaigns: items, Total: total, Offset: filter.Offset, Limit: filter.Limit}, nil
}

func (s *Service) AuditReport(id string, filter AuditFilter) (*AuditResult, error) {
	if filter.Offset < 0 || filter.Limit < 0 || filter.Limit > 100 {
		return nil, domain.ErrInvalid
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	c, err := s.get(id)
	if err != nil {
		return nil, err
	}
	events, err := s.Store.Audits(id)
	if err != nil {
		return nil, err
	}
	integrity := audit.ValidateDetailed(events, c.Revision)
	filtered := make([]audit.Event, 0)
	for _, event := range events {
		if filter.RevisionStart > 0 && event.Revision < filter.RevisionStart {
			continue
		}
		if filter.RevisionEnd > 0 && event.Revision > filter.RevisionEnd {
			continue
		}
		if filter.Action != "" && event.Action != filter.Action {
			continue
		}
		if filter.Actor != "" && event.Actor != filter.Actor {
			continue
		}
		filtered = append(filtered, event)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Revision < filtered[j].Revision })
	total := len(filtered)
	if filter.Offset >= total {
		filtered = []audit.Event{}
	} else {
		end := filter.Offset + filter.Limit
		if end > total {
			end = total
		}
		filtered = filtered[filter.Offset:end]
	}
	return &AuditResult{Events: filtered, Total: total, Offset: filter.Offset, Limit: filter.Limit, Integrity: integrity}, nil
}

func (s *Service) Verify(id string) map[string]any { return s.VerifySection(id, "") }

func (s *Service) VerifySection(id, section string) map[string]any {
	out := map[string]any{"valid": false}
	artifact, err := s.Store.GetArtifact(id)
	if err != nil {
		out["error"] = "artifact not found"
		return out
	}
	campaign, err := s.Store.GetCampaign(id)
	if err != nil {
		out["error"] = "campaign not found"
		return out
	}
	events, err := s.Store.Audits(id)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	report := audit.ValidateDetailed(events, campaign.Revision)
	verification := audit.VerifyArtifactPayload(artifact.Payload, section)
	out["schema_version"], out["payload_digest"], out["audit_head_digest"], out["integrity"], out["sections"], out["failed_sections"] = artifact.SchemaVersion, artifact.PayloadDigest, artifact.AuditHeadDigest, report, verification.Sections, verification.FailedSections
	if !verification.Valid {
		out["error"] = verification.Error
		if verification.Error == "" {
			out["error"] = "section digest mismatch"
		}
		return out
	}
	if artifact.PayloadDigest != verification.PayloadDigest {
		out["error"] = "artifact metadata digest mismatch"
		return out
	}
	if !report.Valid || report.HeadDigest != artifact.AuditHeadDigest {
		out["error"] = "audit head digest mismatch"
		return out
	}
	refs, e := s.Store.References(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	rounds, e := s.Store.Rounds(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	voids, e := s.Store.RoundVoids(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	evals, e := s.Store.Evaluations(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	deviations, e := s.Store.Deviations(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	plans, e := s.Store.Plans(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	reviews, e := s.Store.Reviews(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	claims, e := s.Store.ReviewClaims(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	withdrawals, e := s.Store.ReferenceWithdrawals(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	findings, e := s.Store.ReviewFindings(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	resolutions, e := s.Store.FindingResolutions(id)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	baselines, _ := s.Store.DeviceBaselines(id)
	remEvidence, _ := s.Store.RemediationEvidence(id)
	exclusions, _ := s.Store.SampleExclusions(id)
	rebuilt, e := audit.ArtifactWithSections(audit.ArtifactSource{Campaign: campaign, References: refs, Rounds: rounds, RoundVoids: voids, Evaluations: evals, Deviations: deviations, Plans: plans, Reviews: reviews, Claims: claims, Events: events, Withdrawals: withdrawals, Findings: findings, Resolutions: resolutions, Baselines: baselines, RemediationEvidence: remEvidence, Exclusions: exclusions}, artifact.ReviewerID, artifact.AuditHeadDigest)
	if e != nil {
		out["error"] = e.Error()
		return out
	}
	expected := map[string]string{}
	counts := map[string]int{}
	for _, m := range artifact.Manifest {
		expected[m.SectionName] = m.Digest
		counts[m.SectionName] = m.RecordCount
	}
	failed := []string{}
	databaseChecks := []audit.SectionVerification{}
	for _, m := range rebuilt.Manifest {
		if section != "" && m.SectionName != section {
			continue
		}
		valid := expected[m.SectionName] == m.Digest
		databaseChecks = append(databaseChecks, audit.SectionVerification{SectionName: m.SectionName, Valid: valid, ExpectedDigest: expected[m.SectionName], ActualDigest: m.Digest, RecordCount: counts[m.SectionName]})
		if !valid {
			failed = append(failed, m.SectionName)
		}
	}
	out["sections"] = databaseChecks
	if len(failed) > 0 {
		out["failed_sections"] = failed
		out["error"] = "artifact snapshot mismatch"
		return out
	}
	if section == "" && rebuilt.PayloadDigest != artifact.PayloadDigest {
		out["error"] = "manifest digest mismatch"
		return out
	}
	out["valid"] = true
	return out
}
