package domain

import (
	"errors"
	"testing"
	"time"
)

func TestResourceAvailabilityUsesHalfOpenWindowsAndFindsNextSlot(t *testing.T) {
	day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	existing := []*Campaign{{CampaignID: "old", StationCode: "GS-A", MissionWindowStart: day.Add(9 * time.Hour), MissionWindowEnd: day.Add(10 * time.Hour), DeviceIDs: []string{"D3"}, State: Draft, Revision: 7}}
	result, err := EvaluateResourceAvailability("GS-A", day.Add(9*time.Hour+30*time.Minute), day.Add(10*time.Hour+30*time.Minute), []string{"D3"}, existing, day)
	if err != nil || result.Available || len(result.Conflicts) != 2 || result.NextSlot == nil || !result.NextSlot.Start.Equal(day.Add(10*time.Hour)) {
		t.Fatalf("unexpected result: %+v %v", result, err)
	}
	adjacent, err := EvaluateResourceAvailability("GS-A", day.Add(10*time.Hour), day.Add(11*time.Hour), []string{"D3"}, existing, day)
	if err != nil || !adjacent.Available {
		t.Fatalf("adjacent window conflicted: %+v %v", adjacent, err)
	}
}

func TestMeasurementConsistencyAndMargins(t *testing.T) {
	now := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	c := &Campaign{CampaignID: "c", DeviceIDs: []string{"D1", "D2", "D3"}, Revision: 8, Threshold: ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1}, MeasurementPlan: MeasurementPlan{MaxIntervalSeconds: 2}}
	rounds := []MeasurementRound{{RoundID: "r1", Sequence: 1, Samples: []Sample{{DeviceID: "D1", SampledAt: now, TimeOffset: 0}, {DeviceID: "D2", SampledAt: now, TimeOffset: 0}, {DeviceID: "D3", SampledAt: now.Add(3 * time.Second), TimeOffset: 3}}}, {RoundID: "r2", Sequence: 2, Samples: []Sample{{DeviceID: "D1", SampledAt: now.Add(time.Minute), TimeOffset: 2}, {DeviceID: "D2", SampledAt: now.Add(time.Minute), TimeOffset: 2}, {DeviceID: "D3", SampledAt: now.Add(time.Minute), TimeOffset: 2}}}}
	report, err := BuildMeasurementConsistency(c, rounds, "", "")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, issue := range report.Issues {
		codes[issue.Code] = true
	}
	if !codes["SAMPLE_SKEW"] || !codes["DEVICE_OUTLIER"] || !codes["COMMON_MODE_SHIFT"] {
		t.Fatalf("missing issues: %+v", report.Issues)
	}
	e := Evaluation{Revision: 4, AlgorithmVersion: "a", InputSummary: "stable", Threshold: c.Threshold, Metrics: []MetricAttribution{{DeviceID: "D1", Metric: "time_deviation", ObservedValue: .95, LimitValue: 1}, {DeviceID: "D1", Metric: "frequency_deviation", ObservedValue: .1, LimitValue: 1}, {DeviceID: "D1", Metric: "drift_slope", ObservedValue: .1, LimitValue: 1}}}
	margins, err := BuildEvaluationMargins(e, "", "")
	if err != nil || len(margins.Devices) != 1 || margins.Devices[0].RiskLevel != "CRITICAL" || margins.Devices[0].ClosestMetric != "time_deviation" {
		t.Fatalf("unexpected margins: %+v %v", margins, err)
	}
}

func TestDependencyProjectionRejectsCycle(t *testing.T) {
	deviations := []DeviationCase{{DeviationID: "D1", CampaignID: "c", Status: "OPEN"}, {DeviationID: "D2", CampaignID: "c", Status: "OPEN"}}
	_, err := BuildDependencyProjection(deviations, []RemediationDependency{{DeviationID: "D1", PrerequisiteDeviationID: "D2"}, {DeviationID: "D2", PrerequisiteDeviationID: "D1"}})
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected cycle, got %v", err)
	}
	projection, err := BuildDependencyProjection(deviations, []RemediationDependency{{DeviationID: "D2", PrerequisiteDeviationID: "D1"}})
	if err != nil || projection.Nodes[0].Status != "ACTIONABLE" || projection.Nodes[1].Status != "BLOCKED" {
		t.Fatalf("unexpected projection: %+v %v", projection, err)
	}
}
