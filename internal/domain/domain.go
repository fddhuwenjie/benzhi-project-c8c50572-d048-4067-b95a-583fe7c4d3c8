package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type State string

const (
	Draft               State = "DRAFT"
	ReferenceVerified   State = "REFERENCE_VERIFIED"
	Measured            State = "MEASURED"
	RemediationRequired State = "REMEDIATION_REQUIRED"
	ReviewPending       State = "REVIEW_PENDING"
	ReviewApproved      State = "REVIEW_APPROVED"
	Archived            State = "ARCHIVED"
	Cancelled           State = "CANCELLED"
)

type ThresholdProfile struct {
	MaxAbsDeviation       float64 `json:"max_abs_deviation"`
	MaxFrequencyDeviation float64 `json:"max_frequency_deviation"`
	MaxDriftSlope         float64 `json:"max_drift_slope"`
}
type Campaign struct {
	CampaignID            string           `json:"campaign_id"`
	StationCode           string           `json:"station_code"`
	MissionWindowStart    time.Time        `json:"mission_window_start"`
	MissionWindowEnd      time.Time        `json:"mission_window_end"`
	DeviceIDs             []string         `json:"device_ids"`
	Threshold             ThresholdProfile `json:"threshold"`
	State                 State            `json:"state"`
	Revision              int64            `json:"revision"`
	CreatedBy             string           `json:"created_by"`
	CreatedAt             time.Time        `json:"created_at"`
	ArchivedAt            time.Time        `json:"archived_at,omitempty"`
	ClaimStatus           *ClaimStatus     `json:"claim_status,omitempty"`
	MeasurementPlan       MeasurementPlan  `json:"measurement_plan"`
	MeasurementPlanLocked bool             `json:"measurement_plan_locked"`
	PredecessorCampaignID string           `json:"predecessor_campaign_id,omitempty"`
	PredecessorSummary    string           `json:"predecessor_summary,omitempty"`
	Cancellation          *Cancellation    `json:"cancellation,omitempty"`
}
type ReferenceEvidence struct {
	EvidenceID            string    `json:"evidence_id"`
	CampaignID            string    `json:"campaign_id"`
	ReferenceKind         string    `json:"reference_kind"`
	Provider              string    `json:"provider"`
	CertificateDigest     string    `json:"certificate_digest"`
	SubmittedBy           string    `json:"submitted_by"`
	ValidFrom             time.Time `json:"valid_from"`
	ValidUntil            time.Time `json:"valid_until"`
	SubmittedAt           time.Time `json:"submitted_at"`
	Replaced              bool      `json:"replaced"`
	CorrectionReason      string    `json:"correction_reason,omitempty"`
	ReplacementEvidenceID string    `json:"replacement_evidence_id,omitempty"`
}
type CoverageGap struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}
type ReferenceCoverage struct {
	ClockCovered     bool          `json:"clock_covered"`
	FrequencyCovered bool          `json:"frequency_covered"`
	ClockGaps        []CoverageGap `json:"clock_gaps"`
	FrequencyGaps    []CoverageGap `json:"frequency_gaps"`
	Complete         bool          `json:"complete"`
}
type Sample struct {
	DeviceID        string    `json:"device_id"`
	TimeOffset      float64   `json:"time_offset"`
	FrequencyOffset float64   `json:"frequency_offset"`
	SampledAt       time.Time `json:"sampled_at"`
}
type MeasurementRound struct {
	RoundID               string            `json:"round_id"`
	CampaignID            string            `json:"campaign_id"`
	Purpose               string            `json:"purpose"`
	OperatorID            string            `json:"operator_id"`
	Sequence              int               `json:"sequence"`
	Samples               []Sample          `json:"samples"`
	Environment           map[string]string `json:"environment"`
	CapturedAt            time.Time         `json:"captured_at"`
	Void                  *RoundVoid        `json:"void,omitempty"`
	ReplacementForRoundID string            `json:"replacement_for_round_id,omitempty"`
}
type ResourceConflict struct {
	CampaignID   string    `json:"campaign_id"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	OverlapStart time.Time `json:"overlap_start"`
	OverlapEnd   time.Time `json:"overlap_end"`
	Revision     int64     `json:"revision"`
}
type ConflictError struct {
	Conflicts []ResourceConflict `json:"conflicts"`
}

func (e *ConflictError) Error() string { return "resource window conflict" }

type QualityIssue struct {
	RoundID  string `json:"round_id"`
	DeviceID string `json:"device_id,omitempty"`
	Field    string `json:"field"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}
