package archiveread

import (
	"database/sql"
	"testing"
	"time"

	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	_ "modernc.org/sqlite"
)

func TestArchivePropagatesMalformedBaselineReadError(t *testing.T) {
	path := t.TempDir() + "/db.sqlite"
	st, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	app := application.New(st)
	now := time.Now().UTC()
	c, err := app.Create(application.CreateInput{CampaignID: "c", StationCode: "S", Start: now, End: now.Add(time.Hour), Devices: []string{"d"}, Threshold: domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1}, By: "u"}, "")
	if err != nil {
		t.Fatal(err)
	}
	c.State = domain.ReviewApproved
	if err = st.SaveCampaign(c); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO device_baselines(campaign_id,device_id,data) VALUES('c','d','not-json')`); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Archive("c", c.Revision); err == nil {
		t.Fatalf("expected archive to fail when baseline material is malformed")
	}
}
