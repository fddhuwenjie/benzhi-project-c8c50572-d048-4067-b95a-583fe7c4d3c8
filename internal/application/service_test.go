package application

import (
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"path/filepath"
	"testing"
	"time"
)

func TestQualificationFlowIsAtomicAndIdempotent(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(store)
	now := time.Now().UTC()
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	campaign, err := service.Create(CreateInput{CampaignID: "c1", StationCode: "GS", Start: start, End: end, Devices: []string{"d2", "d1"}, Threshold: domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1}, By: "creator"}, "create-1")
	if err != nil {
		t.Fatal(err)
	}
	clock1 := domain.ReferenceEvidence{EvidenceID: "clock-1", ReferenceKind: "clock", Provider: "lab", CertificateDigest: "aa", SubmittedBy: "creator", ValidFrom: start, ValidUntil: now}
	first, err := service.ReferenceDetailed("c1", "ref-1", clock1, campaign.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if first.Campaign.State != domain.Draft || len(first.Coverage.ClockGaps) != 1 {
		t.Fatalf("unexpected partial coverage: %+v", first)
	}
	replay, err := service.ReferenceDetailed("c1", "ref-1", clock1, 999)
	if err != nil || replay.Campaign.Revision != first.Campaign.Revision {
		t.Fatalf("idempotent replay failed: %+v %v", replay, err)
	}
	refs, _ := store.References("c1")
	if len(refs) != 1 {
		t.Fatalf("replay inserted evidence: %d", len(refs))
	}
	clock2 := domain.ReferenceEvidence{EvidenceID: "clock-2", ReferenceKind: "clock", Provider: "lab", CertificateDigest: "bb", SubmittedBy: "creator", ValidFrom: now, ValidUntil: end}
	second, err := service.ReferenceDetailed("c1", "", clock2, first.Campaign.Revision)
	if err != nil {
		t.Fatal(err)
	}
	frequency := domain.ReferenceEvidence{EvidenceID: "frequency", ReferenceKind: "frequency", Provider: "lab", CertificateDigest: "cc", SubmittedBy: "creator", ValidFrom: start, ValidUntil: end}
	verified, err := service.ReferenceDetailed("c1", "", frequency, second.Campaign.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Coverage.Complete || verified.Campaign.State != domain.ReferenceVerified {
		t.Fatal("reference coverage did not complete")
	}
	rounds := []domain.MeasurementRound{
		{RoundID: "r1", Sequence: 1, OperatorID: "operator", CapturedAt: now, Samples: []domain.Sample{{DeviceID: "d2", TimeOffset: .2, SampledAt: now}, {DeviceID: "d1", TimeOffset: .1, SampledAt: now}}},
		{RoundID: "r2", Sequence: 2, OperatorID: "operator", CapturedAt: now, Samples: []domain.Sample{{DeviceID: "d1", TimeOffset: .2, SampledAt: now}, {DeviceID: "d2", TimeOffset: .1, SampledAt: now}}},
	}
	measured, err := service.MeasureBatch("c1", rounds, verified.Campaign.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if measured.Revision != verified.Campaign.Revision+1 {
		t.Fatal("batch changed revision more than once")
	}
	badBatch := []domain.MeasurementRound{rounds[0], {RoundID: "r3", Sequence: 3, OperatorID: "operator", Samples: rounds[0].Samples}}
	if _, err = service.MeasureBatch("c1", badBatch, measured.Revision); err == nil {
		t.Fatal("duplicate batch should fail")
	}
	savedRounds, _ := store.Rounds("c1")
	savedCampaign, _ := store.GetCampaign("c1")
	if len(savedRounds) != 2 || savedCampaign.Revision != measured.Revision {
		t.Fatal("failed batch left partial state")
	}
	evaluated, err := service.EvaluateIdem("c1", measured.Revision, "eval-1")
	if err != nil {
		t.Fatal(err)
	}
	if evaluated.Campaign.State != domain.ReviewPending || len(evaluated.Evaluation.Devices) != 2 || evaluated.Evaluation.Devices[0].DeviceID != "d1" {
		t.Fatal("evaluation was not deterministic")
	}
	again, err := service.EvaluateIdem("c1", measured.Revision, "eval-1")
	if err != nil || again.Campaign.Revision != evaluated.Campaign.Revision {
		t.Fatal("evaluation replay changed revision")
	}
	if _, err = service.Review("c1", domain.Review{ReviewerID: "operator", Approved: true, Statement: "同意签发"}, evaluated.Campaign.Revision); err == nil {
		t.Fatal("measurement operator reviewed campaign")
	}
	approved, err := service.Review("c1", domain.Review{ReviewerID: "reviewer", Approved: true, Statement: "同意签发"}, evaluated.Campaign.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Archive("c1", approved.Revision); err != nil {
		t.Fatal(err)
	}
	if valid, _ := service.Verify("c1")["valid"].(bool); !valid {
		t.Fatal("archived artifact did not verify")
	}
}