type PreflightResult struct {
	Submittable          bool           `json:"submittable"`
	Issues               []QualityIssue `json:"issues"`
	EffectiveSpanSeconds float64        `json:"effective_span_seconds"`
	DeviceSampleCounts   map[string]int `json:"device_sample_counts"`
}
type DeviceEvaluationDiff struct {
	DeviceID             string  `json:"device_id"`
	MaxAbsDeviationDelta float64 `json:"max_abs_deviation_delta"`
	MeanFrequencyDelta   float64 `json:"mean_frequency_deviation_delta"`
	DriftSlopeDelta      float64 `json:"drift_slope_delta"`
	Change               string  `json:"change"`
	FromConclusion       string  `json:"from_conclusion,omitempty"`
	ToConclusion         string  `json:"to_conclusion,omitempty"`
}
type RemediationPlan struct {
	DeviationID      string    `json:"deviation_id"`
	Owner            string    `json:"owner"`
	RootCause        string    `json:"root_cause"`
	Containment      string    `json:"containment"`
	CorrectiveAction string    `json:"corrective_action"`
	TargetAt         time.Time `json:"target_at"`
	PlannedAt        time.Time `json:"planned_at"`
	Version          int       `json:"version"`
	RiskStatus       string    `json:"risk_status,omitempty"`
}
type RetestAttempt struct {
	DeviationID   string    `json:"deviation_id"`
	RoundID       string    `json:"round_id"`
	Metric        string    `json:"metric"`
	ObservedValue float64   `json:"observed_value"`
	LimitValue    float64   `json:"limit_value"`
	Result        string    `json:"result"`
	Reason        string    `json:"reason"`
	AttemptedAt   time.Time `json:"attempted_at"`
}
type ReviewItem struct {
	CheckCode      string `json:"check_code"`
	Result         string `json:"result"`
	Note           string `json:"note"`
	DeviceID       string `json:"device_id,omitempty"`
	Resolution     string `json:"resolution,omitempty"`
	Severity       string `json:"severity,omitempty"`
	RequiresRetest bool   `json:"requires_retest,omitempty"`
}
type DeviationCase struct {
	DeviationID      string            `json:"deviation_id"`
	CampaignID       string            `json:"campaign_id"`
	DeviceID         string            `json:"device_id"`
	Metric           string            `json:"metric"`
	RootCause        string            `json:"root_cause"`
	Containment      string            `json:"containment"`
	CorrectiveAction string            `json:"corrective_action"`
	RetestRoundID    string            `json:"retest_round_id"`
	Status           string            `json:"status"`
	ObservedValue    float64           `json:"observed_value"`
	LimitValue       float64           `json:"limit_value"`
	MeanFrequency    float64           `json:"mean_frequency,omitempty"`
	DriftSlope       float64           `json:"drift_slope,omitempty"`
	SampleCount      int               `json:"sample_count,omitempty"`
	Conclusion       string            `json:"conclusion,omitempty"`
	AlgorithmVersion string            `json:"algorithm_version,omitempty"`
	InputSummary     string            `json:"input_summary,omitempty"`
	Plans            []RemediationPlan `json:"plans,omitempty"`
	Attempts         []RetestAttempt   `json:"attempts,omitempty"`
}
type DeviceEvaluation struct {
	DeviceID        string  `json:"device_id"`
	MaxAbsDeviation float64 `json:"max_abs_deviation"`
	MeanFrequency   float64 `json:"mean_frequency_deviation"`
	DriftSlope      float64 `json:"drift_slope"`
	SampleCount     int     `json:"sample_count"`
	Conclusion      string  `json:"conclusion"`
}
type MetricAttribution struct {
	DeviceID      string             `json:"device_id"`
	Metric        string             `json:"metric"`
	ObservedValue float64            `json:"observed_value"`
	LimitValue    float64            `json:"limit_value"`
	Margin        float64            `json:"margin"`
	Result        string             `json:"result"`
	RoundID       string             `json:"round_id,omitempty"`
	Sequence      int                `json:"sequence,omitempty"`
	SampledAt     time.Time          `json:"sampled_at,omitempty"`
	Regression    *RegressionDetails `json:"regression,omitempty"`
}
type RegressionPoint struct {
	RoundID   string    `json:"round_id"`
	Sequence  int       `json:"sequence"`
	SampledAt time.Time `json:"sampled_at"`
	XSeconds  float64   `json:"x_seconds"`
	Value     float64   `json:"value"`
}
type RegressionDetails struct {
	Basis       string            `json:"basis"`
	TimeBase    time.Time         `json:"time_base"`
	Numerator   float64           `json:"numerator"`
	Denominator float64           `json:"denominator"`
	Points      []RegressionPoint `json:"points"`
	InputDigest string            `json:"input_digest"`
}
type evaluationObservation struct {
	sequence int
	roundID  string
	sample   Sample
}
type Evaluation struct {
	CampaignID       string              `json:"campaign_id"`
	AlgorithmVersion string              `json:"algorithm_version"`
	InputSummary     string              `json:"input_summary"`
	Threshold        ThresholdProfile    `json:"threshold"`
	RoundSequences   []int               `json:"round_sequences"`
	Devices          []DeviceEvaluation  `json:"devices"`
	Metrics          []MetricAttribution `json:"metrics"`
	EvaluatedAt      time.Time           `json:"evaluated_at"`
	Revision         int64               `json:"revision"`
}
type Review struct {
	ReviewerID       string       `json:"reviewer_id"`
	Statement        string       `json:"statement"`
	Reason           string       `json:"reason"`
	Approved         bool         `json:"approved"`
	SignedAt         time.Time    `json:"signed_at"`
	Checklist        []ReviewItem `json:"checklist"`
	Revision         int64        `json:"revision"`
	FindingIDs       []string     `json:"finding_ids,omitempty"`
	SnapshotDigest   string       `json:"snapshot_digest,omitempty"`
	SnapshotRevision int64        `json:"snapshot_revision,omitempty"`
}
type Artifact struct {
	ArtifactID      string            `json:"artifact_id"`
	CampaignID      string            `json:"campaign_id"`
	SchemaVersion   string            `json:"schema_version"`
	PayloadDigest   string            `json:"payload_digest"`
	ReviewerID      string            `json:"reviewer_id"`
	AuditHeadDigest string            `json:"audit_head_digest"`
	SignedAt        time.Time         `json:"signed_at"`
	Payload         []byte            `json:"payload"`
	Manifest        []SectionManifest `json:"manifest"`
}

