package tracker

import (
	"testing"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

var forecastBase = time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)

func point(position float64, at time.Time) model.PassageEvidence {
	return model.PassageEvidence{
		RunID:      "run-1",
		Station:    "station-a",
		SourceID:   "mag-1",
		Generation: 1,
		Position:   position,
		At:         at,
		Valid:      true,
	}
}

func TestSelectForecastPointsKeepsValidInOrderChain(t *testing.T) {
	evidence := []model.PassageEvidence{
		point(30, forecastBase.Add(60*time.Minute)),
		point(10, forecastBase),
		point(20, forecastBase.Add(30*time.Minute)),
	}
	kept, excluded := SelectForecastPoints(evidence, ForecastMaxGap)
	if excluded != 0 {
		t.Fatalf("expected no exclusions, got %d", excluded)
	}
	if len(kept) != 3 || kept[0].Position != 10 || kept[2].Position != 30 {
		t.Fatalf("expected time-ordered chain of 3, got %+v", kept)
	}
}

func TestSelectForecastPointsDropsOutOfOrderAndInvalid(t *testing.T) {
	regressed := point(15, forecastBase.Add(40*time.Minute))
	invalid := point(25, forecastBase.Add(50*time.Minute))
	invalid.Valid = false
	evidence := []model.PassageEvidence{
		point(10, forecastBase),
		point(20, forecastBase.Add(30*time.Minute)),
		regressed,
		invalid,
		point(30, forecastBase.Add(60*time.Minute)),
	}
	kept, excluded := SelectForecastPoints(evidence, ForecastMaxGap)
	if excluded != 2 {
		t.Fatalf("expected 2 exclusions, got %d", excluded)
	}
	if len(kept) != 3 || kept[2].Position != 30 {
		t.Fatalf("expected chain 10/20/30, got %+v", kept)
	}
}

func TestSelectForecastPointsDropsStaleChainAcrossGap(t *testing.T) {
	evidence := []model.PassageEvidence{
		point(10, forecastBase),
		point(20, forecastBase.Add(30*time.Minute)),
		point(30, forecastBase.Add(4*time.Hour)),
		point(40, forecastBase.Add(4*time.Hour+30*time.Minute)),
	}
	kept, excluded := SelectForecastPoints(evidence, ForecastMaxGap)
	if excluded != 2 {
		t.Fatalf("expected 2 stale exclusions, got %d", excluded)
	}
	if len(kept) != 2 || kept[0].Position != 30 {
		t.Fatalf("expected only the post-gap chain, got %+v", kept)
	}
}

func TestBuildForecastProjectsDownstreamStations(t *testing.T) {
	kept := []model.PassageEvidence{
		point(10, forecastBase),
		point(30, forecastBase.Add(time.Hour)),
	}
	stations := []model.Station{
		{ID: "station-a", Kilometer: 0},
		{ID: "station-b", Kilometer: 86.4},
		{ID: "station-c", Kilometer: 173.2},
	}
	forecast := BuildForecast("run-1", 1, kept, 0, stations, forecastBase.Add(time.Hour))
	if forecast.VelocityKmh != 20 {
		t.Fatalf("expected 20 km/h, got %.3f", forecast.VelocityKmh)
	}
	if len(forecast.Points) != 2 {
		t.Fatalf("expected 2 used points, got %+v", forecast.Points)
	}
	if len(forecast.Arrivals) != 2 {
		t.Fatalf("expected arrivals for the 2 downstream stations, got %+v", forecast.Arrivals)
	}
	if forecast.Arrivals[0].StationID != "station-b" || forecast.Arrivals[1].StationID != "station-c" {
		t.Fatalf("arrivals are not the downstream stations in order: %+v", forecast.Arrivals)
	}
	expectedB := forecastBase.Add(time.Hour).Add(time.Duration(56.4 / 20.0 * float64(time.Hour)))
	if !forecast.Arrivals[0].Expected.Equal(expectedB) {
		t.Fatalf("expected station-b ETA %s, got %s", expectedB, forecast.Arrivals[0].Expected)
	}
	for _, arrival := range forecast.Arrivals {
		if arrival.Earliest.After(arrival.Expected) || arrival.Expected.After(arrival.Latest) {
			t.Fatalf("arrival window is not ordered: %+v", arrival)
		}
	}
	if forecast.Confidence <= 0 || forecast.Confidence > 1 {
		t.Fatalf("confidence out of range: %.3f", forecast.Confidence)
	}
}

func TestBuildForecastWidensWindowWithUnsteadyVelocity(t *testing.T) {
	kept := []model.PassageEvidence{
		point(10, forecastBase),
		point(30, forecastBase.Add(30*time.Minute)), // 40 km/h segment
		point(40, forecastBase.Add(90*time.Minute)), // 10 km/h segment
	}
	stations := []model.Station{{ID: "station-b", Kilometer: 86.4}}
	forecast := BuildForecast("run-1", 1, kept, 0, stations, forecastBase.Add(90*time.Minute))
	if len(forecast.Arrivals) != 1 {
		t.Fatalf("expected one arrival, got %+v", forecast.Arrivals)
	}
	arrival := forecast.Arrivals[0]
	if !arrival.Earliest.Before(arrival.Expected) || !arrival.Expected.Before(arrival.Latest) {
		t.Fatalf("unsteady velocity must widen the window: %+v", arrival)
	}
}

func TestBuildForecastWithoutMovementHasNoArrivals(t *testing.T) {
	kept := []model.PassageEvidence{
		point(10, forecastBase),
		point(10, forecastBase.Add(30*time.Minute)),
	}
	forecast := BuildForecast("run-1", 1, kept, 0, []model.Station{{ID: "station-b", Kilometer: 86.4}}, forecastBase.Add(30*time.Minute))
	if len(forecast.Arrivals) != 0 {
		t.Fatalf("expected no arrivals without forward motion, got %+v", forecast.Arrivals)
	}
	if forecast.Confidence != 0 {
		t.Fatalf("expected zero confidence, got %.3f", forecast.Confidence)
	}
	if len(forecast.Points) != 2 {
		t.Fatalf("used points must stay explicit, got %+v", forecast.Points)
	}
}

func TestRunEvidenceFollowsGeneration(t *testing.T) {
	c := NewCoordinator()
	c.Observe("run-1", "station-a", "mag-1", 10, forecastBase)
	c.Observe("run-1", "station-b", "mag-1", 20, forecastBase.Add(30*time.Minute))
	if got := len(c.RunEvidence("run-1")); got != 2 {
		t.Fatalf("expected 2 current-generation points, got %d", got)
	}
	c.Revise("run-1")
	if got := len(c.RunEvidence("run-1")); got != 0 {
		t.Fatalf("revision must clear forecast input, got %d", got)
	}
	c.Observe("run-1", "station-a", "mag-1", 5, forecastBase.Add(time.Hour))
	evidence := c.RunEvidence("run-1")
	if len(evidence) != 1 || evidence[0].Generation != 2 {
		t.Fatalf("expected one generation-2 point, got %+v", evidence)
	}
}
