package cancelledcreatecommit_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/httpapi"
	"ground-clock-qualification/internal/persistence"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCancelledCreateDoesNotPersistCampaign(t *testing.T) {
	store, err := persistence.Open(t.TempDir() + "/cancelled-create.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	body := []byte(`{
		"campaign_id":"cancelled-create",
		"station_code":"GS-CANCEL",
		"start":"2032-03-01T00:00:00Z",
		"end":"2032-03-01T01:00:00Z",
		"created_by":"operator-cancelled",
		"device_ids":["clock-cancelled"],
		"threshold":{"max_abs_deviation":1,"max_frequency_deviation":1,"max_drift_slope":1}
	}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/campaigns", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "cancelled-create-request")
	recorder := httptest.NewRecorder()

	httpapi.New(application.New(store)).Handler().ServeHTTP(recorder, req)

	_, err = store.GetCampaign("cancelled-create")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("canceled request persisted campaign: status=%d lookup_error=%v", recorder.Code, err)
	}
}
