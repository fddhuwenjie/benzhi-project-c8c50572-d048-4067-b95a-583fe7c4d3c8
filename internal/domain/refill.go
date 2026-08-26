package domain

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type MeasurementPlan struct {
	RequiredRounds      int      `json:"required_rounds"`
	MinSpanSeconds      float64  `json:"min_span_seconds"`
	MaxIntervalSeconds  float64  `json:"max_interval_seconds"`
	MinTemperature      float64  `json:"min_temperature"`
	MaxTemperature      float64  `json:"max_temperature"`
	MinHumidity         float64  `json:"min_humidity"`
	MaxHumidity         float64  `json:"max_humidity"`
	AllowedSignalStatus []string `json:"allowed_signal_status"`
}

func DefaultMeasurementPlan() MeasurementPlan {
	return MeasurementPlan{RequiredRounds: 2, MinSpanSeconds: 0.000000001, MaxIntervalSeconds: 0, MinTemperature: -100, MaxTemperature: 100, MinHumidity: 0, MaxHumidity: 100, AllowedSignalStatus: []string{"0", "1", "OK"}}
}

func NormalizeMeasurementPlan(p MeasurementPlan) (MeasurementPlan, error) {
	if MeasurementPlanIsZero(p) {
		return DefaultMeasurementPlan(), nil
	}
	d := DefaultMeasurementPlan()
	if p.RequiredRounds == 0 {
		p.RequiredRounds = d.RequiredRounds
	}
	if p.MinSpanSeconds == 0 {
		p.MinSpanSeconds = d.MinSpanSeconds
	}
	if p.MinTemperature == 0 && p.MaxTemperature == 0 {
		p.MinTemperature, p.MaxTemperature = d.MinTemperature, d.MaxTemperature
	}
	if p.MinHumidity == 0 && p.MaxHumidity == 0 {
		p.MinHumidity, p.MaxHumidity = d.MinHumidity, d.MaxHumidity
	}
	if len(p.AllowedSignalStatus) == 0 {
		p.AllowedSignalStatus = append([]string(nil), d.AllowedSignalStatus...)
	}
	if p.RequiredRounds < 2 || p.MinSpanSeconds < 0 || p.MaxIntervalSeconds < 0 || !finite(p.MinSpanSeconds) || !finite(p.MaxIntervalSeconds) ||
		!finite(p.MinTemperature) || !finite(p.MaxTemperature) || !finite(p.MinHumidity) || !finite(p.MaxHumidity) ||
		p.MinTemperature > p.MaxTemperature || p.MinHumidity < 0 || p.MinHumidity > p.MaxHumidity || p.MaxHumidity > 100 || len(p.AllowedSignalStatus) == 0 {
		return MeasurementPlan{}, ErrInvalid
	}
	seen := map[string]bool{}
	for i, status := range p.AllowedSignalStatus {
		status = strings.TrimSpace(status)
		if status == "" || seen[status] {
			return MeasurementPlan{}, ErrInvalid
		}
		seen[status] = true
		p.AllowedSignalStatus[i] = status
	}
	sort.Strings(p.AllowedSignalStatus)
	return p, nil
}

func MeasurementPlanIsZero(p MeasurementPlan) bool {
	return p.RequiredRounds == 0 && p.MinSpanSeconds == 0 && p.MaxIntervalSeconds == 0 && p.MinTemperature == 0 && p.MaxTemperature == 0 && p.MinHumidity == 0 && p.MaxHumidity == 0 && len(p.AllowedSignalStatus) == 0
}

type MeasurementCompletion struct {
	Plan              MeasurementPlan    `json:"plan"`
	Complete          bool               `json:"complete"`
	DeviceRoundCounts map[string]int     `json:"device_round_counts"`
	DeviceSpanSeconds map[string]float64 `json:"device_span_seconds"`
}

type ValidationError struct {
	Issues []QualityIssue `json:"issues"`
}

func (e *ValidationError) Error() string { return "measurement plan violation" }

