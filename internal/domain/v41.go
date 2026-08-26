package domain

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"
)

const MaxAvailabilitySpan = 366 * 24 * time.Hour

type AvailabilitySlot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type ResourceAvailability struct {
	Available    bool               `json:"available"`
	Conflicts    []ResourceConflict `json:"conflicts"`
	PreviousSlot *AvailabilitySlot  `json:"previous_slot,omitempty"`
	NextSlot     *AvailabilitySlot  `json:"next_slot,omitempty"`
	EvaluatedAt  time.Time          `json:"evaluated_at"`
}

func EvaluateResourceAvailability(station string, start, end time.Time, devices []string, existing []*Campaign, now time.Time) (ResourceAvailability, error) {
	station = strings.TrimSpace(station)
	if station == "" || len(devices) == 0 || !start.Before(end) || end.Sub(start) > MaxAvailabilitySpan {
		return ResourceAvailability{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range devices {
		if strings.TrimSpace(id) == "" || seen[id] {
			return ResourceAvailability{}, ErrInvalid
		}
		seen[id] = true
	}
	candidate := &Campaign{StationCode: station, MissionWindowStart: start.UTC(), MissionWindowEnd: end.UTC(), DeviceIDs: append([]string(nil), devices...)}
	conflicts := WindowConflicts(candidate, existing)
	sort.Slice(conflicts, func(i, j int) bool {
		if !conflicts[i].OverlapStart.Equal(conflicts[j].OverlapStart) {
			return conflicts[i].OverlapStart.Before(conflicts[j].OverlapStart)
		}
		if conflicts[i].ResourceType != conflicts[j].ResourceType {
			return conflicts[i].ResourceType < conflicts[j].ResourceType
		}
		if conflicts[i].ResourceID != conflicts[j].ResourceID {
			return conflicts[i].ResourceID < conflicts[j].ResourceID
		}
		return conflicts[i].CampaignID < conflicts[j].CampaignID
	})
	out := ResourceAvailability{Available: len(conflicts) == 0, Conflicts: conflicts, EvaluatedAt: now.UTC()}
	if len(conflicts) == 0 {
		return out, nil
	}
	duration := end.Sub(start)
	busy := relevantBusy(candidate, existing)
	previousEnd := start.UTC()
	for i := len(busy) - 1; i >= 0; i-- {
		if !busy[i][1].After(previousEnd.Add(-duration)) || !busy[i][0].Before(previousEnd) {
			continue
		}
		previousEnd = busy[i][0]
		i = len(busy)
	}
	previous := AvailabilitySlot{Start: previousEnd.Add(-duration), End: previousEnd}
	out.PreviousSlot = &previous
	nextStart := start.UTC()
	for i := 0; i < len(busy); i++ {
		if !nextStart.Before(busy[i][1]) || !busy[i][0].Before(nextStart.Add(duration)) {
			continue
		}
		nextStart = busy[i][1]
		i = -1
	}
	next := AvailabilitySlot{Start: nextStart, End: nextStart.Add(duration)}
	out.NextSlot = &next
	return out, nil
}

func relevantBusy(candidate *Campaign, existing []*Campaign) [][2]time.Time {
	devices := map[string]bool{}
	for _, id := range candidate.DeviceIDs {
		devices[id] = true
	}
	spans := [][2]time.Time{}
	for _, c := range existing {
		if c.State == Cancelled {
			continue
		}
		relevant := c.StationCode == candidate.StationCode
		for _, id := range c.DeviceIDs {
			relevant = relevant || devices[id]
		}
		if relevant {
			spans = append(spans, [2]time.Time{c.MissionWindowStart.UTC(), c.MissionWindowEnd.UTC()})
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i][0].Equal(spans[j][0]) {
			return spans[i][1].Before(spans[j][1])
		}
		return spans[i][0].Before(spans[j][0])
	})
	merged := [][2]time.Time{}
	for _, span := range spans {
		if len(merged) == 0 || span[0].After(merged[len(merged)-1][1]) {
			merged = append(merged, span)
			continue
		}
		if span[1].After(merged[len(merged)-1][1]) {
			merged[len(merged)-1][1] = span[1]
		}
	}
	return merged
}

type CertificateFingerprintConflict struct {
	Fields []string `json:"conflict_fields"`
}

func (e *CertificateFingerprintConflict) Error() string { return "CERTIFICATE_FINGERPRINT_CONFLICT" }

func CertificateFingerprint(e ReferenceEvidence) string {
	return Hash([]byte(strings.ToLower(strings.TrimSpace(e.ReferenceKind)) + "|" + strings.ToLower(strings.TrimSpace(e.Provider)) + "|" + e.ValidFrom.UTC().Format(time.RFC3339Nano) + "|" + e.ValidUntil.UTC().Format(time.RFC3339Nano)))
}

func CompareCertificateFingerprint(candidate ReferenceEvidence, history []ReferenceEvidence) ([]string, error) {
	digest := strings.ToLower(strings.TrimSpace(candidate.CertificateDigest))
	sources := map[string]bool{}
	for _, item := range history {
		if item.CampaignID == candidate.CampaignID || !strings.EqualFold(strings.TrimSpace(item.CertificateDigest), digest) || item.Replaced {
			continue
		}
		fields := []string{}
		if item.ReferenceKind != candidate.ReferenceKind {
			fields = append(fields, "reference_kind")
		}
		if !strings.EqualFold(strings.TrimSpace(item.Provider), strings.TrimSpace(candidate.Provider)) {
			fields = append(fields, "provider")
		}
		if !item.ValidFrom.UTC().Equal(candidate.ValidFrom.UTC()) {
			fields = append(fields, "valid_from")
		}
		if !item.ValidUntil.UTC().Equal(candidate.ValidUntil.UTC()) {
			fields = append(fields, "valid_until")
		}
		if len(fields) > 0 {
			return nil, &CertificateFingerprintConflict{Fields: fields}
		}
		sources[item.CampaignID] = true
	}
	ids := make([]string, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

type DigestUsage struct {
	CampaignID  string    `json:"campaign_id"`
	EvidenceID  string    `json:"evidence_id"`
	Status      string    `json:"status"`
	SubmittedAt time.Time `json:"submitted_at"`
	Fingerprint string    `json:"fingerprint"`
}

type RoundConsistencyDiagnostic struct {
	RoundID           string  `json:"round_id"`
	Sequence          int     `json:"sequence"`
	SampleSkewSeconds float64 `json:"sample_skew_seconds"`
	TimeMedian        float64 `json:"time_deviation_median"`
	FrequencyMedian   float64 `json:"frequency_deviation_median"`
}
type DeviceResidual struct {
	RoundID           string    `json:"round_id"`
	Sequence          int       `json:"sequence"`
	DeviceID          string    `json:"device_id"`
	SampledAt         time.Time `json:"sampled_at"`
	TimeValue         float64   `json:"time_deviation"`
	FrequencyValue    float64   `json:"frequency_deviation"`
	TimeResidual      float64   `json:"time_residual"`
	FrequencyResidual float64   `json:"frequency_residual"`
}
type ConsistencyIssue struct {
	Code      string    `json:"code"`
	RoundID   string    `json:"round_id,omitempty"`
	Sequence  int       `json:"sequence,omitempty"`
	DeviceID  string    `json:"device_id,omitempty"`
	SampledAt time.Time `json:"sampled_at,omitempty"`
	Value     float64   `json:"value,omitempty"`
}
type MeasurementConsistency struct {
	Status           string                       `json:"status"`
	RoundDiagnostics []RoundConsistencyDiagnostic `json:"round_diagnostics"`
	DeviceResiduals  []DeviceResidual             `json:"device_residuals"`
	Issues           []ConsistencyIssue           `json:"issues"`
	AnalyzedRevision int64                        `json:"analyzed_revision"`
}

func BuildMeasurementConsistency(c *Campaign, rounds []MeasurementRound, deviceID, purpose string) (MeasurementConsistency, error) {
	if deviceID != "" {
		found := false
		for _, id := range c.DeviceIDs {
			found = found || id == deviceID
		}
		if !found {
			return MeasurementConsistency{Status: "UNKNOWN_DEVICE", RoundDiagnostics: []RoundConsistencyDiagnostic{}, DeviceResiduals: []DeviceResidual{}, Issues: []ConsistencyIssue{}, AnalyzedRevision: c.Revision}, nil
		}
	}
	if purpose != "" && purpose != "original" && purpose != "retest" && purpose != "remediation" {
		return MeasurementConsistency{}, ErrInvalid
	}
	out := MeasurementConsistency{Status: "DIAGNOSABLE", RoundDiagnostics: []RoundConsistencyDiagnostic{}, DeviceResiduals: []DeviceResidual{}, Issues: []ConsistencyIssue{}, AnalyzedRevision: c.Revision}
	var previousMedian, previousFrequencyMedian *float64
	selectedSampleCount := 0
	for _, round := range rounds {
		rp := round.Purpose
		if rp == "" {
			rp = "original"
		}
		if purpose != "" && purpose != rp {
			continue
		}
		samples := round.Samples
		present := map[string]bool{}
		for _, sample := range samples {
			present[sample.DeviceID] = true
		}
		for _, id := range c.DeviceIDs {
			if (deviceID == "" || deviceID == id) && !present[id] {
				out.Issues = append(out.Issues, ConsistencyIssue{Code: "DEVICE_MISSING", RoundID: round.RoundID, Sequence: round.Sequence, DeviceID: id})
			}
		}
		if len(samples) == 0 {
			continue
		}
		times, freqs := []float64{}, []float64{}
		lo, hi := samples[0].SampledAt, samples[0].SampledAt
		for _, sample := range samples {
			times = append(times, sample.TimeOffset)
			freqs = append(freqs, sample.FrequencyOffset)
			if sample.SampledAt.Before(lo) {
				lo = sample.SampledAt
			}
			if sample.SampledAt.After(hi) {
				hi = sample.SampledAt
			}
		}
		tm, fm := median(times), median(freqs)
		skew := roundValue(hi.Sub(lo).Seconds())
		out.RoundDiagnostics = append(out.RoundDiagnostics, RoundConsistencyDiagnostic{round.RoundID, round.Sequence, skew, roundValue(tm), roundValue(fm)})
		skewLimit := c.MeasurementPlan.MaxIntervalSeconds
		if skewLimit <= 0 {
			skewLimit = 60
		}
		if skew > skewLimit {
			out.Issues = append(out.Issues, ConsistencyIssue{Code: "SAMPLE_SKEW", RoundID: round.RoundID, Sequence: round.Sequence, Value: skew})
		}
		commonTime := previousMedian != nil && math.Abs(tm-*previousMedian) > c.Threshold.MaxAbsDeviation
		commonFrequency := previousFrequencyMedian != nil && math.Abs(fm-*previousFrequencyMedian) > c.Threshold.MaxFrequencyDeviation
		outlier := false
		for _, sample := range samples {
			tr, fr := roundValue(sample.TimeOffset-tm), roundValue(sample.FrequencyOffset-fm)
			selected := deviceID == "" || sample.DeviceID == deviceID
			if selected {
				selectedSampleCount++
				out.DeviceResiduals = append(out.DeviceResiduals, DeviceResidual{round.RoundID, round.Sequence, sample.DeviceID, sample.SampledAt.UTC(), roundValue(sample.TimeOffset), roundValue(sample.FrequencyOffset), tr, fr})
			}
			if math.Abs(tr) > c.Threshold.MaxAbsDeviation || math.Abs(fr) > c.Threshold.MaxFrequencyDeviation {
				outlier = true
				if selected {
					out.Issues = append(out.Issues, ConsistencyIssue{Code: "DEVICE_OUTLIER", RoundID: round.RoundID, Sequence: round.Sequence, DeviceID: sample.DeviceID, SampledAt: sample.SampledAt.UTC(), Value: tr})
				}
			}
		}
		if (commonTime || commonFrequency) && !outlier {
			value := fm
			if commonTime {
				value = tm - *previousMedian
			} else {
				value = fm - *previousFrequencyMedian
			}
			out.Issues = append(out.Issues, ConsistencyIssue{Code: "COMMON_MODE_SHIFT", RoundID: round.RoundID, Sequence: round.Sequence, Value: roundValue(value)})
		}
		timeValue, frequencyValue := tm, fm
		previousMedian, previousFrequencyMedian = &timeValue, &frequencyValue
	}
	if len(out.RoundDiagnostics) < 2 {
		out.Status = "INSUFFICIENT_ROUNDS"
	}
	if len(out.RoundDiagnostics) == 0 || selectedSampleCount == 0 {
		out.Status = "NO_EFFECTIVE_SAMPLES"
	}
	sort.Slice(out.Issues, func(i, j int) bool {
		if out.Issues[i].Sequence != out.Issues[j].Sequence {
			return out.Issues[i].Sequence < out.Issues[j].Sequence
		}
		if out.Issues[i].Code != out.Issues[j].Code {
			return out.Issues[i].Code < out.Issues[j].Code
		}
		return out.Issues[i].DeviceID < out.Issues[j].DeviceID
	})
	sort.Slice(out.DeviceResiduals, func(i, j int) bool {
		if out.DeviceResiduals[i].Sequence != out.DeviceResiduals[j].Sequence {
			return out.DeviceResiduals[i].Sequence < out.DeviceResiduals[j].Sequence
		}
		if out.DeviceResiduals[i].DeviceID != out.DeviceResiduals[j].DeviceID {
			return out.DeviceResiduals[i].DeviceID < out.DeviceResiduals[j].DeviceID
		}
		return out.DeviceResiduals[i].RoundID < out.DeviceResiduals[j].RoundID
	})
	return out, nil
}
func median(v []float64) float64 {
	x := append([]float64(nil), v...)
	sort.Float64s(x)
	n := len(x)
	if n%2 == 1 {
		return x[n/2]
	}
	return (x[n/2-1] + x[n/2]) / 2
}

type MarginMetric struct {
	Metric         string  `json:"metric"`
	ObservedValue  float64 `json:"observed_value"`
	LimitValue     float64 `json:"limit_value"`
	AbsoluteMargin float64 `json:"absolute_margin"`
	MarginRatio    float64 `json:"margin_ratio"`
	RiskLevel      string  `json:"risk_level"`
}
type DeviceMargin struct {
	DeviceID      string         `json:"device_id"`
	RiskLevel     string         `json:"risk_level"`
	ClosestMetric string         `json:"closest_metric"`
	Metrics       []MarginMetric `json:"metrics"`
}
type EvaluationMargins struct {
	EvaluationRevision int64          `json:"evaluation_revision"`
	AlgorithmVersion   string         `json:"algorithm_version"`
	InputSummary       string         `json:"input_summary"`
	RiskCounts         map[string]int `json:"risk_counts"`
	Devices            []DeviceMargin `json:"devices"`
	ClosestMetric      *MarginMetric  `json:"closest_metric,omitempty"`
}

func BuildEvaluationMargins(e Evaluation, deviceID, risk string) (EvaluationMargins, error) {
	validRisk := risk == "" || risk == "EXCEEDED" || risk == "CRITICAL" || risk == "WATCH" || risk == "COMFORTABLE"
	if !validRisk {
		return EvaluationMargins{}, ErrInvalid
	}
	if e.Threshold.MaxAbsDeviation <= 0 || e.Threshold.MaxFrequencyDeviation <= 0 || e.Threshold.MaxDriftSlope <= 0 {
		return EvaluationMargins{}, errors.New("INVALID_THRESHOLD_PROFILE")
	}
	by := map[string][]MarginMetric{}
	for _, m := range e.Metrics {
		limit := m.LimitValue
		if limit <= 0 {
			return EvaluationMargins{}, errors.New("INVALID_THRESHOLD_PROFILE")
		}
		margin := limit - math.Abs(m.ObservedValue)
		ratio := margin / limit
		level := "COMFORTABLE"
		if ratio < 0 {
			level = "EXCEEDED"
		} else if ratio <= .1 {
			level = "CRITICAL"
		} else if ratio <= .25 {
			level = "WATCH"
		}
		by[m.DeviceID] = append(by[m.DeviceID], MarginMetric{m.Metric, roundValue(m.ObservedValue), roundValue(limit), roundValue(margin), roundValue(ratio), level})
	}
	out := EvaluationMargins{e.Revision, e.AlgorithmVersion, e.InputSummary, map[string]int{"EXCEEDED": 0, "CRITICAL": 0, "WATCH": 0, "COMFORTABLE": 0}, []DeviceMargin{}, nil}
	priority := map[string]int{"EXCEEDED": 0, "CRITICAL": 1, "WATCH": 2, "COMFORTABLE": 3}
	for id, metrics := range by {
		sort.Slice(metrics, func(i, j int) bool {
			if priority[metrics[i].RiskLevel] != priority[metrics[j].RiskLevel] {
				return priority[metrics[i].RiskLevel] < priority[metrics[j].RiskLevel]
			}
			if metrics[i].MarginRatio != metrics[j].MarginRatio {
				return metrics[i].MarginRatio < metrics[j].MarginRatio
			}
			return metrics[i].Metric < metrics[j].Metric
		})
		level := metrics[0].RiskLevel
		out.RiskCounts[level]++
		if (deviceID == "" || id == deviceID) && (risk == "" || risk == level) {
			out.Devices = append(out.Devices, DeviceMargin{id, level, metrics[0].Metric, metrics})
		}
	}
	sort.Slice(out.Devices, func(i, j int) bool {
		pi, pj := priority[out.Devices[i].RiskLevel], priority[out.Devices[j].RiskLevel]
		if pi != pj {
			return pi < pj
		}
		ri, rj := out.Devices[i].Metrics[0].MarginRatio, out.Devices[j].Metrics[0].MarginRatio
		if ri != rj {
			return ri < rj
		}
		return out.Devices[i].DeviceID < out.Devices[j].DeviceID
	})
	if len(out.Devices) > 0 {
		m := out.Devices[0].Metrics[0]
		out.ClosestMetric = &m
	}
	return out, nil
}

type RemediationDependency struct {
	DeviationID             string    `json:"deviation_id"`
	PrerequisiteDeviationID string    `json:"prerequisite_deviation_id"`
	Reason                  string    `json:"reason"`
	RegisteredBy            string    `json:"registered_by"`
	Version                 int       `json:"version"`
	RegisteredAt            time.Time `json:"registered_at"`
	Revision                int64     `json:"revision"`
}
type DependencyNode struct {
	DeviationID          string   `json:"deviation_id"`
	Status               string   `json:"status"`
	TopologyLevel        int      `json:"topology_level"`
	BlockingDeviationIDs []string `json:"blocking_deviation_ids"`
}
type DependencyUnlock struct {
	DeviationID string   `json:"deviation_id"`
	UnlockedBy  []string `json:"unlocked_by"`
}
type DependencyProjection struct {
	Version       int                     `json:"version"`
	Nodes         []DependencyNode        `json:"nodes"`
	Edges         []RemediationDependency `json:"edges"`
	UnlockHistory []DependencyUnlock      `json:"unlock_history"`
	Revision      int64                   `json:"revision"`
}

var ErrDependencyCycle = errors.New("DEPENDENCY_CYCLE")
var ErrDeviationScope = errors.New("DEVIATION_SCOPE_MISMATCH")

func BuildDependencyProjection(deviations []DeviationCase, edges []RemediationDependency) (DependencyProjection, error) {
	by := map[string]DeviationCase{}
	for _, d := range deviations {
		by[d.DeviationID] = d
	}
	deps := map[string][]string{}
	seen := map[string]bool{}
	version := 0
	revision := int64(0)
	for _, e := range edges {
		if _, ok := by[e.DeviationID]; !ok {
			return DependencyProjection{}, ErrDeviationScope
		}
		if _, ok := by[e.PrerequisiteDeviationID]; !ok {
			return DependencyProjection{}, ErrDeviationScope
		}
		if e.DeviationID == e.PrerequisiteDeviationID {
			return DependencyProjection{}, ErrDependencyCycle
		}
		key := e.DeviationID + "|" + e.PrerequisiteDeviationID
		if seen[key] {
			return DependencyProjection{}, ErrInvalid
		}
		seen[key] = true
		deps[e.DeviationID] = append(deps[e.DeviationID], e.PrerequisiteDeviationID)
		if e.Version > version {
			version = e.Version
		}
		if e.Revision > revision {
			revision = e.Revision
		}
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return ErrDependencyCycle
		}
		if done[id] {
			return nil
		}
		visiting[id] = true
		for _, p := range deps[id] {
			if err := visit(p); err != nil {
				return err
			}
		}
		visiting[id] = false
		done[id] = true
		return nil
	}
	for id := range by {
		if err := visit(id); err != nil {
			return DependencyProjection{}, err
		}
	}
	levelMemo := map[string]int{}
	var level func(string) int
	level = func(id string) int {
		if v, ok := levelMemo[id]; ok {
			return v
		}
		n := 0
		for _, p := range deps[id] {
			if x := level(p) + 1; x > n {
				n = x
			}
		}
		levelMemo[id] = n
		return n
	}
	out := DependencyProjection{Version: version, Nodes: []DependencyNode{}, Edges: append([]RemediationDependency(nil), edges...), UnlockHistory: []DependencyUnlock{}, Revision: revision}
	for id, d := range by {
		blocking := []string{}
		for _, p := range deps[id] {
			if by[p].Status != "CLOSED" {
				blocking = append(blocking, p)
			}
		}
		sort.Strings(blocking)
		status := "ACTIONABLE"
		if d.Status == "CLOSED" {
			status = "COMPLETED"
		} else if len(blocking) > 0 {
			status = "BLOCKED"
		}
		out.Nodes = append(out.Nodes, DependencyNode{id, status, level(id), blocking})
		unlocked := []string{}
		for _, p := range deps[id] {
			if by[p].Status == "CLOSED" {
				unlocked = append(unlocked, p)
			}
		}
		if len(unlocked) > 0 {
			sort.Strings(unlocked)
			out.UnlockHistory = append(out.UnlockHistory, DependencyUnlock{id, unlocked})
		}
	}
	sort.Slice(out.Nodes, func(i, j int) bool {
		if out.Nodes[i].TopologyLevel != out.Nodes[j].TopologyLevel {
			return out.Nodes[i].TopologyLevel < out.Nodes[j].TopologyLevel
		}
		return out.Nodes[i].DeviationID < out.Nodes[j].DeviationID
	})
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].Version != out.Edges[j].Version {
			return out.Edges[i].Version < out.Edges[j].Version
		}
		if out.Edges[i].DeviationID != out.Edges[j].DeviationID {
			return out.Edges[i].DeviationID < out.Edges[j].DeviationID
		}
		return out.Edges[i].PrerequisiteDeviationID < out.Edges[j].PrerequisiteDeviationID
	})
	return out, nil
}

