package reference_correction_atomicity_test

import (
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"path/filepath"
	"testing"
	"time"
)

func TestReferenceCorrectionRollbackPreservesOriginalEvidence(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "qualification.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := application.New(store)
	now := time.Now().UTC()
	start, end := now.Add(-time.Hour), now.Add(time.Hour)
	campaign, err := service.Create(application.CreateInput{
		CampaignID:  "atomic-correction",
		StationCode: "GS-ATOMIC",
		Start:       start,
		End:         end,
		Devices:     []string{"clock-device"},
		Threshold: domain.ThresholdProfile{
			MaxAbsDeviation:       1,
			MaxFrequencyDeviation: 1,
			MaxDriftSlope:         1,
		},
		By: "engineer",
	}, "create")
	if err != nil {
		t.Fatal(err)
	}
	original := domain.ReferenceEvidence{
		EvidenceID:        "clock-original",
		ReferenceKind:     "clock",
		Provider:          "clock-lab",
		CertificateDigest: "aa11",
		SubmittedBy:       "engineer",
		ValidFrom:         start,
		ValidUntil:        end,
	}
	partial, err := service.ReferenceDetailed(campaign.CampaignID, "original", original, campaign.Revision)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a late SQLite constraint failure after the correction has been
	// validated but before its aggregate revision can be committed.
	collidingRevision := partial.Campaign.Revision + 1
	if err = store.SaveAudit(audit.NewEvent(campaign.CampaignID, collidingRevision, "FAULT", "injector", "", now)); err != nil {
		t.Fatal(err)
	}
	replacement := domain.ReferenceEvidence{
		EvidenceID:        "clock-replacement",
		ReferenceKind:     "clock",
		Provider:          "replacement-lab",
		CertificateDigest: "bb22",
		SubmittedBy:       "quality-engineer",
		ValidFrom:         start,
		ValidUntil:        end,
	}
	_, err = service.CorrectReference(campaign.CampaignID, "correction", application.CorrectionInput{
		EvidenceID:       original.EvidenceID,
		Reason:           "原证书录入错误",
		Replacement:      replacement,
		ExpectedRevision: partial.Campaign.Revision,
	})
	if err == nil {
		t.Fatal("expected the colliding audit revision to reject the correction")
	}

	persistedCampaign, err := store.GetCampaign(campaign.CampaignID)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := store.References(campaign.CampaignID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedCampaign.Revision != partial.Campaign.Revision {
		t.Fatalf("failed correction advanced campaign revision: got %d want %d", persistedCampaign.Revision, partial.Campaign.Revision)
	}
	if len(refs) != 1 || refs[0].EvidenceID != original.EvidenceID || refs[0].Replaced || refs[0].ReplacementEvidenceID != "" {
		t.Fatalf("failed correction left partially committed reference evidence: %+v", refs)
	}
}
