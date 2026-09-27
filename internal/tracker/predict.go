package tracker

import (
	"math"
	"sort"
	"time"

	"github.com/wyw14/solo-036/internal/model"
)

// Defaults governing which readings may anchor an ETA.
const (
	// DefaultMaxGap is the longest silence allowed between two consecutive
	// readings used for one speed estimate. Beyond it the pig may have stopped,
	// so older readings must not be extrapolated across the break.
	DefaultMaxGap = 10 * time.Minute
	// DefaultStaleAge downgrades confidence when no fresh reading has arrived.
	DefaultStaleAge = 30 * time.Minute
	// StationarySpeed is the km/h below which the pig is treated as stopped.
	StationarySpeed = 0.2
	// positionEpsilon treats differences under 5 metres as "same position".
	positionEpsilon = 0.005
)

// ForecastInput carries the current tracking lineage plus the downstream
// station chain (in pipeline order) that ETAs are projected onto.
type ForecastInput struct {
	RunID      string
	RouteID    string
	Generation int64
	Evidence   []model.PassageEvidence
	Stations   []model.Station
	Now        time.Time
	MaxGap     time.Duration
}

// candidate pairs a time-sortable reading with its annotation slot.
type candidate struct {
	evidence model.PassageEvidence
	slot     int
}

// Forecast projects arrival windows at downstream stations from the latest
// continuous chain of readings. Readings that are invalid, belong to another
// generation, move backwards, or sit on the far side of a time gap are
// excluded and reported with a reason.
func Forecast(in ForecastInput) model.ArrivalForecast {
	if in.MaxGap <= 0 {
		in.MaxGap = DefaultMaxGap
	}
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	forecast := model.ArrivalForecast{
		RunID:      in.RunID,
		RouteID:    in.RouteID,
		Generation: in.Generation,
		Confidence: string(model.ConfidenceNone),
		UpdatedAt:  in.Now,
		Points:     make([]model.ArrivalPoint, 0, len(in.Evidence)),
		Windows:    []model.ArrivalWindow{},
	}

	candidates := make([]candidate, 0, len(in.Evidence))
	for _, e := range in.Evidence {
		point := model.ArrivalPoint{
			StationID:  e.Station,
			SourceID:   e.SourceID,
			Position:   e.Position,
			At:         e.At,
			Generation: e.Generation,
		}
		switch {
		case !model.ValidEvidence(e) || e.At.IsZero():
			point.Reason = string(model.RejectInvalid)
		case e.Generation != in.Generation:
			point.Reason = string(model.RejectGeneration)
		default:
			candidates = append(candidates, candidate{evidence: e, slot: len(forecast.Points)})
		}
		forecast.Points = append(forecast.Points, point)
	}

	// Earliest time first; within one instant keep the furthest-downstream
	// reading first so it becomes the group representative.
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].evidence, candidates[j].evidence
		if a.At.Equal(b.At) {
			if a.Position == b.Position {
				return a.SourceID < b.SourceID
			}
			return a.Position > b.Position
		}
		return a.At.Before(b.At)
	})

	// Collapse equal-instant readings: a zero-duration pair cannot estimate
	// speed, so only the furthest position of each instant survives.
	deduped := make([]candidate, 0, len(candidates))
	for i := 0; i < len(candidates); {
		deduped = append(deduped, candidates[i])
		j := i + 1
		for j < len(candidates) && candidates[j].evidence.At.Equal(candidates[i].evidence.At) {
			forecast.Points[candidates[j].slot].Reason = string(model.RejectDuplicate)
			j++
		}
		i = j
	}

	// Walk the time-ordered readings, starting a new chain whenever a gap is
	// found. Only the last contiguous chain anchors the ETA.
	type chain struct{ members []candidate }
	var chains []chain
	current := chain{}
	rejected := make(map[int]model.RejectReason)
	for i, cand := range deduped {
		if len(current.members) == 0 {
			current.members = append(current.members, cand)
			continue
		}
		last := current.members[len(current.members)-1].evidence
		switch {
		case cand.evidence.Position < last.Position-positionEpsilon:
			rejected[i] = model.RejectReordered
		case cand.evidence.Position <= last.Position+positionEpsilon:
			rejected[i] = model.RejectStopped
		case cand.evidence.At.Sub(last.At) > in.MaxGap:
			chains = append(chains, current)
			current = chain{members: []candidate{cand}}
		default:
			current.members = append(current.members, cand)
		}
	}
	if len(current.members) > 0 {
		chains = append(chains, current)
	}

	// Earlier chains are separated from the latest one by a gap and must not
	// be extrapolated across it.
	usedSlots := make(map[int]bool)
	for ci := range chains {
		for _, cand := range chains[ci].members {
			if ci == len(chains)-1 {
				usedSlots[cand.slot] = true
			} else {
				forecast.Points[cand.slot].Reason = string(model.RejectGap)
			}
		}
	}
	for i, reason := range rejected {
		forecast.Points[deduped[i].slot].Reason = string(reason)
	}
	for slot := range usedSlots {
		forecast.Points[slot].Used = true
	}

	chainPoints := make([]model.PassageEvidence, 0)
	if len(chains) > 0 {
		for _, cand := range chains[len(chains)-1].members {
			chainPoints = append(chainPoints, cand.evidence)
		}
	}
	for _, point := range forecast.Points {
		if !point.Used {
			forecast.PointsRejected++
		}
	}
	forecast.PointsUsed = len(chainPoints)

	if len(chainPoints) == 0 {
		forecast.Reason = "no usable readings for the current tracking generation"
		return forecast
	}
	first := chainPoints[0]
	last := chainPoints[len(chainPoints)-1]
	forecast.LastPosition = last.Position
	forecast.LastObservedAt = last.At

	if len(chainPoints) < 2 {
		forecast.Reason = "single reading; waiting for movement confirmation"
		return forecast
	}

	distance := last.Position - first.Position
	span := last.At.Sub(first.At)
	speed := 0.0
	if span > 0 {
		speed = distance / span.Hours()
	}
	if speed <= StationarySpeed {
		forecast.SpeedKmh = speed
		forecast.Reason = "pig is not advancing; arrival time cannot be extrapolated"
		return forecast
	}
	forecast.SpeedKmh = speed

	// Per-segment speeds describe how steady the motion is.
	segmentSpeeds := make([]float64, 0, len(chainPoints)-1)
	for i := 1; i < len(chainPoints); i++ {
		dt := chainPoints[i].At.Sub(chainPoints[i-1].At).Hours()
		dp := chainPoints[i].Position - chainPoints[i-1].Position
		if dt > 0 && dp > 0 {
			segmentSpeeds = append(segmentSpeeds, dp/dt)
		}
	}
	low := percentile(segmentSpeeds, 0.10)
	high := percentile(segmentSpeeds, 0.90)
	baseUncertainty := 0.25
	switch {
	case len(chainPoints) >= 4:
		baseUncertainty = 0.10
	case len(chainPoints) == 3:
		baseUncertainty = 0.15
	}
	spread := 0.0
	if speed > 0 {
		spread = math.Max(high-speed, speed-low) / speed
	}
	uncertainty := math.Max(baseUncertainty, spread)
	speedLow := math.Max(speed*(1-uncertainty), speed*0.5)
	speedHigh := speed * (1 + uncertainty)

	confidence := gradeConfidence(len(chainPoints), span, distance, in.Now.Sub(last.At))
	forecast.Confidence = string(confidence)

	for _, station := range in.Stations {
		remaining := station.Kilometer - last.Position
		if remaining <= positionEpsilon {
			continue
		}
		expectedAt := last.At.Add(durationForDistance(remaining, speed))
		earliest := last.At.Add(durationForDistance(remaining, speedHigh))
		latest := last.At.Add(durationForDistance(remaining, speedLow))
		// A window that exists is, at worst, a weak extrapolation: "none" is
		// reserved for forecasts unable to produce a window at all.
		windowConfidence := downgrade(confidence, horizonSteps(remaining, distance))
		if windowConfidence == model.ConfidenceNone {
			windowConfidence = model.ConfidenceLow
		}
		window := model.ArrivalWindow{
			StationID:   station.ID,
			Kilometer:   station.Kilometer,
			Earliest:    earliest,
			Expected:    expectedAt,
			Latest:      latest,
			Distance:    remaining,
			WindowWidth: latest.Sub(earliest).Seconds(),
			Confidence:  string(windowConfidence),
		}
		forecast.Windows = append(forecast.Windows, window)
	}
	if len(forecast.Windows) == 0 {
		forecast.Reason = "pig has passed every station on the route"
	}
	return forecast
}

