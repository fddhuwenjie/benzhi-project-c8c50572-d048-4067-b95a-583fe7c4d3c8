package measurementbatchalias

import (
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type callerView struct {
	RoundIDs    []string
	SampleIDs   [][]string
	Environment []map[string]string
}

func viewOf(batch []domain.MeasurementRound) callerView {
	view := callerView{
		RoundIDs:    make([]string, len(batch)),
		SampleIDs:   make([][]string, len(batch)),
		Environment: make([]map[string]string, len(batch)),
	}
	for i, round := range batch {
		view.RoundIDs[i] = round.RoundID
		view.SampleIDs[i] = make([]string, len(round.Samples))
		for j, sample := range round.Samples {
			view.SampleIDs[i][j] = sample.DeviceID
		}
		view.Environment[i] = make(map[string]string, len(round.Environment))
		for key, value := range round.Environment {
			view.Environment[i][key] = value
		}
	}
	return view
}

func TestMeasureIdemPreservesCallerOwnedBatch(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	service := application.New(store)
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	campaign, err := service.Create(application.CreateInput{
		CampaignID: "batch-alias", StationCode: "GS-ALIAS", Start: start, End: end,
		Devices:   []string{"d1", "d2"},
		Threshold: domain.ThresholdProfile{MaxAbsDeviation: 10, MaxFrequencyDeviation: 10, MaxDriftSlope: 10},
		By:        "engineer",
	}, "create-batch-alias")
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []domain.ReferenceEvidence{
		{EvidenceID: "clock", ReferenceKind: "clock", Provider: "lab", CertificateDigest: "aa", ValidFrom: start, ValidUntil: end, SubmittedBy: "engineer"},
		{EvidenceID: "frequency", ReferenceKind: "frequency", Provider: "lab", CertificateDigest: "bb", ValidFrom: start, ValidUntil: end, SubmittedBy: "engineer"},
	} {
		result, referenceErr := service.ReferenceDetailed(campaign.CampaignID, "ref-"+evidence.EvidenceID, evidence, campaign.Revision)
		if referenceErr != nil {
			t.Fatal(referenceErr)
		}
		campaign = result.Campaign
	}

	batch := []domain.MeasurementRound{
		{
			RoundID: "round-2", Sequence: 2, OperatorID: "engineer", CapturedAt: now.Add(time.Minute),
			Environment: map[string]string{"temperature": " 21.0 ", "humidity": " 45 ", "signal_status": " OK "},
			Samples: []domain.Sample{
				{DeviceID: "d2", TimeOffset: 0.2, SampledAt: now.Add(time.Minute)},
				{DeviceID: "d1", TimeOffset: 0.1, SampledAt: now.Add(time.Minute)},
			},
		},
		{
			RoundID: "round-1", Sequence: 1, OperatorID: "engineer", CapturedAt: now,
			Environment: map[string]string{"temperature": " 20.0 ", "humidity": " 40 ", "signal_status": " GOOD "},
			Samples: []domain.Sample{
				{DeviceID: "d2", TimeOffset: 0.1, SampledAt: now},
				{DeviceID: "d1", TimeOffset: 0.05, SampledAt: now},
			},
		},
	}
	want := viewOf(batch)
	if _, err = service.MeasureIdem(campaign.CampaignID, batch, campaign.Revision, "measure-batch-alias", ""); err != nil {
		t.Fatal(err)
	}
	if got := viewOf(batch); !reflect.DeepEqual(got, want) {
		t.Fatalf("MeasureIdem changed caller-owned batch: got %+v, want %+v", got, want)
	}
}