func MeasurementPlanCompliance(c *Campaign, rounds []MeasurementRound) (MeasurementCompletion, []QualityIssue) {
	p := c.MeasurementPlan
	if MeasurementPlanIsZero(p) {
		p = DefaultMeasurementPlan()
		p.RequiredRounds = 1
		p.MinSpanSeconds = 0
	}
	out := MeasurementCompletion{Plan: p, Complete: true, DeviceRoundCounts: map[string]int{}, DeviceSpanSeconds: map[string]float64{}}
	issues := []QualityIssue{}
	type samplePoint struct {
		at      time.Time
		roundID string
	}
	times := map[string][]samplePoint{}
	allowed := map[string]bool{}
	for _, x := range p.AllowedSignalStatus {
		allowed[x] = true
	}
	for _, r := range rounds {
		temp, te := strconv.ParseFloat(strings.TrimSpace(r.Environment["temperature"]), 64)
		humidity, he := strconv.ParseFloat(strings.TrimSpace(r.Environment["humidity"]), 64)
		signal := strings.TrimSpace(r.Environment["signal_status"])
		for _, id := range c.DeviceIDs {
			found := false
			for _, sample := range r.Samples {
				if sample.DeviceID == id {
					found = true
					times[id] = append(times[id], samplePoint{sample.SampledAt.UTC(), r.RoundID})
					break
				}
			}
			if found {
				out.DeviceRoundCounts[id]++
			}
			if c.MeasurementPlanLocked && (te != nil || math.IsNaN(temp) || math.IsInf(temp, 0) || temp < p.MinTemperature || temp > p.MaxTemperature) {
				issues = append(issues, QualityIssue{r.RoundID, id, "environment.temperature", "ERROR", "TEMPERATURE_OUT_OF_PLAN", "温度不符合锁定方案"})
			}
			if c.MeasurementPlanLocked && (he != nil || math.IsNaN(humidity) || math.IsInf(humidity, 0) || humidity < p.MinHumidity || humidity > p.MaxHumidity) {
				issues = append(issues, QualityIssue{r.RoundID, id, "environment.humidity", "ERROR", "HUMIDITY_OUT_OF_PLAN", "湿度不符合锁定方案"})
			}
			if c.MeasurementPlanLocked && !allowed[signal] {
				issues = append(issues, QualityIssue{r.RoundID, id, "environment.signal_status", "ERROR", "SIGNAL_STATUS_NOT_ALLOWED", "信号状态不符合锁定方案"})
			}
		}
	}
	for _, id := range c.DeviceIDs {
		sort.Slice(times[id], func(i, j int) bool { return times[id][i].at.Before(times[id][j].at) })
		if len(times[id]) > 1 {
			out.DeviceSpanSeconds[id] = roundValue(times[id][len(times[id])-1].at.Sub(times[id][0].at).Seconds())
			if p.MaxIntervalSeconds > 0 {
				for i := 1; i < len(times[id]); i++ {
					if times[id][i].at.Sub(times[id][i-1].at).Seconds() > p.MaxIntervalSeconds {
						issues = append(issues, QualityIssue{times[id][i].roundID, id, "sampled_at", "ERROR", "MAX_INTERVAL_EXCEEDED", "相邻采样间隔超过锁定方案"})
					}
				}
			}
		}
		if out.DeviceRoundCounts[id] < p.RequiredRounds || out.DeviceSpanSeconds[id] < p.MinSpanSeconds {
			out.Complete = false
		}
		if out.DeviceRoundCounts[id] >= p.RequiredRounds && out.DeviceSpanSeconds[id] < p.MinSpanSeconds {
			roundID := ""
			if len(times[id]) > 0 {
				roundID = times[id][len(times[id])-1].roundID
			}
			issues = append(issues, QualityIssue{roundID, id, "sampled_at", "ERROR", "MIN_SPAN_NOT_MET", "采样跨度未达到锁定方案"})
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].RoundID != issues[j].RoundID {
			return issues[i].RoundID < issues[j].RoundID
		}
		if issues[i].DeviceID != issues[j].DeviceID {
			return issues[i].DeviceID < issues[j].DeviceID
		}
		return issues[i].Code < issues[j].Code
	})
	return out, issues
}

type Cancellation struct {
	ReasonCode  string    `json:"reason_code"`
	Reason      string    `json:"reason"`
	CancelledBy string    `json:"cancelled_by"`
	CancelledAt time.Time `json:"cancelled_at"`
	RequestID   string    `json:"request_id"`
}

func ValidateCancellation(c *Campaign, roundCount int, reasonCode, reason, by, requestID string) error {
	if (c.State != Draft && c.State != ReferenceVerified) || roundCount != 0 {
		return ErrState
	}
	if !reasonCodePatternDomain(reasonCode) || hanCount(reason) < 5 || strings.TrimSpace(by) == "" || strings.TrimSpace(requestID) == "" {
		return ErrInvalid
	}
	return nil
}

