package reproducibility_read_error

import (
	"database/sql"
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	_ "modernc.org/sqlite"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifyReproducibilityPropagatesMaterialReadError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "reproducibility.db")
	store, err := persistence.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	service := application.New(store)
	now := time.Now().UTC().Truncate(time.Second)
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	campaign, err := service.Create(application.CreateInput{
		CampaignID:  "reproducibility-corrupt",
		StationCode: "GS-REPRO",
		Start:       start, End: end, Devices: []string{"D1"},
		Threshold: domain.ThresholdProfile{MaxAbsDeviation: 10, MaxFrequencyDeviation: 10, MaxDriftSlope: 10},
		By:        "engineer",
	}, "create")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	clock := domain.ReferenceEvidence{EvidenceID: "clock", ReferenceKind: "clock", Provider: "lab-clock", CertificateDigest: "aa", SubmittedBy: "engineer", ValidFrom: start, ValidUntil: end}
	result, err := service.ReferenceDetailed(campaign.CampaignID, "", clock, campaign.Revision)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	frequency := domain.ReferenceEvidence{EvidenceID: "frequency", ReferenceKind: "frequency", Provider: "lab-frequency", CertificateDigest: "bb", SubmittedBy: "engineer", ValidFrom: start, ValidUntil: end}
	result, err = service.ReferenceDetailed(campaign.CampaignID, "", frequency, result.Campaign.Revision)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	base := start.Add(10 * time.Minute)
	measured, err := service.MeasureBatch(campaign.CampaignID, []domain.MeasurementRound{
		{RoundID: "round-1", Sequence: 1, OperatorID: "collector", Samples: []domain.Sample{{DeviceID: "D1", SampledAt: base}}},
		{RoundID: "round-2", Sequence: 2, OperatorID: "collector", Samples: []domain.Sample{{DeviceID: "D1", SampledAt: base.Add(time.Minute)}}},
	}, result.Campaign.Revision)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	evaluated, err := service.EvaluateIdem(campaign.CampaignID, measured.Revision, "evaluate")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE evaluations SET data=? WHERE campaign_id=?", []byte("{"), campaign.CampaignID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = persistence.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service = application.New(store)
	_, err = service.VerifyReproducibility(campaign.CampaignID, evaluated.Evaluation.Revision)
	if err == nil || !strings.Contains(err.Error(), "malformed JSON") {
		t.Fatalf("expected malformed evaluation read error, got %v", err)
	}
}
