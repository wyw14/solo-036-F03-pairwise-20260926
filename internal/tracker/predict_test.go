package tracker

import (
	"testing"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

func evidence(station, source string, generation int64, position float64, at time.Time) model.PassageEvidence {
	return model.PassageEvidence{
		RunID:      "run-1",
		Station:    station,
		SourceID:   source,
		Generation: generation,
		Position:   position,
		At:         at,
		Valid:      true,
	}
}

func defaultStations() []model.Station {
	return []model.Station{
		{ID: "station-a", Role: model.StationLauncher, Kilometer: 0, PressureLimit: 12},
		{ID: "station-b", Role: model.StationIntermediate, Kilometer: 86.4, PressureLimit: 12},
		{ID: "station-c", Role: model.StationReceiver, Kilometer: 173.2, PressureLimit: 12},
	}
}

func TestForecastRejectsStaleGenerationAndInvalid(t *testing.T) {
	base := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	valid := evidence("station-a", "mag-1", 2, 10, base)
	old := evidence("station-a", "mag-1", 1, 5, base.Add(-time.Hour))
	bad := evidence("station-a", "mag-2", 2, 11, base.Add(time.Minute))
	bad.Valid = false

	forecast := Forecast(ForecastInput{
		RunID: "run-1", Generation: 2, Now: base.Add(time.Minute),
		Evidence: []model.PassageEvidence{old, valid, bad},
		Stations: defaultStations(),
	})
	if forecast.PointsUsed != 1 {
		t.Fatalf("expected only one usable point, got %d", forecast.PointsUsed)
	}
	reasons := map[string]int{}
	for _, p := range forecast.Points {
		reasons[p.Reason]++
	}
	if reasons[string(model.RejectGeneration)] != 1 || reasons[string(model.RejectInvalid)] != 1 {
		t.Fatalf("expected generation and invalid rejections, got %+v", reasons)
	}
	if forecast.Confidence != string(model.ConfidenceNone) || len(forecast.Windows) != 0 {
		t.Fatalf("single point must not produce windows, confidence=%s windows=%d", forecast.Confidence, len(forecast.Windows))
	}
}

func TestForecastProjectsArrivalFromContinuousChain(t *testing.T) {
	base := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	// 40 km/h progression: 0 -> 20 -> 40 km every 30 minutes.
	points := []model.PassageEvidence{
		evidence("station-a", "mag-1", 1, 0, base),
		evidence("station-a", "mag-2", 1, 20, base.Add(30*time.Minute)),
		evidence("station-b", "mag-1", 1, 40, base.Add(60*time.Minute)),
	}
	forecast := Forecast(ForecastInput{
		RunID: "run-1", Generation: 1, Now: base.Add(61 * time.Minute),
		Evidence: points, Stations: defaultStations(), MaxGap: time.Hour,
	})
	if forecast.PointsUsed != 3 {
		t.Fatalf("expected 3 used points, got %d", forecast.PointsUsed)
	}
	if forecast.PointsRejected != 0 {
		t.Fatalf("expected no rejected points, got %d", forecast.PointsRejected)
	}
	if got := forecast.SpeedKmh; got < 39.9 || got > 40.1 {
		t.Fatalf("expected ~40 km/h, got %v", got)
	}
	if len(forecast.Windows) != 2 {
		t.Fatalf("expected windows at station-b and station-c, got %d", len(forecast.Windows))
	}
	windowB := forecast.Windows[0]
	if windowB.StationID != "station-b" {
		t.Fatalf("first window must target station-b, got %s", windowB.StationID)
	}
	// 46.4 km remaining at 40 km/h = 69.6 minutes after the last reading.
	expectedB := base.Add(60*time.Minute + 70*time.Minute)
	if diff := windowB.Expected.Sub(expectedB); diff > 30*time.Second || diff < -30*time.Second {
		t.Fatalf("station-b ETA off by %v", diff)
	}
	if !windowB.Earliest.Before(windowB.Expected) || !windowB.Latest.After(windowB.Expected) {
		t.Fatalf("expected earliest..expected..latest ordering: %+v", windowB)
	}
	if windowB.WindowWidth <= 0 {
		t.Fatalf("expected positive window width")
	}
}

func TestForecastRejectsReorderedReadings(t *testing.T) {
	base := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	points := []model.PassageEvidence{
		evidence("station-a", "mag-1", 1, 20, base),
		evidence("station-a", "mag-2", 1, 40, base.Add(10*time.Minute)),
		// backwards position after a later timestamp: cannot be real motion.
		evidence("station-b", "mag-1", 1, 35, base.Add(15*time.Minute)),
		evidence("station-b", "mag-2", 1, 45, base.Add(20*time.Minute)),
	}
	forecast := Forecast(ForecastInput{
		RunID: "run-1", Generation: 1, Now: base.Add(21 * time.Minute),
		Evidence: points, Stations: defaultStations(), MaxGap: time.Hour,
	})
	var reordered int
	for _, p := range forecast.Points {
		if p.Reason == string(model.RejectReordered) {
			reordered++
		}
	}
	if reordered != 1 {
		t.Fatalf("expected one reordered rejection, got %d", reordered)
	}
	// Last chain: 20, 40, 45 — the 35 reading never anchors the forecast.
	if forecast.LastPosition != 45 {
		t.Fatalf("expected last position 45, got %v", forecast.LastPosition)
	}
	if forecast.PointsUsed != 3 {
		t.Fatalf("expected 3 points in the final chain, got %d", forecast.PointsUsed)
	}
}

func TestForecastSplitsChainOnTimeGap(t *testing.T) {
	base := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	points := []model.PassageEvidence{
		evidence("station-a", "mag-1", 1, 0, base),
		evidence("station-a", "mag-2", 1, 10, base.Add(15*time.Minute)),
		// gap longer than DefaultMaxGap: older readings must not anchor ETA.
		evidence("station-b", "mag-1", 1, 50, base.Add(2*time.Hour)),
		evidence("station-b", "mag-2", 1, 60, base.Add(2*time.Hour+15*time.Minute)),
	}
	forecast := Forecast(ForecastInput{
		RunID: "run-1", Generation: 1, Now: base.Add(2*time.Hour + 16*time.Minute),
		Evidence: points, Stations: defaultStations(), MaxGap: time.Hour,
	})
	if forecast.PointsUsed != 2 {
		t.Fatalf("expected only the post-gap chain of 2 points to be used, got %d", forecast.PointsUsed)
	}
	gapRejections := 0
	for _, p := range forecast.Points {
		if p.Reason == string(model.RejectGap) {
			gapRejections++
		}
	}
	if gapRejections != 2 {
		t.Fatalf("expected 2 gap rejections, got %d", gapRejections)
	}
	// Speed comes solely from 50 -> 60 km over 15 minutes = 40 km/h,
	// never from the pre-gap speed of 40 km/h over the full span.
	if got := forecast.SpeedKmh; got < 39.9 || got > 40.1 {
		t.Fatalf("expected post-gap speed 40 km/h, got %v", got)
	}
}

func TestForecastStoppedPigHasNoETA(t *testing.T) {
	base := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	// Advances 20 metres over 20 minutes -> 0.06 km/h, below the stop threshold.
	points := []model.PassageEvidence{
		evidence("station-a", "mag-1", 1, 20, base),
		evidence("station-a", "mag-2", 1, 20.01, base.Add(10*time.Minute)),
		evidence("station-a", "mag-1", 1, 20.02, base.Add(20*time.Minute)),
	}
	forecast := Forecast(ForecastInput{
		RunID: "run-1", Generation: 1, Now: base.Add(21 * time.Minute),
		Evidence: points, Stations: defaultStations(), MaxGap: time.Hour,
	})
	if len(forecast.Windows) != 0 || forecast.Confidence != string(model.ConfidenceNone) {
		t.Fatalf("stopped pig must not get an ETA, got windows=%d confidence=%s", len(forecast.Windows), forecast.Confidence)
	}
}

func TestForecastDropsConcurrentReadings(t *testing.T) {
	base := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	points := []model.PassageEvidence{
		evidence("station-a", "mag-1", 1, 10, base),
		evidence("station-a", "mag-9", 1, 12, base), // same instant, furthest wins
		evidence("station-b", "mag-1", 1, 30, base.Add(30*time.Minute)),
	}
	forecast := Forecast(ForecastInput{
		RunID: "run-1", Generation: 1, Now: base.Add(31 * time.Minute),
		Evidence: points, Stations: defaultStations(), MaxGap: time.Hour,
	})
	if forecast.PointsUsed != 2 || forecast.PointsRejected != 1 {
		t.Fatalf("expected 2 used / 1 rejected, got used=%d rejected=%d", forecast.PointsUsed, forecast.PointsRejected)
	}
	if forecast.LastPosition != 30 {
		t.Fatalf("unexpected last position %v", forecast.LastPosition)
	}
}

func TestForecastEmptyInput(t *testing.T) {
	forecast := Forecast(ForecastInput{
		RunID: "run-1", Generation: 1,
		Stations: defaultStations(),
	})
	if forecast.Confidence != string(model.ConfidenceNone) || len(forecast.Windows) != 0 {
		t.Fatalf("empty forecast must report none confidence and no windows")
	}
}
