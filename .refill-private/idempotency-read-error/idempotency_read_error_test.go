package idempotencyreaderror

import (
	"database/sql"
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestReferenceReplayPropagatesCorruptIdempotencyResponse(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "campaign.db")
	store, err := persistence.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := application.New(store)
	now := time.Now().UTC().Truncate(time.Second)
	campaign, err := service.Create(application.CreateInput{
		CampaignID:  "idem-corrupt",
		StationCode: "GS-1",
		Start:       now.Add(-time.Hour),
		End:         now.Add(time.Hour),
		Devices:     []string{"D1"},
		Threshold:   domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1},
		By:          "operator",
	}, "create")
	if err != nil {
		t.Fatal(err)
	}
	evidence := domain.ReferenceEvidence{EvidenceID: "clock", ReferenceKind: "clock", Provider: "lab", CertificateDigest: "aa", SubmittedBy: "operator", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}
	if _, err := service.ReferenceDetailed(campaign.CampaignID, "reference-1", evidence, campaign.Revision); err != nil {
		t.Fatal(err)
	}

	// Corrupt only the persisted idempotent response while retaining its request hash.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE idem SET response=? WHERE request_id=?`, []byte("{not-json"), "reference|idem-corrupt|reference-1"); err != nil {
		t.Fatal(err)
	}

	_, err = service.ReferenceDetailed(campaign.CampaignID, "reference-1", evidence, 999)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "invalid character") {
		t.Fatalf("expected corrupt idempotency response error to propagate, got %v", err)
	}
}
