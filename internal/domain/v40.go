package domain

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

type ReferencePreflightIssue struct {
	CandidateIndex int    `json:"candidate_index"`
	EvidenceID     string `json:"evidence_id,omitempty"`
	Field          string `json:"field"`
	Code           string `json:"code"`
	Message        string `json:"message"`
}
type CoverageInterval struct {
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	EvidenceIDs []string  `json:"evidence_ids"`
}
type ReferenceKindPreflight struct {
	ReferenceKind string             `json:"reference_kind"`
	EvidenceIDs   []string           `json:"evidence_ids"`
	Coverage      []CoverageInterval `json:"coverage"`
	Gaps          []CoverageGap      `json:"gaps"`
	Complete      bool               `json:"complete"`
}
type ReferencePreflightResult struct {
	Kinds     []ReferenceKindPreflight  `json:"kinds"`
	Issues    []ReferencePreflightIssue `json:"issues"`
	CanVerify bool                      `json:"can_verify"`
}

func ReferencePreflight(c *Campaign, existing, candidates []ReferenceEvidence) ReferencePreflightResult {
	result := ReferencePreflightResult{Kinds: []ReferenceKindPreflight{}, Issues: []ReferencePreflightIssue{}}
	active := make([]ReferenceEvidence, 0, len(existing)+len(candidates))
	seenID, seenDigest, seenSource := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range existing {
		if e.Replaced {
			continue
		}
		active = append(active, e)
		seenID[e.EvidenceID] = true
		seenDigest[strings.ToLower(e.CertificateDigest)] = true
		seenSource[e.ReferenceKind+"|"+strings.ToLower(strings.TrimSpace(e.Provider))+"|"+e.ValidFrom.UTC().Format(time.RFC3339Nano)+"|"+e.ValidUntil.UTC().Format(time.RFC3339Nano)] = true
	}
	for i, e := range candidates {
		valid := true
		add := func(field, code, message string) {
			result.Issues = append(result.Issues, ReferencePreflightIssue{i, e.EvidenceID, field, code, message})
			valid = false
		}
		if strings.TrimSpace(e.EvidenceID) == "" {
			add("evidence_id", "EVIDENCE_ID_REQUIRED", "证据编号不能为空")
		}
		if e.ReferenceKind != "clock" && e.ReferenceKind != "frequency" {
			add("reference_kind", "REFERENCE_KIND_INVALID", "参考类型必须为 clock 或 frequency")
		}
		if strings.TrimSpace(e.Provider) == "" {
			add("provider", "PROVIDER_REQUIRED", "来源不能为空")
		}
		if len(e.CertificateDigest) == 0 || len(e.CertificateDigest)%2 != 0 || !isHex(e.CertificateDigest) {
			add("certificate_digest", "CERTIFICATE_DIGEST_INVALID", "证书摘要必须为十六进制")
		}
		if !e.ValidFrom.Before(e.ValidUntil) {
			add("valid_until", "VALIDITY_WINDOW_INVALID", "证据有效期无效")
		}
		if seenID[e.EvidenceID] {
			add("evidence_id", "DUPLICATE_EVIDENCE_ID", "证据编号重复")
		}
		digest := strings.ToLower(e.CertificateDigest)
		if seenDigest[digest] {
			add("certificate_digest", "DUPLICATE_CERTIFICATE_DIGEST", "证书摘要重复")
		}
		source := e.ReferenceKind + "|" + strings.ToLower(strings.TrimSpace(e.Provider)) + "|" + e.ValidFrom.UTC().Format(time.RFC3339Nano) + "|" + e.ValidUntil.UTC().Format(time.RFC3339Nano)
		if seenSource[source] {
			add("provider", "DUPLICATE_REFERENCE_SOURCE", "参考类型与来源组合重复")
		}
		seenID[e.EvidenceID], seenDigest[digest], seenSource[source] = true, true, true
		if valid {
			active = append(active, e)
		}
	}
	for _, kind := range []string{"clock", "frequency"} {
		spans := make([]CoverageInterval, 0)
		ids := make([]string, 0)
		for _, e := range active {
			if e.ReferenceKind != kind || !e.ValidUntil.After(c.MissionWindowStart) || !e.ValidFrom.Before(c.MissionWindowEnd) {
				continue
			}
			start, end := e.ValidFrom.UTC(), e.ValidUntil.UTC()
			if start.Before(c.MissionWindowStart) {
				start = c.MissionWindowStart
			}
			if end.After(c.MissionWindowEnd) {
				end = c.MissionWindowEnd
			}
			spans = append(spans, CoverageInterval{Start: start, End: end, EvidenceIDs: []string{e.EvidenceID}})
			ids = append(ids, e.EvidenceID)
		}
		sort.Slice(spans, func(i, j int) bool {
			if spans[i].Start.Equal(spans[j].Start) {
				return spans[i].EvidenceIDs[0] < spans[j].EvidenceIDs[0]
			}
			return spans[i].Start.Before(spans[j].Start)
		})
		merged := make([]CoverageInterval, 0)
		for _, span := range spans {
			if len(merged) == 0 || span.Start.After(merged[len(merged)-1].End) {
				merged = append(merged, span)
				continue
			}
			last := &merged[len(merged)-1]
			last.EvidenceIDs = append(last.EvidenceIDs, span.EvidenceIDs...)
			if span.End.After(last.End) {
				last.End = span.End
			}
		}
		for i := range merged {
			sort.Strings(merged[i].EvidenceIDs)
		}
		sort.Strings(ids)
		gaps := c.coverageGaps(active, kind)
		result.Kinds = append(result.Kinds, ReferenceKindPreflight{kind, ids, merged, gaps, len(gaps) == 0})
	}
	result.CanVerify = len(result.Issues) == 0 && result.Kinds[0].Complete && result.Kinds[1].Complete
	return result
}

