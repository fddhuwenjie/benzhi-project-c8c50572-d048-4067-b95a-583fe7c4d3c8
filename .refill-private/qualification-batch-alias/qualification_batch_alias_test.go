package qualification_batch_alias_test

import (
	"reflect"
	"testing"
	"time"

	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/audit"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
)

func TestQualificationBatchPreservesCallerOwnedInput(t *testing.T) {
	store, err := persistence.Open(t.TempDir() + "/qualification.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	start := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	end := start.Add(8 * time.Hour)
	campaign := domain.Campaign{
		CampaignID:         "QUALIFICATION-SEALED",
		StationCode:        "GS-Q",
		MissionWindowStart: start,
		MissionWindowEnd:   end,
		DeviceIDs:          []string{"DEVICE-A", "DEVICE-B"},
		State:              domain.Archived,
		Revision:           1,
		CreatedBy:          "engineer",
		CreatedAt:          start.Add(-time.Hour),
	}
	event := audit.NewEvent(campaign.CampaignID, campaign.Revision, "ARCHIVE", "reviewer", "", start)
	artifact, err := audit.ArtifactWithSections(audit.ArtifactSource{
		Campaign: &campaign,
		Events:   []audit.Event{event},
	}, "reviewer", event.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCampaign(&campaign); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveAudit(event); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveArtifact(artifact); err != nil {
		t.Fatal(err)
	}

	zone := time.FixedZone("UTC+8", 8*60*60)
	input := application.QualificationBatchInput{Checks: []application.QualificationBatchCheck{
		{
			QueryID:     " later-window ",
			StationCode: " GS-Q ",
			WindowStart: start.Add(4 * time.Hour).In(zone),
			WindowEnd:   start.Add(5 * time.Hour).In(zone),
			DeviceIDs:   []string{" DEVICE-B ", " DEVICE-A "},
		},
		{
			QueryID:     " earlier-window ",
			StationCode: " GS-Q ",
			WindowStart: start.Add(time.Hour).In(zone),
			WindowEnd:   start.Add(2 * time.Hour).In(zone),
			DeviceIDs:   []string{" DEVICE-B ", " DEVICE-A "},
		},
	}}
	before := cloneBatchInput(input)

	result, err := application.New(store).QualificationBatch(campaign.CampaignID, input)
	if err != nil {
		t.Fatalf("qualification batch failed: %v", err)
	}
	if len(result.Checks) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatalf("QualificationBatch mutated caller-owned input\nbefore: %#v\nafter:  %#v", before, input)
	}
}

func cloneBatchInput(in application.QualificationBatchInput) application.QualificationBatchInput {
	out := application.QualificationBatchInput{Checks: append([]application.QualificationBatchCheck(nil), in.Checks...)}
	for i := range out.Checks {
		out.Checks[i].DeviceIDs = append([]string(nil), in.Checks[i].DeviceIDs...)
	}
	return out
}