func hanCount(s string) int {
	n := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return n
}

func reasonCodePatternDomain(s string) bool {
	if len(s) < 3 || len(s) > 64 || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

type ReferenceWithdrawal struct {
	CampaignID  string    `json:"campaign_id"`
	EvidenceID  string    `json:"evidence_id"`
	ReasonCode  string    `json:"reason_code"`
	Reason      string    `json:"reason"`
	WithdrawnBy string    `json:"withdrawn_by"`
	WithdrawnAt time.Time `json:"withdrawn_at"`
	Revision    int64     `json:"revision"`
}

func EffectiveReferences(refs []ReferenceEvidence, withdrawals []ReferenceWithdrawal) []ReferenceEvidence {
	withdrawn := map[string]bool{}
	for _, w := range withdrawals {
		withdrawn[w.EvidenceID] = true
	}
	out := []ReferenceEvidence{}
	for _, r := range refs {
		if !r.Replaced && !withdrawn[r.EvidenceID] {
			out = append(out, r)
		}
	}
	return out
}

type ReviewFinding struct {
	FindingID      string    `json:"finding_id"`
	CampaignID     string    `json:"campaign_id"`
	ReviewRevision int64     `json:"review_revision"`
	CheckCode      string    `json:"check_code"`
	Description    string    `json:"description"`
	DeviceID       string    `json:"device_id,omitempty"`
	Severity       string    `json:"severity"`
	RequiresRetest bool      `json:"requires_retest"`
	CreatedAt      time.Time `json:"created_at"`
}
type FindingResolution struct {
	FindingID       string    `json:"finding_id"`
	Resolution      string    `json:"resolution"`
	ResolvedBy      string    `json:"resolved_by"`
	EvidenceSummary string    `json:"evidence_summary"`
	RetestRoundID   string    `json:"retest_round_id,omitempty"`
	ResolvedAt      time.Time `json:"resolved_at"`
	Revision        int64     `json:"revision"`
}

var ErrIntegrity = errors.New("integrity verification failed")

type QualificationDeviceResult struct {
	DeviceID   string `json:"device_id"`
	Conclusion string `json:"conclusion"`
}
type QualificationCheck struct {
	OverallQualified bool                        `json:"overall_qualified"`
	WindowConclusion string                      `json:"window_conclusion"`
	Devices          []QualificationDeviceResult `json:"devices"`
	ReviewerID       string                      `json:"reviewer_id"`
	EvidenceDigest   string                      `json:"evidence_digest"`
	AuditHeadDigest  string                      `json:"audit_head_digest"`
}

func BuildQualificationCheck(c Campaign, station string, start, end time.Time, deviceIDs []string, failed map[string]bool) (QualificationCheck, error) {
	if station == "" || station != c.StationCode || !start.Before(end) || len(deviceIDs) == 0 {
		return QualificationCheck{}, ErrInvalid
	}
	seen := map[string]bool{}
	result := QualificationCheck{OverallQualified: true, WindowConclusion: "COVERED", Devices: []QualificationDeviceResult{}}
	if start.Before(c.MissionWindowStart) && end.After(c.MissionWindowEnd) || start.Before(c.MissionWindowStart) || end.After(c.MissionWindowEnd) {
		result.WindowConclusion = "PARTIAL_WINDOW"
		result.OverallQualified = false
	}
	if end.Before(c.MissionWindowStart) || end.Equal(c.MissionWindowStart) || start.After(c.MissionWindowEnd) || start.Equal(c.MissionWindowEnd) {
		result.WindowConclusion = "OUTSIDE_WINDOW"
		result.OverallQualified = false
	}
	for _, id := range deviceIDs {
		if id == "" || seen[id] {
			return QualificationCheck{}, ErrInvalid
		}
		seen[id] = true
		conclusion := "QUALIFIED"
		if !contains(c.DeviceIDs, id) {
			conclusion = "DEVICE_NOT_COVERED"
			result.OverallQualified = false
		} else if failed[id] {
			conclusion = "DEVICE_FAILED"
			result.OverallQualified = false
		}
		result.Devices = append(result.Devices, QualificationDeviceResult{id, conclusion})
	}
	sort.Slice(result.Devices, func(i, j int) bool { return result.Devices[i].DeviceID < result.Devices[j].DeviceID })
	return result, nil
}