func durationForDistance(distance, speedKmh float64) time.Duration {
	if speedKmh <= 0 {
		return 0
	}
	return time.Duration(distance / speedKmh * float64(time.Hour))
}

func horizonSteps(remaining, observedDistance float64) int {
	if observedDistance <= 0 {
		return 2
	}
	switch ratio := remaining / observedDistance; {
	case ratio > 6:
		return 2
	case ratio > 3:
		return 1
	default:
		return 0
	}
}

func gradeConfidence(points int, span time.Duration, distance float64, age time.Duration) model.ConfidenceLevel {
	level := model.ConfidenceLow
	switch {
	case points >= 4 && span >= 10*time.Minute && distance >= 5:
		level = model.ConfidenceHigh
	case (points >= 3 && span >= 2*time.Minute && distance >= 0.5) || distance >= 2:
		level = model.ConfidenceMedium
	}
	if age > DefaultStaleAge {
		level = downgrade(level, 1)
	}
	return level
}

func downgrade(level model.ConfidenceLevel, steps int) model.ConfidenceLevel {
	order := []model.ConfidenceLevel{model.ConfidenceHigh, model.ConfidenceMedium, model.ConfidenceLow, model.ConfidenceNone}
	for i, candidate := range order {
		if candidate == level {
			return order[min(i+steps, len(order)-1)]
		}
	}
	return model.ConfidenceNone
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	position := p * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}