type DeviceBaseline struct {
	CampaignID            string    `json:"campaign_id"`
	DeviceID              string    `json:"device_id"`
	DeviceType            string    `json:"device_type"`
	AssetSerial           string    `json:"asset_serial"`
	FirmwareVersion       string    `json:"firmware_version"`
	CalibrationValidFrom  time.Time `json:"calibration_valid_from,omitempty"`
	CalibrationValidUntil time.Time `json:"calibration_valid_until"`
	RegisteredBy          string    `json:"registered_by"`
	RegisteredAt          time.Time `json:"registered_at"`
	Revision              int64     `json:"revision"`
}

type SampleExclusion struct {
	CampaignID string    `json:"campaign_id"`
	RoundID    string    `json:"round_id"`
	DeviceID   string    `json:"device_id"`
	ReasonCode string    `json:"reason_code"`
	Reason     string    `json:"reason"`
	ExcludedBy string    `json:"excluded_by"`
	ExcludedAt time.Time `json:"excluded_at"`
	Revision   int64     `json:"revision"`
}

func EffectiveRoundsWithExclusions(rounds []MeasurementRound, voids []RoundVoid, exclusions []SampleExclusion) []MeasurementRound {
	out := EffectiveRounds(rounds, voids)
	excluded := map[string]map[string]bool{}
	for _, item := range exclusions {
		if excluded[item.RoundID] == nil {
			excluded[item.RoundID] = map[string]bool{}
		}
		excluded[item.RoundID][item.DeviceID] = true
	}
	for i := range out {
		samples := make([]Sample, 0, len(out[i].Samples))
		for _, sample := range out[i].Samples {
			if !excluded[out[i].RoundID][sample.DeviceID] {
				samples = append(samples, sample)
			}
		}
		out[i].Samples = samples
	}
	return out
}