type LineageIssue struct {
	Code              string `json:"code"`
	CampaignID        string `json:"campaign_id,omitempty"`
	RelatedCampaignID string `json:"related_campaign_id,omitempty"`
	Layer             string `json:"layer,omitempty"`
}
type LineageNode struct {
	CampaignID    string           `json:"campaign_id"`
	State         State            `json:"state"`
	Revision      int64            `json:"revision"`
	StationCode   string           `json:"station_code"`
	WindowStart   time.Time        `json:"window_start"`
	WindowEnd     time.Time        `json:"window_end"`
	DeviceIDs     []string         `json:"device_ids"`
	Threshold     ThresholdProfile `json:"threshold"`
	ArtifactValid *bool            `json:"artifact_valid,omitempty"`
}
type LineageEdge struct {
	PredecessorCampaignID string `json:"predecessor_campaign_id"`
	SuccessorCampaignID   string `json:"successor_campaign_id"`
}
type LineageChange struct {
	CampaignID                string   `json:"campaign_id"`
	AddedDevices              []string `json:"added_devices"`
	RemovedDevices            []string `json:"removed_devices"`
	ThresholdChanged          bool     `json:"threshold_changed"`
	QualificationChecksStatus string   `json:"qualification_checks_status"`
}
type QualificationLineage struct {
	Valid         bool            `json:"valid"`
	LineageDigest string          `json:"lineage_digest"`
	Nodes         []LineageNode   `json:"nodes"`
	Edges         []LineageEdge   `json:"edges"`
	Changes       []LineageChange `json:"changes"`
	Issues        []LineageIssue  `json:"issues"`
}
