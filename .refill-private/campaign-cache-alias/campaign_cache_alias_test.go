package campaigncachealias

import (
	"ground-clock-qualification/internal/application"
	"ground-clock-qualification/internal/domain"
	"ground-clock-qualification/internal/httpapi"
	"ground-clock-qualification/internal/persistence"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestConcurrentSnapshotRequestsDoNotRaceOnCampaignCache(t *testing.T) {
	store, err := persistence.Open(filepath.Join(t.TempDir(), "campaign.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	service := application.New(store)
	start := time.Date(2031, 4, 5, 6, 0, 0, 0, time.UTC)
	_, err = service.Create(application.CreateInput{
		CampaignID:  "cache-ownership",
		StationCode: "GS-ORIGINAL",
		Start:       start,
		End:         start.Add(2 * time.Hour),
		Devices:     []string{"clock-a", "clock-b"},
		Threshold: domain.ThresholdProfile{
			MaxAbsDeviation:       1,
			MaxFrequencyDeviation: 1,
			MaxDriftSlope:         1,
		},
		By: "engineer-a",
	}, "create-cache-ownership")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(httpapi.New(service).Handler())
	defer server.Close()
	url := server.URL + "/api/v1/campaigns/cache-ownership?include=reviews"
	warm, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	warm.Body.Close()
	if warm.StatusCode != http.StatusOK {
		t.Fatalf("warm snapshot status = %d", warm.StatusCode)
	}

	ready := make(chan struct{}, 2)
	startRequests := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			ready <- struct{}{}
			<-startRequests
			response, requestErr := http.Get(url)
			if requestErr == nil {
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					requestErr = &unexpectedStatus{response.StatusCode}
				}
			}
			results <- requestErr
		}()
	}
	<-ready
	<-ready
	close(startRequests)
	for range 2 {
		if requestErr := <-results; requestErr != nil {
			t.Fatal(requestErr)
		}
	}
}

type unexpectedStatus struct{ code int }

func (e *unexpectedStatus) Error() string { return http.StatusText(e.code) }