type RemediationEvidence struct {
	EvidenceID      string    `json:"evidence_id"`
	CampaignID      string    `json:"campaign_id"`
	DeviationID     string    `json:"deviation_id"`
	PlanVersion     int       `json:"plan_version"`
	EvidenceType    string    `json:"evidence_type"`
	ExecutedBy      string    `json:"executed_by"`
	OccurredAt      time.Time `json:"occurred_at"`
	MaterialSummary string    `json:"material_summary"`
	ContentDigest   string    `json:"content_digest"`
	CreatedAt       time.Time `json:"created_at"`
	Revision        int64     `json:"revision"`
}

type ReviewSnapshot struct {
	CampaignID     string          `json:"campaign_id"`
	Revision       int64           `json:"revision"`
	ReviewerID     string          `json:"reviewer_id"`
	ClaimVersion   int64           `json:"claim_version"`
	SnapshotDigest string          `json:"snapshot_digest"`
	ExpiresAt      time.Time       `json:"expires_at"`
	Payload        json.RawMessage `json:"payload"`
	CreatedAt      time.Time       `json:"created_at"`
}
type SectionManifest struct {
	SectionName      string `json:"section_name"`
	RecordCount      int    `json:"record_count"`
	Digest           string `json:"digest"`
	CanonicalVersion string `json:"canonical_version"`
}

var ErrInvalid = errors.New("invalid domain input")
var ErrState = errors.New("invalid state transition")
var ErrConflict = errors.New("revision conflict")
var ErrDuplicate = errors.New("duplicate evidence")
var ErrCoverage = errors.New("evidence window coverage incomplete")
var ErrAlreadyExists = errors.New("campaign already exists")

