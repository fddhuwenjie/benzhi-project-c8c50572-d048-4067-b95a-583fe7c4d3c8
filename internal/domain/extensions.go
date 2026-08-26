package domain

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

func WindowConflicts(candidate *Campaign, existing []*Campaign) []ResourceConflict {
	out := []ResourceConflict{}
	devices := map[string]bool{}
	for _, d := range candidate.DeviceIDs {
		devices[d] = true
	}
	for _, c := range existing {
		if c.State == Cancelled {
			continue
		}
		if !candidate.MissionWindowStart.Before(c.MissionWindowEnd) || !c.MissionWindowStart.Before(candidate.MissionWindowEnd) {
			continue
		}
		start := candidate.MissionWindowStart
		if c.MissionWindowStart.After(start) {
			start = c.MissionWindowStart
		}
		end := candidate.MissionWindowEnd
		if c.MissionWindowEnd.Before(end) {
			end = c.MissionWindowEnd
		}
		if c.StationCode == candidate.StationCode {
			out = append(out, ResourceConflict{c.CampaignID, "station", c.StationCode, start, end, c.Revision})
		}
		for _, d := range c.DeviceIDs {
			if devices[d] {
				out = append(out, ResourceConflict{c.CampaignID, "device", d, start, end, c.Revision})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CampaignID != out[j].CampaignID {
			return out[i].CampaignID < out[j].CampaignID
		}
		if out[i].ResourceType != out[j].ResourceType {
			return out[i].ResourceType < out[j].ResourceType
		}
		if out[i].ResourceID != out[j].ResourceID {
			return out[i].ResourceID < out[j].ResourceID
		}
		if !out[i].OverlapStart.Equal(out[j].OverlapStart) {
			return out[i].OverlapStart.Before(out[j].OverlapStart)
		}
		return out[i].OverlapEnd.Before(out[j].OverlapEnd)
	})
	return out
}

func MeasurementPreflight(c *Campaign, existing, batch []MeasurementRound) PreflightResult {
	r := PreflightResult{Submittable: true, Issues: []QualityIssue{}, DeviceSampleCounts: map[string]int{}}
	all := append(append([]MeasurementRound{}, existing...), batch...)
	sort.Slice(all, func(i, j int) bool { return all[i].Sequence < all[j].Sequence })
	seq := map[int]bool{}
	ids := map[string]bool{}
	var min, max time.Time
	issue := func(x QualityIssue) {
		r.Issues = append(r.Issues, x)
		if x.Severity == "ERROR" {
			r.Submittable = false
		}
	}
	var previous time.Time
	for _, round := range all {
		if seq[round.Sequence] {
			issue(QualityIssue{round.RoundID, "", "sequence", "ERROR", "DUPLICATE_SEQUENCE", "轮次序号重复"})
		}
		seq[round.Sequence] = true
		if ids[round.RoundID] {
			issue(QualityIssue{round.RoundID, "", "round_id", "ERROR", "DUPLICATE_ROUND_ID", "轮次编号重复"})
		}
		ids[round.RoundID] = true
		var lo, hi time.Time
		for _, s := range round.Samples {
			r.DeviceSampleCounts[s.DeviceID]++
			if !previous.IsZero() && s.SampledAt.Before(previous) {
				issue(QualityIssue{round.RoundID, s.DeviceID, "sampled_at", "ERROR", "SAMPLED_AT_NOT_MONOTONIC", "采样时间早于前序轮次"})
			}
			if lo.IsZero() || s.SampledAt.Before(lo) {
				lo = s.SampledAt
			}
			if hi.IsZero() || s.SampledAt.After(hi) {
				hi = s.SampledAt
			}
			if min.IsZero() || s.SampledAt.Before(min) {
				min = s.SampledAt
			}
			if max.IsZero() || s.SampledAt.After(max) {
				max = s.SampledAt
			}
		}
		if !hi.IsZero() {
			previous = hi
		}
		if !lo.IsZero() && hi.Sub(lo) > time.Minute {
			issue(QualityIssue{round.RoundID, "", "samples", "ERROR", "ROUND_SAMPLE_DISPERSION", "同轮设备采样时间离散度超过一分钟"})
		}
		for _, f := range []string{"temperature", "humidity", "signal_status"} {
			v, ok := round.Environment[f]
			if !ok || strings.TrimSpace(v) == "" {
				issue(QualityIssue{round.RoundID, "", "environment." + f, "WARNING", "ENVIRONMENT_MISSING", "环境项缺失"})
				continue
			}
			if f == "signal_status" {
				continue
			}
			n, e := strconv.ParseFloat(v, 64)
			if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				issue(QualityIssue{round.RoundID, "", "environment." + f, "ERROR", "ENVIRONMENT_NOT_FINITE", "环境值不是有限数值"})
				continue
			}
			if (f == "temperature" && (n < -100 || n > 100)) || (f == "humidity" && (n < 0 || n > 100)) {
				issue(QualityIssue{round.RoundID, "", "environment." + f, "ERROR", "ENVIRONMENT_OUT_OF_RANGE", "环境值超出物理范围"})
			}
		}
	}
	if !min.IsZero() {
		r.EffectiveSpanSeconds = max.Sub(min).Seconds()
	}
	if len(all) > 1 && r.EffectiveSpanSeconds <= 0 {
		issue(QualityIssue{"", "", "sampled_at", "ERROR", "INSUFFICIENT_TIME_SPAN", "用于斜率计算的时间跨度不足"})
	}
	sort.Slice(r.Issues, func(i, j int) bool {
		a, b := r.Issues[i], r.Issues[j]
		if a.RoundID != b.RoundID {
			return a.RoundID < b.RoundID
		}
		if a.DeviceID != b.DeviceID {
			return a.DeviceID < b.DeviceID
		}
		return a.Code < b.Code
	})
	return r
}

func EvaluationDiff(from, to Evaluation) []DeviceEvaluationDiff {
	a := map[string]DeviceEvaluation{}
	b := map[string]DeviceEvaluation{}
	for _, d := range from.Devices {
		a[d.DeviceID] = d
	}
	for _, d := range to.Devices {
		b[d.DeviceID] = d
	}
	ids := map[string]bool{}
	for id := range a {
		ids[id] = true
	}
	for id := range b {
		ids[id] = true
	}
	out := []DeviceEvaluationDiff{}
	for id := range ids {
		x, xok := a[id]
		y, yok := b[id]
		ch := "UNCHANGED"
		if !xok {
			ch = "ADDED"
		} else if x.Conclusion != y.Conclusion {
			if y.Conclusion == "PASS" {
				ch = "IMPROVED"
			} else {
				ch = "WORSENED"
			}
		}
		out = append(out, DeviceEvaluationDiff{id, y.MaxAbsDeviation - x.MaxAbsDeviation, y.MeanFrequency - x.MeanFrequency, y.DriftSlope - x.DriftSlope, ch, x.Conclusion, y.Conclusion})
		_ = yok
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out
}
