package tracker

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

// ForecastMaxGap is the largest silence tolerated between consecutive
// run-level readings before earlier points are treated as a stale,
// disconnected chain. It is deliberately much larger than the per-station
// passage clearance window: along-line readings arrive minutes to hours
// apart, so only a multi-hour silence counts as an obvious gap.
const ForecastMaxGap = 2 * time.Hour

// RunEvidence merges the current-generation evidence recorded for every
// station of a run into one time-ordered sequence.
func (c *Coordinator) RunEvidence(runID string) []model.PassageEvidence {
	c.mu.Lock()
	defer c.mu.Unlock()
	generation := c.generation[runID]
	if generation == 0 {
		generation = 1
	}
	prefix := runID + "/"
	out := make([]model.PassageEvidence, 0)
	for key, points := range c.points {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		for _, e := range points {
			if e.Generation == generation {
				out = append(out, e)
			}
		}
	}
	sortEvidence(out)
	return out
}

func sortEvidence(evidence []model.PassageEvidence) {
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].At.Equal(evidence[j].At) {
			if evidence[i].Position == evidence[j].Position {
				return evidence[i].SourceID < evidence[j].SourceID
			}
			return evidence[i].Position < evidence[j].Position
		}
		return evidence[i].At.Before(evidence[j].At)
	})
}

// SelectForecastPoints reduces raw evidence to the valid points a forecast
// may trust: invalid readings are dropped, readings that move backwards
// relative to the previously kept point are dropped as out-of-order, and
// everything before a gap larger than maxGap is dropped as a stale chain.
// It returns the kept points in time order and the number excluded.
func SelectForecastPoints(evidence []model.PassageEvidence, maxGap time.Duration) (kept []model.PassageEvidence, excluded int) {
	ordered := append([]model.PassageEvidence(nil), evidence...)
	sortEvidence(ordered)
	chain := make([]model.PassageEvidence, 0, len(ordered))
	for _, e := range ordered {
		if !model.ValidEvidence(e) {
			excluded++
			continue
		}
		if len(chain) > 0 && e.Position < chain[len(chain)-1].Position {
			excluded++
			continue
		}
		chain = append(chain, e)
	}
	start := 0
	for i := 1; i < len(chain); i++ {
		if chain[i].At.Sub(chain[i-1].At) > maxGap {
			start = i
		}
	}
	excluded += start
	return append([]model.PassageEvidence(nil), chain[start:]...), excluded
}

// VelocityEstimate summarizes observed speed in km/h.
type VelocityEstimate struct {
	Mean     float64
	Min      float64
	Max      float64
	Segments int
	CV       float64
}

// EstimateVelocity derives the overall speed from the first and last kept
// points and the per-segment spread used for the arrival window. Segments
// with no elapsed time or no forward motion are ignored.
func EstimateVelocity(kept []model.PassageEvidence) (VelocityEstimate, bool) {
	if len(kept) < 2 {
		return VelocityEstimate{}, false
	}
	first := kept[0]
	last := kept[len(kept)-1]
	hours := last.At.Sub(first.At).Hours()
	span := last.Position - first.Position
	if hours <= 0 || span <= 0 {
		return VelocityEstimate{}, false
	}
	estimate := VelocityEstimate{Mean: span / hours, Min: math.MaxFloat64}
	var sum, sumSq float64
	for i := 1; i < len(kept); i++ {
		dt := kept[i].At.Sub(kept[i-1].At).Hours()
		dp := kept[i].Position - kept[i-1].Position
		if dt <= 0 || dp <= 0 {
			continue
		}
		v := dp / dt
		estimate.Segments++
		sum += v
		sumSq += v * v
		if v < estimate.Min {
			estimate.Min = v
		}
		if v > estimate.Max {
			estimate.Max = v
		}
	}
	if estimate.Segments == 0 {
		return VelocityEstimate{}, false
	}
	mean := sum / float64(estimate.Segments)
	if variance := sumSq/float64(estimate.Segments) - mean*mean; variance > 0 && mean > 0 {
		estimate.CV = math.Sqrt(variance) / mean
	}
	return estimate, true
}

// BuildForecast projects arrival windows for every station downstream of the
// last kept point. Stations must belong to the run's route; they are sorted
// by kilometer here. Confidence blends how many points were used, how fresh
// the newest one is, and how steady the observed velocity has been.
func BuildForecast(runID string, generation int64, kept []model.PassageEvidence, excluded int, stations []model.Station, now time.Time) model.TrackingForecast {
	points := make([]model.ForecastPoint, 0, len(kept))
	for _, e := range kept {
		points = append(points, model.ForecastPoint{
			StationID:  e.Station,
			SourceID:   e.SourceID,
			Position:   e.Position,
			ObservedAt: e.At,
		})
	}
	forecast := model.TrackingForecast{
		RunID:      runID,
		Generation: generation,
		Points:     points,
		Excluded:   excluded,
		Arrivals:   make([]model.ArrivalForecast, 0),
		UpdatedAt:  now,
	}
	estimate, ok := EstimateVelocity(kept)
	if !ok {
		return forecast
	}
	last := kept[len(kept)-1]
	forecast.VelocityKmh = math.Round(estimate.Mean*1000) / 1000
	forecast.Confidence = forecastConfidence(len(kept), now.Sub(last.At), ForecastMaxGap, estimate.CV)
	downstream := make([]model.Station, 0, len(stations))
	for _, station := range stations {
		if station.Kilometer > last.Position {
			downstream = append(downstream, station)
		}
	}
	sort.Slice(downstream, func(i, j int) bool {
		if downstream[i].Kilometer == downstream[j].Kilometer {
			return downstream[i].ID < downstream[j].ID
		}
		return downstream[i].Kilometer < downstream[j].Kilometer
	})
	for _, station := range downstream {
		distance := station.Kilometer - last.Position
		forecast.Arrivals = append(forecast.Arrivals, model.ArrivalForecast{
			StationID: station.ID,
			Kilometer: station.Kilometer,
			Earliest:  last.At.Add(travelTime(distance, estimate.Max)),
			Expected:  last.At.Add(travelTime(distance, estimate.Mean)),
			Latest:    last.At.Add(travelTime(distance, estimate.Min)),
		})
	}
	return forecast
}

func travelTime(distance, velocity float64) time.Duration {
	return time.Duration(distance / velocity * float64(time.Hour))
}

func forecastConfidence(points int, age, maxGap time.Duration, cv float64) float64 {
	countScore := float64(points) / 4
	if countScore > 1 {
		countScore = 1
	}
	recencyScore := 1.0
	if age > 0 && maxGap > 0 {
		recencyScore = 1 - float64(age)/float64(maxGap)
		if recencyScore < 0 {
			recencyScore = 0
		}
	}
	consistencyScore := 1 - cv
	if consistencyScore < 0 {
		consistencyScore = 0
	}
	score := 0.4*countScore + 0.3*recencyScore + 0.3*consistencyScore
	return math.Round(score*1000) / 1000
}
