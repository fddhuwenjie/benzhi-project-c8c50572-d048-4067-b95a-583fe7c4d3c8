package malformedroundread

import (
	"database/sql"
	"testing"
	"time"

	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/persistence"
	_ "modernc.org/sqlite"
)

func TestMalformedRoundRowPropagatesReadError(t *testing.T) {
	path := t.TempDir() + "/db.sqlite"
	st, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	app := application.New(st)
	now := time.Now().UTC()
	if _, err = app.Create(application.CreateInput{CampaignID: "c", StationCode: "S", Start: now, End: now.Add(time.Hour), Devices: []string{"d"}, Threshold: domain.ThresholdProfile{MaxAbsDeviation: 1, MaxFrequencyDeviation: 1, MaxDriftSlope: 1}, By: "u"}, ""); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO refs(id,campaign_id,data) VALUES('bad-ref','c','not-json'); INSERT INTO rounds(id,campaign_id,seq,data) VALUES('bad-round','c',1,'not-json'); INSERT INTO deviations(id,campaign_id,data) VALUES('bad-dev','c','not-json'); INSERT INTO remediation_plans(campaign_id,deviation_id,version,data) VALUES('c','bad-dev',1,'not-json')`); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Snapshot("c", "all"); err == nil {
		t.Fatalf("expected malformed persisted evidence JSON read error")
	}
}
