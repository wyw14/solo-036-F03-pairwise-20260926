package model

import "time"

// ConfidenceLevel describes how trustworthy an arrival forecast is.
type ConfidenceLevel string

const (
	// ConfidenceNone: no usable pair of readings, no ETA can be produced.
	ConfidenceNone ConfidenceLevel = "none"
	// ConfidenceLow: exactly one usable pair, or the observation window is short.
	ConfidenceLow ConfidenceLevel = "low"
	// ConfidenceMedium: several points agree over a meaningful distance.
	ConfidenceMedium ConfidenceLevel = "medium"
	// ConfidenceHigh: many tightly spaced points agree over a long distance.
	ConfidenceHigh ConfidenceLevel = "high"
)

func (c ConfidenceLevel) Valid() bool {
	switch c {
	case ConfidenceNone, ConfidenceLow, ConfidenceMedium, ConfidenceHigh:
		return true
	default:
		return false
	}
}

// RejectReason explains why a recorded reading was excluded from a forecast.
type RejectReason string

const (
	RejectInvalid    RejectReason = "invalid"
	RejectGeneration RejectReason = "generation_mismatch"
	RejectReordered  RejectReason = "position_reordered"
	RejectDuplicate  RejectReason = "duplicate_instant"
	RejectStale      RejectReason = "stale_timestamp"
	RejectGap        RejectReason = "time_gap"
	RejectStopped    RejectReason = "not_advancing"
)

// ArrivalPoint is one reading considered when building a forecast.
type ArrivalPoint struct {
	StationID  string    `json:"station_id"`
	SourceID   string    `json:"source_id"`
	Position   float64   `json:"position"`
	At         time.Time `json:"at"`
	Generation int64     `json:"generation"`
	Used       bool      `json:"used"`
	Reason     string    `json:"reason,omitempty"`
}

// ArrivalWindow is the predicted arrival interval at one downstream station.
type ArrivalWindow struct {
	StationID   string    `json:"station_id"`
	Kilometer   float64   `json:"kilometer"`
	Earliest    time.Time `json:"earliest"`
	Expected    time.Time `json:"expected"`
	Latest      time.Time `json:"latest"`
	Confidence  string    `json:"confidence"`
	Distance    float64   `json:"distance_km"`
	WindowWidth float64   `json:"window_seconds"`
}

// ArrivalForecast is the arrival prediction for a run, rebuilt on every new
// reading and whenever the tracking generation changes.
type ArrivalForecast struct {
	RunID          string          `json:"run_id"`
	RouteID        string          `json:"route_id,omitempty"`
	Generation     int64           `json:"generation"`
	Confidence     string          `json:"confidence"`
	SpeedKmh       float64         `json:"speed_kmh,omitempty"`
	LastPosition   float64         `json:"last_position_km"`
	LastObservedAt time.Time       `json:"last_observed_at"`
	PointsUsed     int             `json:"points_used"`
	PointsRejected int             `json:"points_rejected"`
	Points         []ArrivalPoint  `json:"points,omitempty"`
	Windows        []ArrivalWindow `json:"windows"`
	Reason         string          `json:"reason,omitempty"`
	UpdatedAt      time.Time       `json:"updated_at"`
}
