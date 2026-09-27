package service

import (
	"context"
	"testing"
	"time"

	"github.com/wyw14/solo-036/internal/model"
	"github.com/wyw14/solo-036/internal/tracker"
)

func launchTrackingRun(t *testing.T, r *Runtime) string {
	t.Helper()
	operation, err := r.Launch(context.Background(), LaunchRequest{
		StationID:      "station-a",
		RouteID:        "main-station-a-station-c",
		TargetPressure: 10,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	return operation.Run.ID
}

func trackAt(t *testing.T, r *Runtime, runID string, position float64, at time.Time) model.OperationRecord {
	t.Helper()
	operation, err := r.Track(runID, TrackingRequest{
		StationID:  "station-a",
		SourceID:   "mag-1",
		Kind:       tracker.SourceMagnetic,
		Position:   position,
		ObservedAt: at,
	})
	if err != nil {
		t.Fatalf("track position %v: %v", position, err)
	}
	return operation
}

func TestArrivalForecastRefreshesOnReadings(t *testing.T) {
	r := NewRuntime()
	runID := launchTrackingRun(t, r)

	// A single reading cannot estimate speed yet.
	first := time.Now().UTC().Add(-16 * time.Minute)
	op := trackAt(t, r, runID, 0, first)
	if op.Forecast == nil || op.Forecast.Confidence != string(model.ConfidenceNone) {
		t.Fatalf("expected no confidence after one reading, got %+v", op.Forecast)
	}

	// 40 km/h over 15 minutes with four fresh readings.
	for i, position := range []float64{3.333, 6.667, 10} {
		op = trackAt(t, r, runID, position, first.Add(time.Duration(5*(i+1))*time.Minute))
	}
	forecast := op.Forecast
	if forecast == nil {
		t.Fatal("forecast missing after movement")
	}
	if forecast.Generation != 1 || forecast.PointsUsed != 4 {
		t.Fatalf("expected 4 gen-1 points used, got gen=%d used=%d", forecast.Generation, forecast.PointsUsed)
	}
	if forecast.SpeedKmh < 39 || forecast.SpeedKmh > 41 {
		t.Fatalf("expected ~40 km/h, got %v", forecast.SpeedKmh)
	}
	if forecast.Confidence != string(model.ConfidenceHigh) {
		t.Fatalf("expected high overall confidence, got %s", forecast.Confidence)
	}
	if len(forecast.Windows) != 2 || forecast.Windows[0].StationID != "station-b" || forecast.Windows[1].StationID != "station-c" {
		t.Fatalf("expected windows at station-b/station-c, got %+v", forecast.Windows)
	}
	b := forecast.Windows[0]
	if !b.Earliest.Before(b.Expected) || !b.Latest.After(b.Expected) {
		t.Fatalf("window must bracket the expected ETA: %+v", b)
	}
	if b.Confidence != string(model.ConfidenceLow) {
		t.Fatalf("far horizon must be downgraded, got %s", b.Confidence)
	}

	// Snapshot must surface the same forecast.
	snapshot := r.Snapshot()
	if len(snapshot.Arrivals) != 1 || snapshot.Arrivals[0].RunID != runID {
		t.Fatalf("snapshot arrivals not exposed: %+v", snapshot.Arrivals)
	}
}

func TestArrivalForecastInvalidatedByRevision(t *testing.T) {
	r := NewRuntime()
	runID := launchTrackingRun(t, r)
	first := time.Now().UTC().Add(-11 * time.Minute)
	trackAt(t, r, runID, 0, first)
	trackAt(t, r, runID, 4, first.Add(5*time.Minute))
	trackAt(t, r, runID, 8, first.Add(10*time.Minute))

	before := r.Snapshot().Arrivals
	if len(before) != 1 || before[0].Generation != 1 || len(before[0].Windows) == 0 {
		t.Fatalf("expected a gen-1 forecast with windows, got %+v", before)
	}

	generation, err := r.ReviseTracking(runID)
	if err != nil || generation != 2 {
		t.Fatalf("revision: gen=%d err=%v", generation, err)
	}
	op, err := r.Operation(runID)
	if err != nil {
		t.Fatal(err)
	}
	if op.Forecast.Generation != 2 || len(op.Forecast.Windows) != 0 || op.Forecast.Confidence != string(model.ConfidenceNone) {
		t.Fatalf("revision must reset the forecast, got %+v", op.Forecast)
	}

	// New gen-2 readings rebuild the forecast without touching stale points.
	base := time.Now().UTC().Add(-6 * time.Minute)
	trackAt(t, r, runID, 8, base)
	trackAt(t, r, runID, 12, base.Add(5*time.Minute))
	op, _ = r.Operation(runID)
	if op.Forecast.Generation != 2 || op.Forecast.PointsUsed != 2 || len(op.Forecast.Windows) != 2 {
		t.Fatalf("expected rebuilt gen-2 forecast, got %+v", op.Forecast)
	}
	for _, point := range op.Forecast.Points {
		if point.Generation != 2 {
			t.Fatalf("gen-1 point leaked into gen-2 forecast: %+v", point)
		}
	}
}

func TestArrivalForecastRestoredAfterRecovery(t *testing.T) {
	directory := t.TempDir()
	first := NewRuntime()
	if err := first.ConfigurePersistence(directory); err != nil {
		t.Fatal(err)
	}
	runID := launchTrackingRun(t, first)
	base := time.Now().UTC().Add(-11 * time.Minute)
	trackAt(t, first, runID, 0, base)
	trackAt(t, first, runID, 4, base.Add(5*time.Minute))
	trackAt(t, first, runID, 8, base.Add(10*time.Minute))
	if err := first.PersistSnapshot(); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Simulate a process restart.
	second := NewRuntime()
	if err := second.ConfigurePersistence(directory); err != nil {
		t.Fatal(err)
	}
	if _, err := second.RecoverPersistent(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	op, err := second.Operation(runID)
	if err != nil {
		t.Fatalf("recovered operation: %v", err)
	}
	if op.Forecast == nil || op.Forecast.PointsUsed != 3 || len(op.Forecast.Windows) != 2 {
		t.Fatalf("forecast was not rebuilt after recovery: %+v", op.Forecast)
	}
	if op.Forecast.SpeedKmh < 47 || op.Forecast.SpeedKmh > 49 {
		t.Fatalf("expected ~48 km/h (8 km / 10 min), got %v", op.Forecast.SpeedKmh)
	}
}