func NewCampaign(id, station string, start, end time.Time, devices []string, t ThresholdProfile, by string, now time.Time) (*Campaign, error) {
	if id == "" || station == "" || by == "" || len(devices) == 0 || !start.Before(end) || !positiveFinite(t.MaxAbsDeviation) || !positiveFinite(t.MaxFrequencyDeviation) || !positiveFinite(t.MaxDriftSlope) {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, d := range devices {
		if d == "" || seen[d] {
			return nil, ErrInvalid
		}
		seen[d] = true
	}
	return &Campaign{CampaignID: id, StationCode: station, MissionWindowStart: start.UTC(), MissionWindowEnd: end.UTC(), DeviceIDs: append([]string(nil), devices...), Threshold: t, State: Draft, Revision: 1, CreatedBy: by, CreatedAt: now.UTC()}, nil
}
func positiveFinite(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func (c *Campaign) AddReference(e ReferenceEvidence, now time.Time) error {
	if c.State != Draft {
		return ErrState
	}
	if e.EvidenceID == "" || e.Provider == "" || e.SubmittedBy == "" ||
		(e.ReferenceKind != "clock" && e.ReferenceKind != "frequency") ||
		len(e.CertificateDigest) == 0 || len(e.CertificateDigest)%2 != 0 || !isHex(e.CertificateDigest) ||
		!e.ValidFrom.Before(e.ValidUntil) {
		return ErrInvalid
	}
	return nil
}
func (c *Campaign) ValidateReferences(refs []ReferenceEvidence) error {
	seenID, seenDigest, seenTuple := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range refs {
		tuple := e.EvidenceID + "|" + e.ReferenceKind + "|" + e.Provider
		if e.EvidenceID == "" || seenID[e.EvidenceID] || seenDigest[e.CertificateDigest] || seenTuple[tuple] {
			return ErrDuplicate
		}
		if len(e.CertificateDigest) == 0 || len(e.CertificateDigest)%2 != 0 || !isHex(e.CertificateDigest) || !e.ValidFrom.Before(e.ValidUntil) {
			return ErrInvalid
		}
		if e.ReferenceKind != "clock" && e.ReferenceKind != "frequency" {
			return ErrInvalid
		}
		seenID[e.EvidenceID], seenDigest[e.CertificateDigest], seenTuple[tuple] = true, true, true
	}
	if !c.ReferenceCoverage(refs).Complete {
		return ErrCoverage
	}
	return nil
}

func (c *Campaign) ReferenceCoverage(refs []ReferenceEvidence) ReferenceCoverage {
	clock := c.coverageGaps(refs, "clock")
	frequency := c.coverageGaps(refs, "frequency")
	return ReferenceCoverage{ClockCovered: len(clock) == 0, FrequencyCovered: len(frequency) == 0, ClockGaps: clock, FrequencyGaps: frequency, Complete: len(clock) == 0 && len(frequency) == 0}
}

func (c *Campaign) coverageGaps(refs []ReferenceEvidence, kind string) []CoverageGap {
	spans := make([][2]time.Time, 0)
	for _, e := range refs {
		if e.ReferenceKind != kind || !e.ValidFrom.Before(e.ValidUntil) || !e.ValidUntil.After(c.MissionWindowStart) || !e.ValidFrom.Before(c.MissionWindowEnd) {
			continue
		}
		start, end := e.ValidFrom, e.ValidUntil
		if start.Before(c.MissionWindowStart) {
			start = c.MissionWindowStart
		}
		if end.After(c.MissionWindowEnd) {
			end = c.MissionWindowEnd
		}
		spans = append(spans, [2]time.Time{start, end})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0].Before(spans[j][0]) })
	gaps := make([]CoverageGap, 0)
	cursor := c.MissionWindowStart
	for _, span := range spans {
		if span[0].After(cursor) {
			gaps = append(gaps, CoverageGap{Start: cursor, End: span[0]})
		}
		if span[1].After(cursor) {
			cursor = span[1]
		}
	}
	if cursor.Before(c.MissionWindowEnd) {
		gaps = append(gaps, CoverageGap{Start: cursor, End: c.MissionWindowEnd})
	}
	return gaps
}
func (c *Campaign) AddMeasurements(round MeasurementRound, existing []MeasurementRound) error {
	if c.State != ReferenceVerified && c.State != Measured && c.State != RemediationRequired {
		return ErrState
	}
	if round.Sequence <= 0 || round.RoundID == "" || round.OperatorID == "" || len(round.Samples) == 0 {
		return ErrInvalid
	}
	for _, r := range existing {
		if r.Sequence == round.Sequence || r.RoundID == round.RoundID {
			return ErrInvalid
		}
	}
	seen := map[string]bool{}
	for _, s := range round.Samples {
		if seen[s.DeviceID] || !contains(c.DeviceIDs, s.DeviceID) || math.IsNaN(s.TimeOffset) || math.IsInf(s.TimeOffset, 0) || math.IsNaN(s.FrequencyOffset) || math.IsInf(s.FrequencyOffset, 0) || math.Abs(s.TimeOffset) > 1e9 || math.Abs(s.FrequencyOffset) > 1e9 || s.SampledAt.Before(c.MissionWindowStart) || s.SampledAt.After(c.MissionWindowEnd) {
			return ErrInvalid
		}
		seen[s.DeviceID] = true
	}
	c.Revision++
	all := append(append([]MeasurementRound(nil), existing...), round)
	if c.State == ReferenceVerified && MeasurementCoverageFor(c, all).Complete {
		c.State = Measured
	}
	return nil
}
func (c *Campaign) AddMeasurementBatch(batch []MeasurementRound, existing []MeasurementRound) error {
	if len(batch) == 0 {
		return ErrInvalid
	}
	orig := *c
	orig.DeviceIDs = append([]string(nil), c.DeviceIDs...)
	seenSeq, seenID := map[int]bool{}, map[string]bool{}
	all := append([]MeasurementRound(nil), existing...)
	for _, r := range batch {
		if seenSeq[r.Sequence] || seenID[r.RoundID] {
			*c = orig
			return ErrInvalid
		}
		seenSeq[r.Sequence] = true
		seenID[r.RoundID] = true
		if err := c.AddMeasurements(r, all); err != nil {
			*c = orig
			return err
		}
		all = append(all, r)
		c.Revision--
	}
	c.Revision++
	if c.State == ReferenceVerified && MeasurementCoverageFor(c, all).Complete {
		c.State = Measured
	}
	return nil
}

