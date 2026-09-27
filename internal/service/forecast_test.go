package service

import (
	"context"
	"testing"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

func launchTrackingRun(t *testing.T) *Runtime {
	t.Helper()
	r := NewRuntime()
	operation, err := r.Launch(context.Background(), LaunchRequest{
		StationID:      "station-a",
		RouteID:        "main-station-a-station-c",
		TargetPressure: 10,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if operation.Run.ID == "" {
		t.Fatal("launch returned no run id")
	}
	return r
}

func trackAt(t *testing.T, r *Runtime, runID string, position float64, at time.Time) model.OperationRecord {
	t.Helper()
	operation, err := r.Track(runID, TrackingRequest{
		StationID:  "station-a",
		SourceID:   "mag-1",
		Position:   position,
		ObservedAt: at,
	})
	if err != nil {
		t.Fatalf("track %.1f: %v", position, err)
	}
	return operation
}

func TestOperationCarriesArrivalForecast(t *testing.T) {
	r := launchTrackingRun(t)
	runID := r.Operations()[0].ID
	base := time.Now().UTC().Add(-time.Hour)
	trackAt(t, r, runID, 10, base)
	operation := trackAt(t, r, runID, 30, base.Add(time.Hour))
	if operation.Forecast == nil {
		t.Fatal("run page must include the arrival forecast")
	}
	forecast := operation.Forecast
	if forecast.VelocityKmh != 20 {
		t.Fatalf("expected 20 km/h, got %.3f", forecast.VelocityKmh)
	}
	if len(forecast.Points) != 2 {
		t.Fatalf("forecast must list the valid points used, got %+v", forecast.Points)
	}
	if len(forecast.Arrivals) != 2 || forecast.Arrivals[0].StationID != "station-b" || forecast.Arrivals[1].StationID != "station-c" {
		t.Fatalf("expected ETAs for station-b and station-c, got %+v", forecast.Arrivals)
	}
	if forecast.Confidence <= 0 {
		t.Fatalf("expected positive confidence, got %.3f", forecast.Confidence)
	}
}

func TestForecastExcludesRegressedReading(t *testing.T) {
	r := launchTrackingRun(t)
	runID := r.Operations()[0].ID
	base := time.Now().UTC().Add(-time.Hour)
	trackAt(t, r, runID, 10, base)
	trackAt(t, r, runID, 30, base.Add(time.Hour))
	if _, err := r.Track(runID, TrackingRequest{
		StationID:  "station-a",
		SourceID:   "mag-1",
		Position:   5,
		ObservedAt: base.Add(90 * time.Minute),
	}); err == nil {
		t.Fatal("regressed position must be rejected by the flow")
	}
	operation, err := r.Operation(runID)
	if err != nil {
		t.Fatalf("operation: %v", err)
	}
	if operation.Forecast == nil {
		t.Fatal("forecast must survive a rejected reading")
	}
	if operation.Forecast.Excluded != 1 {
		t.Fatalf("expected the regressed point to be excluded, got %+v", operation.Forecast)
	}
	if len(operation.Forecast.Points) != 2 {
		t.Fatalf("regressed point must not feed the forecast, got %+v", operation.Forecast.Points)
	}
}

func TestForecastResetsAfterTrackingRevision(t *testing.T) {
	r := launchTrackingRun(t)
	runID := r.Operations()[0].ID
	base := time.Now().UTC().Add(-time.Hour)
	trackAt(t, r, runID, 10, base)
	trackAt(t, r, runID, 30, base.Add(time.Hour))
	if _, err := r.ReviseTracking(runID); err != nil {
		t.Fatalf("revise: %v", err)
	}
	operation, err := r.Operation(runID)
	if err != nil {
		t.Fatalf("operation: %v", err)
	}
	if operation.Forecast != nil {
		t.Fatalf("revision must reset the forecast, got %+v", operation.Forecast)
	}
	operation = trackAt(t, r, runID, 12, base.Add(2*time.Hour))
	if operation.Forecast == nil {
		t.Fatal("forecast must rebuild from new-generation readings")
	}
	if operation.Forecast.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", operation.Forecast.Generation)
	}
	if len(operation.Forecast.Arrivals) != 0 {
		t.Fatalf("one point cannot project arrivals, got %+v", operation.Forecast.Arrivals)
	}
	if len(operation.Forecast.Points) != 1 {
		t.Fatalf("the single new reading must be listed, got %+v", operation.Forecast.Points)
	}
}
