package consistency_material_read_test

import (
	"database/sql"
	"encoding/json"
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	"testing"
	"time"
)

func TestMeasurementConsistencyPropagatesCorruptMaterialReadError(t *testing.T) {
	dbPath := t.TempDir() + "/qualification.db"
	store, err := persistence.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	campaign := &domain.Campaign{
		CampaignID:         "consistency-corrupt-material",
		StationCode:        "GS-READ",
		MissionWindowStart: now,
		MissionWindowEnd:   now.Add(time.Hour),
		DeviceIDs:          []string{"DEVICE-A"},
		Threshold:          domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1},
		State:              domain.Measured,
		Revision:           3,
		CreatedBy:          "engineer",
		CreatedAt:          now.Add(-time.Hour),
	}
	if err = store.SaveCampaign(campaign); err != nil {
		t.Fatal(err)
	}
	round := domain.MeasurementRound{
		RoundID:    "ROUND-1",
		CampaignID: campaign.CampaignID,
		Purpose:    "original",
		OperatorID: "engineer",
		Sequence:   1,
		Samples: []domain.Sample{{
			DeviceID:        "DEVICE-A",
			TimeOffset:      0.2,
			FrequencyOffset: 0.1,
			SampledAt:       now.Add(10 * time.Minute),
		}},
		Environment: map[string]string{"temperature": "20", "humidity": "40", "signal_status": "OK"},
		CapturedAt:  now.Add(10 * time.Minute),
	}
	if err = store.SaveRound(round); err != nil {
		t.Fatal(err)
	}

	void := domain.RoundVoid{CampaignID: campaign.CampaignID, RoundID: round.RoundID, ReasonCode: "BAD_SAMPLE", Reason: "invalid sample", VoidedBy: "engineer", VoidedAt: now.Add(20 * time.Minute), Revision: 2}
	exclusion := domain.SampleExclusion{CampaignID: campaign.CampaignID, RoundID: round.RoundID, DeviceID: "DEVICE-A", ReasonCode: "BAD_SAMPLE", Reason: "invalid sample", ExcludedBy: "engineer", ExcludedAt: now.Add(20 * time.Minute), Revision: 3}
	voidJSON, err := json.Marshal(void)
	if err != nil {
		t.Fatal(err)
	}
	exclusionJSON, err := json.Marshal(exclusion)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO round_voids(campaign_id,round_id,data) VALUES(?,?,?)`, campaign.CampaignID, round.RoundID, voidJSON); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO sample_exclusions(campaign_id,round_id,device_id,data) VALUES(?,?,?,?)`, campaign.CampaignID, round.RoundID, "DEVICE-A", exclusionJSON); err != nil {
		t.Fatal(err)
	}

	service := application.New(store)
	if _, err = db.Exec(`UPDATE round_voids SET data=? WHERE campaign_id=?`, []byte(`{"broken"`), campaign.CampaignID); err != nil {
		t.Fatal(err)
	}
	if _, queryErr := service.MeasurementConsistency(campaign.CampaignID, "", ""); queryErr == nil {
		t.Errorf("round void corruption was not propagated: %v", queryErr)
	}

	if _, err = db.Exec(`UPDATE round_voids SET data=? WHERE campaign_id=?`, voidJSON, campaign.CampaignID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE sample_exclusions SET data=? WHERE campaign_id=?`, []byte(`{"broken"`), campaign.CampaignID); err != nil {
		t.Fatal(err)
	}
	if _, queryErr := service.MeasurementConsistency(campaign.CampaignID, "", ""); queryErr == nil {
		t.Errorf("sample exclusion corruption was not propagated: %v", queryErr)
	}
}
