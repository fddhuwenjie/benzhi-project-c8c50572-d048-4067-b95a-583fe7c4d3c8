package retestroundalias

import (
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRemediateDetailedPreservesCallerRetestRound(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "retest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := application.New(store)

	start := time.Date(2032, 4, 5, 8, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	campaign, err := service.Create(application.CreateInput{
		CampaignID:  "retest-alias-campaign",
		StationCode: "GS-ALIAS",
		Start:       start,
		End:         end,
		Devices:     []string{"DEVICE-A", "DEVICE-B"},
		Threshold: domain.ThresholdProfile{
			MaxAbsDeviation:       1,
			MaxFrequencyDeviation: 10,
			MaxDriftSlope:         10,
		},
		By: "engineer",
	}, "create-retest-alias")
	if err != nil {
		t.Fatal(err)
	}

	revision := campaign.Revision
	for i, kind := range []string{"clock", "frequency"} {
		result, referenceErr := service.ReferenceDetailed(campaign.CampaignID, "reference-"+kind, domain.ReferenceEvidence{
			EvidenceID:        "reference-" + kind,
			ReferenceKind:     kind,
			Provider:          "reference-lab",
			CertificateDigest: []string{"aa", "bb"}[i],
			SubmittedBy:       "engineer",
			ValidFrom:         start,
			ValidUntil:        end,
		}, revision)
		if referenceErr != nil {
			t.Fatal(referenceErr)
		}
		revision = result.Campaign.Revision
	}

	measured, err := service.MeasureBatch(campaign.CampaignID, []domain.MeasurementRound{
		{
			RoundID: "round-1", Sequence: 1, OperatorID: "collector",
			Samples: []domain.Sample{
				{DeviceID: "DEVICE-A", TimeOffset: 2, SampledAt: start.Add(10 * time.Minute)},
				{DeviceID: "DEVICE-B", TimeOffset: 2, SampledAt: start.Add(10 * time.Minute)},
			},
		},
		{
			RoundID: "round-2", Sequence: 2, OperatorID: "collector",
			Samples: []domain.Sample{
				{DeviceID: "DEVICE-A", TimeOffset: 2, SampledAt: start.Add(11 * time.Minute)},
				{DeviceID: "DEVICE-B", TimeOffset: 2, SampledAt: start.Add(11 * time.Minute)},
			},
		},
	}, revision)
	if err != nil {
		t.Fatal(err)
	}
	evaluated, err := service.EvaluateIdem(campaign.CampaignID, measured.Revision, "evaluate-retest-alias")
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluated.Deviations) != 2 {
		t.Fatalf("expected two time-deviation cases, got %+v", evaluated.Deviations)
	}
	cases := append([]domain.DeviationCase(nil), evaluated.Deviations...)
	for i := range cases {
		cases[i].RootCause = "reference distribution delay"
		cases[i].Containment = "isolate affected timing path"
		cases[i].CorrectiveAction = "replace distribution module"
	}

	retest := domain.MeasurementRound{
		RoundID: "round-3", Sequence: 3, OperatorID: "remediation-engineer",
		Samples: []domain.Sample{
			{DeviceID: "DEVICE-B", TimeOffset: 0.1, SampledAt: start.Add(12 * time.Minute)},
			{DeviceID: "DEVICE-A", TimeOffset: 0.1, SampledAt: start.Add(12 * time.Minute)},
		},
		Environment: map[string]string{"temperature": " 20 ", "humidity": " 40 "},
	}
	originalSamples := append([]domain.Sample(nil), retest.Samples...)
	originalEnvironment := make(map[string]string, len(retest.Environment))
	for key, value := range retest.Environment {
		originalEnvironment[key] = value
	}

	result, err := service.RemediateDetailed(campaign.CampaignID, cases, retest, evaluated.Campaign.Revision)
	if err != nil {
		t.Fatalf("remediation failed: %v", err)
	}
	if result.Campaign.State != domain.ReviewPending {
		t.Fatalf("unexpected remediation state: %s", result.Campaign.State)
	}
	storedRounds, err := store.Rounds(campaign.CampaignID)
	if err != nil {
		t.Fatal(err)
	}
	storedRetest := storedRounds[len(storedRounds)-1]
	if storedRetest.Samples[0].DeviceID != "DEVICE-A" || storedRetest.Samples[1].DeviceID != "DEVICE-B" || storedRetest.Environment["temperature"] != "20" || storedRetest.Environment["humidity"] != "40" {
		t.Fatalf("stored retest was not canonicalized: %+v", storedRetest)
	}
	if !reflect.DeepEqual(retest.Samples, originalSamples) || !reflect.DeepEqual(retest.Environment, originalEnvironment) {
		t.Fatalf("caller-owned retest round mutated: got samples=%+v environment=%+v", retest.Samples, retest.Environment)
	}
}
