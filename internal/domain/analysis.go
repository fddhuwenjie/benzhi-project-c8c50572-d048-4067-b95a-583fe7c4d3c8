package domain

import (
	"errors"
	"sort"
	"time"
)

type CoverageSegment struct {
	ReferenceKind string    `json:"reference_kind"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	EvidenceIDs   []string  `json:"evidence_ids"`
	EvidenceCount int       `json:"evidence_count"`
}
type ResilienceGap struct {
	ReferenceKind string    `json:"reference_kind"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
}
type CriticalEvidence struct {
	EvidenceID             string          `json:"evidence_id"`
	NewGaps                []ResilienceGap `json:"new_gaps"`
	AffectedReferenceKinds []string        `json:"affected_reference_kinds"`
	Criticality            string          `json:"criticality"`
}
type ReferenceResilience struct {
	AnalyzedRevision    int64              `json:"analyzed_revision"`
	CoverageSegments    []CoverageSegment  `json:"coverage_segments"`
	SingleSourceWindows []CoverageSegment  `json:"single_source_windows"`
	Gaps                []ResilienceGap    `json:"gaps"`
	CriticalEvidence    []CriticalEvidence `json:"critical_evidence"`
}

func BuildReferenceResilience(c *Campaign, refs []ReferenceEvidence, withdrawals []ReferenceWithdrawal, kind string) (ReferenceResilience, error) {
	if c == nil || !c.MissionWindowStart.Before(c.MissionWindowEnd) {
		return ReferenceResilience{}, errors.New("TASK_WINDOW_INVALID")
	}
	if kind != "" && kind != "clock" && kind != "frequency" {
		return ReferenceResilience{}, errors.New("REFERENCE_KIND_INVALID")
	}
	withdrawn := map[string]bool{}
	for _, w := range withdrawals {
		withdrawn[w.EvidenceID] = true
	}
	active := make([]ReferenceEvidence, 0)
	for _, e := range refs {
		if !e.ValidFrom.Before(e.ValidUntil) {
			return ReferenceResilience{}, errors.New("EVIDENCE_TIME_REVERSED")
		}
		if e.Replaced || withdrawn[e.EvidenceID] || !e.ValidFrom.Before(e.ValidUntil) {
			continue
		}
		if kind != "" && e.ReferenceKind != kind {
			continue
		}
		if e.ReferenceKind != "clock" && e.ReferenceKind != "frequency" {
			continue
		}
		if !e.ValidUntil.After(c.MissionWindowStart) || !e.ValidFrom.Before(c.MissionWindowEnd) {
			continue
		}
		active = append(active, e)
	}
	build := func(items []ReferenceEvidence) (segs []CoverageSegment, gaps []ResilienceGap) {
		for _, k := range []string{"clock", "frequency"} {
			if kind != "" && kind != k {
				continue
			}
			points := []time.Time{c.MissionWindowStart, c.MissionWindowEnd}
			for _, e := range items {
				if e.ReferenceKind == k {
					st, en := e.ValidFrom, e.ValidUntil
					if st.Before(c.MissionWindowStart) {
						st = c.MissionWindowStart
					}
					if en.After(c.MissionWindowEnd) {
						en = c.MissionWindowEnd
					}
					points = append(points, st, en)
				}
			}
			sort.Slice(points, func(i, j int) bool { return points[i].Before(points[j]) })
			uniq := points[:0]
			for _, p := range points {
				if len(uniq) == 0 || !p.Equal(uniq[len(uniq)-1]) {
					uniq = append(uniq, p)
				}
			}
			for i := 0; i+1 < len(uniq); i++ {
				st, en := uniq[i], uniq[i+1]
				ids := []string{}
				for _, e := range items {
					if e.ReferenceKind == k && !e.ValidFrom.After(st) && !e.ValidUntil.Before(en) {
						ids = append(ids, e.EvidenceID)
					}
				}
				sort.Strings(ids)
				g := ResilienceGap{ReferenceKind: k, Start: st, End: en}
				if len(ids) == 0 {
					gaps = append(gaps, g)
				} else {
					seg := CoverageSegment{ReferenceKind: k, Start: st, End: en, EvidenceIDs: ids, EvidenceCount: len(ids)}
					segs = append(segs, seg)
				}
			}
		}
		return
	}
	segs, gaps := build(active)
	out := ReferenceResilience{AnalyzedRevision: c.Revision, CoverageSegments: segs, SingleSourceWindows: []CoverageSegment{}, Gaps: gaps, CriticalEvidence: []CriticalEvidence{}}
	for _, s := range segs {
		if s.EvidenceCount == 1 {
			out.SingleSourceWindows = append(out.SingleSourceWindows, s)
		}
	}
	for _, e := range active {
		reduced := make([]ReferenceEvidence, 0, len(active)-1)
		for _, x := range active {
			if x.EvidenceID != e.EvidenceID {
				reduced = append(reduced, x)
			}
		}
		_, rg := build(reduced)
		base := map[string]bool{}
		for _, g := range gaps {
			base[g.ReferenceKind+g.Start.String()+g.End.String()] = true
		}
		ng := []ResilienceGap{}
		kinds := map[string]bool{}
		for _, g := range rg {
			if !base[g.ReferenceKind+g.Start.String()+g.End.String()] {
				ng = append(ng, g)
				kinds[g.ReferenceKind] = true
			}
		}
		if len(ng) > 0 {
			ks := []string{}
			for k := range kinds {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			out.CriticalEvidence = append(out.CriticalEvidence, CriticalEvidence{EvidenceID: e.EvidenceID, NewGaps: ng, AffectedReferenceKinds: ks, Criticality: "CRITICAL"})
		}
	}
	sort.Slice(out.CoverageSegments, func(i, j int) bool {
		a, b := out.CoverageSegments[i], out.CoverageSegments[j]
		if !a.Start.Equal(b.Start) {
			return a.Start.Before(b.Start)
		}
		if !a.End.Equal(b.End) {
			return a.End.Before(b.End)
		}
		return a.EvidenceIDs[0] < b.EvidenceIDs[0]
	})
	sort.Slice(out.SingleSourceWindows, func(i, j int) bool { return out.SingleSourceWindows[i].Start.Before(out.SingleSourceWindows[j].Start) })
	sort.Slice(out.Gaps, func(i, j int) bool {
		if !out.Gaps[i].Start.Equal(out.Gaps[j].Start) {
			return out.Gaps[i].Start.Before(out.Gaps[j].Start)
		}
		if !out.Gaps[i].End.Equal(out.Gaps[j].End) {
			return out.Gaps[i].End.Before(out.Gaps[j].End)
		}
		return out.Gaps[i].ReferenceKind < out.Gaps[j].ReferenceKind
	})
	sort.Slice(out.CriticalEvidence, func(i, j int) bool { return out.CriticalEvidence[i].EvidenceID < out.CriticalEvidence[j].EvidenceID })
	return out, nil
}

type EffectivenessObservation struct {
	RoundID                string    `json:"round_id"`
	AttemptedAt            time.Time `json:"attempted_at"`
	ObservedValue          float64   `json:"observed_value"`
	LimitValue             float64   `json:"limit_value"`
	ThresholdMultiple      float64   `json:"threshold_multiple"`
	ImprovementFromInitial float64   `json:"improvement_from_initial"`
	ChangeFromPrevious     float64   `json:"change_from_previous"`
	Result                 string    `json:"result"`
	ConsecutiveFailures    int       `json:"consecutive_failures"`
}
type DeviationTrack struct {
	DeviationID          string                     `json:"deviation_id"`
	DeviceID             string                     `json:"device_id"`
	Metric               string                     `json:"metric"`
	Status               string                     `json:"status"`
	InitialObservedValue float64                    `json:"initial_observed_value"`
	LimitValue           float64                    `json:"limit_value"`
	BestObservedValue    float64                    `json:"best_observed_value"`
	Observations         []EffectivenessObservation `json:"observations"`
	LatestTrend          string                     `json:"latest_trend"`
	LatestPlan           *RemediationPlan           `json:"latest_plan,omitempty"`
	PlanCompleteness     float64                    `json:"plan_completeness"`
	ExecutionEvidence    []RemediationEvidence      `json:"execution_evidence,omitempty"`
}
type EffectivenessIssue struct {
	Code        string `json:"code"`
	DeviationID string `json:"deviation_id,omitempty"`
	RoundID     string `json:"round_id,omitempty"`
	Message     string `json:"message"`
}
type RemediationEffectiveness struct {
	AnalyzedRevision int64                `json:"analyzed_revision"`
	DeviationTracks  []DeviationTrack     `json:"deviation_tracks"`
	Summary          map[string]int       `json:"summary"`
	Issues           []EffectivenessIssue `json:"issues"`
}

func BuildRemediationEffectiveness(c *Campaign, ds []DeviationCase, plans []RemediationPlan, rounds []MeasurementRound, evidence []RemediationEvidence, kindDevice, kindMetric, status string) RemediationEffectiveness {
	out := RemediationEffectiveness{AnalyzedRevision: c.Revision, DeviationTracks: []DeviationTrack{}, Summary: map[string]int{}, Issues: []EffectivenessIssue{}}
	rm := map[string]bool{}
	for _, r := range rounds {
		rm[r.RoundID] = true
	}
	latest := map[string]RemediationPlan{}
	for _, p := range plans {
		if old, ok := latest[p.DeviationID]; !ok || p.Version > old.Version {
			latest[p.DeviationID] = p
		}
	}
	for _, d := range ds {
		if kindDevice != "" && d.DeviceID != kindDevice || kindMetric != "" && d.Metric != kindMetric || status != "" && d.Status != status {
			continue
		}
		t := DeviationTrack{DeviationID: d.DeviationID, DeviceID: d.DeviceID, Metric: d.Metric, Status: d.Status, InitialObservedValue: d.ObservedValue, LimitValue: d.LimitValue, BestObservedValue: d.ObservedValue, Observations: []EffectivenessObservation{}}
		if d.LimitValue <= 0 {
			out.Issues = append(out.Issues, EffectivenessIssue{Code: "LIMIT_NON_POSITIVE", DeviationID: d.DeviationID, Message: "门限必须为正"})
		}
		if p, ok := latest[d.DeviationID]; ok {
			t.LatestPlan = &p
			n := 0
			if p.Owner != "" {
				n++
			}
			if p.RootCause != "" {
				n++
			}
			if p.Containment != "" {
				n++
			}
			if p.CorrectiveAction != "" {
				n++
			}
			t.PlanCompleteness = float64(n) / 4
		}
		for _, ev := range evidence {
			if ev.DeviationID == d.DeviationID {
				t.ExecutionEvidence = append(t.ExecutionEvidence, ev)
			}
		}
		sort.Slice(t.ExecutionEvidence, func(i, j int) bool {
			if t.ExecutionEvidence[i].OccurredAt.Equal(t.ExecutionEvidence[j].OccurredAt) {
				return t.ExecutionEvidence[i].EvidenceID < t.ExecutionEvidence[j].EvidenceID
			}
			return t.ExecutionEvidence[i].OccurredAt.Before(t.ExecutionEvidence[j].OccurredAt)
		})
		attempts := append([]RetestAttempt(nil), d.Attempts...)
		sort.SliceStable(attempts, func(i, j int) bool {
			if attempts[i].AttemptedAt.Equal(attempts[j].AttemptedAt) {
				return attempts[i].RoundID < attempts[j].RoundID
			}
			return attempts[i].AttemptedAt.Before(attempts[j].AttemptedAt)
		})
		prev := d.ObservedValue
		fails := 0
		seenRounds := map[string]bool{}
		for _, a := range attempts {
			if seenRounds[a.RoundID] {
				out.Issues = append(out.Issues, EffectivenessIssue{Code: "TRACK_ORDER_INVALID", DeviationID: d.DeviationID, RoundID: a.RoundID, Message: "复测轮次重复"})
			}
			seenRounds[a.RoundID] = true
			if !rm[a.RoundID] {
				out.Issues = append(out.Issues, EffectivenessIssue{"RETEST_ROUND_MISSING", d.DeviationID, a.RoundID, "复测轮次不存在"})
				continue
			}
			if a.LimitValue <= 0 {
				out.Issues = append(out.Issues, EffectivenessIssue{"LIMIT_NON_POSITIVE", d.DeviationID, a.RoundID, "门限必须为正"})
				continue
			}
			mult := a.ObservedValue / a.LimitValue
			imp := 0.0
			if d.ObservedValue != 0 {
				imp = (d.ObservedValue - a.ObservedValue) / d.ObservedValue
			}
			ch := 0.0
			if prev != 0 {
				ch = (prev - a.ObservedValue) / prev
			}
			if a.Result == "FAIL" {
				fails++
			} else {
				fails = 0
			}
			t.Observations = append(t.Observations, EffectivenessObservation{RoundID: a.RoundID, AttemptedAt: a.AttemptedAt, ObservedValue: a.ObservedValue, LimitValue: a.LimitValue, ThresholdMultiple: mult, ImprovementFromInitial: imp, ChangeFromPrevious: ch, Result: a.Result, ConsecutiveFailures: fails})
			if a.ObservedValue < t.BestObservedValue {
				t.BestObservedValue = a.ObservedValue
			}
			prev = a.ObservedValue
		}
		if d.Status == "CLOSED" {
			t.LatestTrend = "CLOSED"
		} else if len(t.Observations) > 0 {
			last := t.Observations[len(t.Observations)-1]
			t.LatestTrend = "STALLED"
			if last.ChangeFromPrevious > 0.01 {
				t.LatestTrend = "IMPROVING"
			}
			if last.ChangeFromPrevious < -0.01 {
				t.LatestTrend = "REGRESSED"
			}
		}
		out.Summary[t.LatestTrend]++
		out.DeviationTracks = append(out.DeviationTracks, t)
	}
	sort.Slice(out.DeviationTracks, func(i, j int) bool { return out.DeviationTracks[i].DeviationID < out.DeviationTracks[j].DeviationID })
	return out
}