func (c *Campaign) BuildEvaluation(rounds []MeasurementRound, now time.Time) (Evaluation, []DeviationCase, error) {
	if c.State != Measured && c.State != RemediationRequired {
		return Evaluation{}, nil, ErrState
	}
	if len(rounds) == 0 {
		return Evaluation{}, nil, ErrInvalid
	}
	sorted := append([]MeasurementRound(nil), rounds...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Sequence == sorted[j].Sequence {
			return sorted[i].RoundID < sorted[j].RoundID
		}
		return sorted[i].Sequence < sorted[j].Sequence
	})
	for i := range sorted {
		sorted[i].Samples = append([]Sample(nil), sorted[i].Samples...)
		sort.Slice(sorted[i].Samples, func(a, b int) bool { return sorted[i].Samples[a].DeviceID < sorted[i].Samples[b].DeviceID })
	}
	input := struct {
		Algorithm string             `json:"algorithm_version"`
		Threshold ThresholdProfile   `json:"threshold"`
		Rounds    []MeasurementRound `json:"rounds"`
	}{"timesync-eval-v2", c.Threshold, sorted}
	b, _ := json.Marshal(input)
	eval := Evaluation{CampaignID: c.CampaignID, AlgorithmVersion: "timesync-eval-v2", InputSummary: Hash(b), Threshold: c.Threshold, EvaluatedAt: now.UTC(), Devices: []DeviceEvaluation{}, Metrics: []MetricAttribution{}}
	for _, r := range sorted {
		eval.RoundSequences = append(eval.RoundSequences, r.Sequence)
	}
	devices := append([]string(nil), c.DeviceIDs...)
	sort.Strings(devices)
	deviations := make([]DeviationCase, 0)
	hasInsufficient := false
	for _, deviceID := range devices {
		observations := make([]evaluationObservation, 0)
		for _, r := range sorted {
			for _, sample := range r.Samples {
				if sample.DeviceID == deviceID {
					observations = append(observations, evaluationObservation{r.Sequence, r.RoundID, sample})
				}
			}
		}
		sort.SliceStable(observations, func(i, j int) bool {
			if observations[i].sample.SampledAt.Equal(observations[j].sample.SampledAt) {
				if observations[i].sequence == observations[j].sequence {
					return observations[i].roundID < observations[j].roundID
				}
				return observations[i].sequence < observations[j].sequence
			}
			return observations[i].sample.SampledAt.Before(observations[j].sample.SampledAt)
		})
		maxAbs, meanFrequency := 0.0, 0.0
		var timeSource, frequencySource *evaluationObservation
		for _, o := range observations {
			if timeSource == nil || math.Abs(o.sample.TimeOffset) > maxAbs {
				maxAbs = math.Abs(o.sample.TimeOffset)
				copy := o
				timeSource = &copy
			}
			if frequencySource == nil || math.Abs(o.sample.FrequencyOffset) > math.Abs(frequencySource.sample.FrequencyOffset) {
				copy := o
				frequencySource = &copy
			}
			meanFrequency += o.sample.FrequencyOffset
		}
		if len(observations) > 0 {
			meanFrequency /= float64(len(observations))
		}
		drift, regression, regressionOK := regressionDetails(observations)
		if !finite(maxAbs) || !finite(meanFrequency) || !finite(drift) || !regressionOK {
			return Evaluation{}, nil, ErrInvalid
		}
		maxAbs, meanFrequency, drift = roundValue(maxAbs), roundValue(meanFrequency), roundValue(drift)
		conclusion := "PASS"
		if len(observations) < 2 {
			conclusion, hasInsufficient = "INSUFFICIENT", true
		} else if maxAbs > c.Threshold.MaxAbsDeviation || math.Abs(meanFrequency) > c.Threshold.MaxFrequencyDeviation || math.Abs(drift) > c.Threshold.MaxDriftSlope {
			conclusion = "FAIL"
		}
		summary := DeviceEvaluation{DeviceID: deviceID, MaxAbsDeviation: maxAbs, MeanFrequency: meanFrequency, DriftSlope: drift, SampleCount: len(observations), Conclusion: conclusion}
		eval.Devices = append(eval.Devices, summary)
		addMetric := func(metric string, observed, limit float64, source *evaluationObservation, details *RegressionDetails) {
			result := "PASS"
			if observed > limit {
				result = "FAIL"
			}
			item := MetricAttribution{DeviceID: deviceID, Metric: metric, ObservedValue: roundValue(observed), LimitValue: roundValue(limit), Margin: roundValue(limit - observed), Result: result, Regression: details}
			if source != nil {
				item.RoundID = source.roundID
				item.Sequence = source.sequence
				item.SampledAt = source.sample.SampledAt.UTC()
			}
			eval.Metrics = append(eval.Metrics, item)
		}
		addMetric("time_deviation", maxAbs, c.Threshold.MaxAbsDeviation, timeSource, nil)
		addMetric("frequency_deviation", math.Abs(meanFrequency), c.Threshold.MaxFrequencyDeviation, frequencySource, nil)
		addMetric("drift_slope", math.Abs(drift), c.Threshold.MaxDriftSlope, nil, regression)
		if conclusion != "FAIL" {
			continue
		}
		add := func(metric string, observed, limit float64) {
			deviations = append(deviations, DeviationCase{DeviationID: fmt.Sprintf("%s-%s-%s-%s", c.CampaignID, deviceID, metric, eval.InputSummary[:12]), CampaignID: c.CampaignID, DeviceID: deviceID, Metric: metric, ObservedValue: observed, LimitValue: limit, Status: "OPEN", MeanFrequency: meanFrequency, DriftSlope: drift, SampleCount: len(observations), Conclusion: "FAIL", AlgorithmVersion: eval.AlgorithmVersion, InputSummary: eval.InputSummary})
		}
		if maxAbs > c.Threshold.MaxAbsDeviation {
			add("time_deviation", maxAbs, c.Threshold.MaxAbsDeviation)
		}
		if math.Abs(meanFrequency) > c.Threshold.MaxFrequencyDeviation {
			add("frequency_deviation", math.Abs(meanFrequency), c.Threshold.MaxFrequencyDeviation)
		}
		if math.Abs(drift) > c.Threshold.MaxDriftSlope {
			add("drift_slope", math.Abs(drift), c.Threshold.MaxDriftSlope)
		}
	}
	if len(deviations) > 0 {
		c.State = RemediationRequired
	} else if !hasInsufficient {
		c.State = ReviewPending
	}
	c.Revision++
	eval.Revision = c.Revision
	return eval, deviations, nil
}

