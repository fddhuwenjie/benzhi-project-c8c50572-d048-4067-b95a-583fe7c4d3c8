package cancelled_archive_commit_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestCancelledArchiveDoesNotCommitArtifact(t *testing.T) {
	reached := make(chan struct{})
	release := make(chan struct{})
	sqlite.MustRegisterScalarFunction("refill_archive_gate", 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		close(reached)
		<-release
		return int64(1), nil
	})

	databasePath := filepath.Join(t.TempDir(), "archive.db")
	store, err := persistence.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := application.New(store)
	now := time.Now().UTC()
	campaign, err := domain.NewCampaign(
		"cancelled-archive",
		"GS-CANCEL",
		now.Add(-time.Hour),
		now.Add(time.Hour),
		[]string{"clock-a"},
		domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1},
		"engineer",
		now.Add(-2*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	campaign.State = domain.ReviewApproved
	event := audit.NewEvent(campaign.CampaignID, campaign.Revision, "CREATE", campaign.CreatedBy, "", now.Add(-2*time.Hour))
	if err = store.SaveCampaign(campaign); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveAudit(event); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []domain.ReferenceEvidence{
		{EvidenceID: "clock-ref", CampaignID: campaign.CampaignID, ReferenceKind: "clock", Provider: "lab", CertificateDigest: "aa", ValidFrom: campaign.MissionWindowStart, ValidUntil: campaign.MissionWindowEnd, SubmittedBy: "engineer", SubmittedAt: now.Add(-time.Hour)},
		{EvidenceID: "frequency-ref", CampaignID: campaign.CampaignID, ReferenceKind: "frequency", Provider: "lab", CertificateDigest: "bb", ValidFrom: campaign.MissionWindowStart, ValidUntil: campaign.MissionWindowEnd, SubmittedBy: "engineer", SubmittedAt: now.Add(-time.Hour)},
	} {
		if err = store.SaveReference(ref); err != nil {
			t.Fatal(err)
		}
	}
	round := domain.MeasurementRound{RoundID: "round-1", CampaignID: campaign.CampaignID, Sequence: 1, Purpose: "original", OperatorID: "engineer", CapturedAt: now.Add(-30 * time.Minute), Samples: []domain.Sample{{DeviceID: "clock-a", SampledAt: now.Add(-30 * time.Minute)}}}
	if err = store.SaveRound(round); err != nil {
		t.Fatal(err)
	}
	evaluation := domain.Evaluation{CampaignID: campaign.CampaignID, Revision: campaign.Revision, InputSummary: "input"}
	review := domain.Review{ReviewerID: "reviewer", Approved: true, Statement: "同意签发", Revision: campaign.Revision}
	if err = store.Commit(persistence.Mutation{Campaign: campaign, Evaluation: &evaluation, Review: &review}); err != nil {
		t.Fatal(err)
	}
	triggerDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = triggerDB.Exec(`CREATE TRIGGER refill_block_archive BEFORE INSERT ON artifacts BEGIN SELECT refill_archive_gate(); END`); err != nil {
		t.Fatal(err)
	}
	if err = triggerDB.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, archiveErr := service.ArchiveWithTokenContext(ctx, campaign.CampaignID, campaign.Revision, "")
		result <- archiveErr
	}()
	<-reached
	cancel()
	close(release)
	archiveErr := <-result
	if !errors.Is(archiveErr, context.Canceled) {
		t.Fatalf("archive returned %v after cancellation; want context.Canceled", archiveErr)
	}
	saved, err := store.GetCampaign(campaign.CampaignID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.State != domain.ReviewApproved || saved.Revision != campaign.Revision {
		t.Fatalf("cancelled archive persisted campaign state: state=%s revision=%d", saved.State, saved.Revision)
	}
	if _, err = store.GetArtifact(campaign.CampaignID); err == nil {
		t.Fatal("cancelled archive persisted artifact")
	}
}