type RoundVoid struct {
	CampaignID         string    `json:"campaign_id"`
	RoundID            string    `json:"round_id"`
	ReasonCode         string    `json:"reason_code"`
	Reason             string    `json:"reason"`
	VoidedBy           string    `json:"voided_by"`
	VoidedAt           time.Time `json:"voided_at"`
	Revision           int64     `json:"revision"`
	ReplacementRoundID string    `json:"replacement_round_id,omitempty"`
}
type MeasurementCoverage struct {
	Complete                 bool     `json:"complete"`
	MissingDevices           []string `json:"missing_devices"`
	InsufficientSpanDevices  []string `json:"insufficient_span_devices"`
	MissingRounds            []int    `json:"missing_rounds"`
	RequiredAdditionalRounds int      `json:"required_additional_rounds"`
}

func EffectiveRounds(rounds []MeasurementRound, voids []RoundVoid) []MeasurementRound {
	voided := map[string]bool{}
	for _, v := range voids {
		voided[v.RoundID] = true
	}
	out := make([]MeasurementRound, 0, len(rounds))
	for _, r := range rounds {
		if !voided[r.RoundID] {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sequence == out[j].Sequence {
			return out[i].RoundID < out[j].RoundID
		}
		return out[i].Sequence < out[j].Sequence
	})
	return out
}
func MeasurementCoverageFor(c *Campaign, rounds []MeasurementRound) MeasurementCoverage {
	seen := map[string]bool{}
	missingRounds := []int{}
	for _, r := range rounds {
		per := map[string]bool{}
		for _, s := range r.Samples {
			seen[s.DeviceID] = true
			per[s.DeviceID] = true
		}
		if len(per) < len(c.DeviceIDs) {
			missingRounds = append(missingRounds, r.Sequence)
		}
	}
	missing := []string{}
	for _, id := range c.DeviceIDs {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	sort.Ints(missingRounds)
	return MeasurementCoverage{Complete: len(missing) == 0, MissingDevices: missing, MissingRounds: missingRounds}
}

func MeasurementReadinessFor(c *Campaign, rounds []MeasurementRound) MeasurementCoverage {
	out := MeasurementCoverageFor(c, rounds)
	counts := map[string]int{}
	first, last := map[string]time.Time{}, map[string]time.Time{}
	for _, r := range rounds {
		for _, s := range r.Samples {
			counts[s.DeviceID]++
			if first[s.DeviceID].IsZero() || s.SampledAt.Before(first[s.DeviceID]) {
				first[s.DeviceID] = s.SampledAt
			}
			if last[s.DeviceID].IsZero() || s.SampledAt.After(last[s.DeviceID]) {
				last[s.DeviceID] = s.SampledAt
			}
		}
	}
	for _, id := range c.DeviceIDs {
		need := 2 - counts[id]
		if need > out.RequiredAdditionalRounds {
			out.RequiredAdditionalRounds = need
		}
		if counts[id] < 2 || !last[id].After(first[id]) {
			out.InsufficientSpanDevices = append(out.InsufficientSpanDevices, id)
		}
	}
	sort.Strings(out.InsufficientSpanDevices)
	out.Complete = out.Complete && len(out.InsufficientSpanDevices) == 0
	return out
}

type DeviceMeasurementStats struct {
	DeviceID           string     `json:"device_id"`
	SampleCount        int        `json:"sample_count"`
	FirstSampledAt     *time.Time `json:"first_sampled_at,omitempty"`
	LastSampledAt      *time.Time `json:"last_sampled_at,omitempty"`
	SpanSeconds        float64    `json:"span_seconds"`
	MinAbsTimeOffset   *float64   `json:"min_abs_time_offset,omitempty"`
	MaxAbsTimeOffset   *float64   `json:"max_abs_time_offset,omitempty"`
	MinFrequencyOffset *float64   `json:"min_frequency_offset,omitempty"`
	MaxFrequencyOffset *float64   `json:"max_frequency_offset,omitempty"`
	RoundIDs           []string   `json:"round_ids"`
	Ready              bool       `json:"ready"`
	NotReadyReason     string     `json:"not_ready_reason,omitempty"`
}
type MeasurementSummaryIssue struct {
	RoundID  string `json:"round_id"`
	Sequence int    `json:"sequence"`
	DeviceID string `json:"device_id,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}
type MeasurementSummary struct {
	CampaignID string                    `json:"campaign_id"`
	Revision   int64                     `json:"revision"`
	Devices    []DeviceMeasurementStats  `json:"devices"`
	Issues     []MeasurementSummaryIssue `json:"issues"`
	RoundIDs   []string                  `json:"round_ids"`
}

func roundValue(v float64) float64 { return math.Round(v*1e9) / 1e9 }
func BuildMeasurementSummary(c *Campaign, rounds []MeasurementRound, deviceID, purpose string) MeasurementSummary {
	out := MeasurementSummary{CampaignID: c.CampaignID, Revision: c.Revision, Devices: []DeviceMeasurementStats{}, Issues: []MeasurementSummaryIssue{}, RoundIDs: []string{}}
	selected := []MeasurementRound{}
	for _, r := range rounds {
		rp := r.Purpose
		if rp == "" {
			rp = "original"
		}
		if purpose == "" || rp == purpose {
			selected = append(selected, r)
			out.RoundIDs = append(out.RoundIDs, r.RoundID)
			for _, field := range []string{"temperature", "humidity", "signal_status"} {
				if strings.TrimSpace(r.Environment[field]) == "" {
					out.Issues = append(out.Issues, MeasurementSummaryIssue{r.RoundID, r.Sequence, "", "ENVIRONMENT_MISSING", "轮次环境字段缺失: " + field})
				}
			}
		}
	}
	ids := append([]string(nil), c.DeviceIDs...)
	if deviceID != "" {
		ids = []string{deviceID}
	}
	sort.Strings(ids)
	var typical time.Duration
	for _, id := range ids {
		st := DeviceMeasurementStats{DeviceID: id, RoundIDs: []string{}}
		var samples []Sample
		var previousAcross time.Time
		for _, r := range selected {
			found := false
			for _, s := range r.Samples {
				if s.DeviceID != id {
					continue
				}
				found = true
				samples = append(samples, s)
				st.RoundIDs = append(st.RoundIDs, r.RoundID)
				if !previousAcross.IsZero() {
					if s.SampledAt.Before(previousAcross) {
						out.Issues = append(out.Issues, MeasurementSummaryIssue{r.RoundID, r.Sequence, id, "SAMPLE_TIME_REVERSED", "同设备采样时刻倒序"})
					} else if s.SampledAt.Equal(previousAcross) {
						out.Issues = append(out.Issues, MeasurementSummaryIssue{r.RoundID, r.Sequence, id, "SAMPLE_INTERVAL_ZERO", "相邻采样间隔为零"})
					}
				}
				previousAcross = s.SampledAt
			}
			if !found {
				out.Issues = append(out.Issues, MeasurementSummaryIssue{r.RoundID, r.Sequence, id, "DEVICE_SAMPLE_MISSING", "轮次缺少设备样本"})
			}
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i].SampledAt.Before(samples[j].SampledAt) })
		st.SampleCount = len(samples)
		if len(samples) == 0 {
			st.NotReadyReason = "NO_EFFECTIVE_SAMPLES"
			out.Devices = append(out.Devices, st)
			continue
		}
		first, last := samples[0].SampledAt.UTC(), samples[len(samples)-1].SampledAt.UTC()
		st.FirstSampledAt = &first
		st.LastSampledAt = &last
		st.SpanSeconds = roundValue(last.Sub(first).Seconds())
		minT, maxT := math.Abs(samples[0].TimeOffset), math.Abs(samples[0].TimeOffset)
		minF, maxF := samples[0].FrequencyOffset, samples[0].FrequencyOffset
		for i, s := range samples {
			a := math.Abs(s.TimeOffset)
			if a < minT {
				minT = a
			}
			if a > maxT {
				maxT = a
			}
			if s.FrequencyOffset < minF {
				minF = s.FrequencyOffset
			}
			if s.FrequencyOffset > maxF {
				maxF = s.FrequencyOffset
			}
			if i > 0 {
				gap := s.SampledAt.Sub(samples[i-1].SampledAt)
				if gap > 0 && (typical == 0 || gap < typical) {
					typical = gap
				}
			}
		}
		minT, maxT, minF, maxF = roundValue(minT), roundValue(maxT), roundValue(minF), roundValue(maxF)
		st.MinAbsTimeOffset = &minT
		st.MaxAbsTimeOffset = &maxT
		st.MinFrequencyOffset = &minF
		st.MaxFrequencyOffset = &maxF
		st.Ready = len(samples) >= 2 && last.After(first)
		if !st.Ready {
			st.NotReadyReason = "INSUFFICIENT_TIME_SPAN"
		}
		out.Devices = append(out.Devices, st)
	}
	if typical > 0 {
		for _, id := range ids {
			var prev *Sample
			for _, r := range selected {
				for _, s := range r.Samples {
					if s.DeviceID != id {
						continue
					}
					if prev != nil && s.SampledAt.Sub(prev.SampledAt) > typical*3 {
						out.Issues = append(out.Issues, MeasurementSummaryIssue{r.RoundID, r.Sequence, id, "SAMPLE_INTERVAL_OUTLIER", "相邻采样间隔异常放大"})
					}
					x := s
					prev = &x
				}
			}
		}
	}
	sort.SliceStable(out.Issues, func(i, j int) bool {
		a, b := out.Issues[i], out.Issues[j]
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		if a.DeviceID != b.DeviceID {
			return a.DeviceID < b.DeviceID
		}
		return a.Code < b.Code
	})
	return out
}

type ReviewClaim struct {
	CampaignID string    `json:"campaign_id"`
	ReviewerID string    `json:"reviewer_id"`
	ClaimedAt  time.Time `json:"claimed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Note       string    `json:"note,omitempty"`
	Status     string    `json:"status"`
	Version    int       `json:"version"`
	Revision   int64     `json:"revision"`
}
type ClaimStatus struct {
	Status     string    `json:"status"`
	ReviewerID string    `json:"reviewer_id,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

func DerivedClaimStatus(claim *ReviewClaim, now time.Time) ClaimStatus {
	if claim == nil {
		return ClaimStatus{Status: "UNCLAIMED"}
	}
	if claim.Status == "ACTIVE" && now.Before(claim.ExpiresAt) {
		return ClaimStatus{"ACTIVE", claim.ReviewerID, claim.ExpiresAt}
	}
	if claim.Status == "ACTIVE" {
		return ClaimStatus{"EXPIRED", claim.ReviewerID, claim.ExpiresAt}
	}
	return ClaimStatus{claim.Status, claim.ReviewerID, claim.ExpiresAt}
}

func canonicalDigest(v any) string { b, _ := json.Marshal(v); return Hash(b) }