func regressionDetails(observations []evaluationObservation) (float64, *RegressionDetails, bool) {
	if len(observations) < 2 {
		return 0, &RegressionDetails{Basis: "sampled_at_seconds", Points: []RegressionPoint{}}, true
	}
	base := observations[0].sample.SampledAt.UTC()
	points := make([]RegressionPoint, 0, len(observations))
	var sx, sy, sxy, sx2 float64
	for _, o := range observations {
		x, y := o.sample.SampledAt.Sub(base).Seconds(), o.sample.TimeOffset
		if !finite(x) || !finite(y) {
			return 0, nil, false
		}
		points = append(points, RegressionPoint{o.roundID, o.sequence, o.sample.SampledAt.UTC(), roundValue(x), roundValue(y)})
		sx += x
		sy += y
		sxy += x * y
		sx2 += x * x
	}
	n := float64(len(observations))
	denominator := n*sx2 - sx*sx
	numerator := n*sxy - sx*sy
	basis := "sampled_at_seconds"
	if denominator == 0 {
		basis = "sequence_fallback"
		sx, sy, sxy, sx2 = 0, 0, 0, 0
		for i, o := range observations {
			x, y := float64(o.sequence), o.sample.TimeOffset
			points[i].XSeconds = roundValue(x)
			sx += x
			sy += y
			sxy += x * y
			sx2 += x * x
		}
		denominator = n*sx2 - sx*sx
		numerator = n*sxy - sx*sy
	}
	if denominator <= 0 || !finite(numerator) || !finite(denominator) {
		return 0, nil, false
	}
	details := &RegressionDetails{Basis: basis, TimeBase: base, Numerator: roundValue(numerator), Denominator: roundValue(denominator), Points: points, InputDigest: canonicalDigest(points)}
	value := numerator / denominator
	return value, details, finite(value)
}
func regressionSlope(observations []evaluationObservation) float64 {
	value, _, ok := regressionDetails(observations)
	if !ok {
		return math.NaN()
	}
	return value
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func (c *Campaign) Evaluate(rounds []MeasurementRound) ([]DeviationCase, error) {
	_, out, err := c.BuildEvaluation(rounds, time.Now().UTC())
	return out, err
}
func (c *Campaign) Remediate(cases []DeviationCase, retest []MeasurementRound) error {
	if c.State != RemediationRequired {
		return ErrState
	}
	if len(cases) == 0 {
		return ErrInvalid
	}
	for _, dc := range cases {
		if dc.RootCause == "" || dc.Containment == "" || dc.CorrectiveAction == "" || dc.RetestRoundID == "" {
			return ErrInvalid
		}
		found := false
		for _, r := range retest {
			if r.RoundID == dc.RetestRoundID {
				found = true
			}
		}
		if !found || dc.Status != "CLOSED" {
			return ErrInvalid
		}
	}
	c.State = ReviewPending
	c.Revision++
	return nil
}
func (c *Campaign) Review(r Review) error {
	if c.State != ReviewPending {
		return ErrState
	}
	if r.ReviewerID == "" || r.ReviewerID == c.CreatedBy {
		return ErrInvalid
	}
	if r.Approved && strings.TrimSpace(r.Statement) == "" {
		return ErrInvalid
	}
	if !r.Approved && utf8.RuneCountInString(strings.TrimSpace(r.Reason)) < 5 {
		return ErrInvalid
	}
	if !r.Approved {
		c.State = RemediationRequired
		c.Revision++
		return nil
	}
	c.State = ReviewApproved
	c.Revision++
	return nil
}
func (c *Campaign) Archive() error {
	if c.State != ReviewApproved {
		return ErrState
	}
	c.State = Archived
	c.Revision++
	c.ArchivedAt = time.Now().UTC()
	return nil
}
func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func isHex(s string) bool {
	if len(s)%2 != 0 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
