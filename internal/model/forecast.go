package model

import "time"

// ForecastPoint is one valid tracking reading that fed an arrival forecast.
type ForecastPoint struct {
	StationID  string    `json:"station_id"`
	SourceID   string    `json:"source_id"`
	Position   float64   `json:"position"`
	ObservedAt time.Time `json:"observed_at"`
}

// ArrivalForecast is the predicted arrival window for one downstream station.
type ArrivalForecast struct {
	StationID string    `json:"station_id"`
	Kilometer float64   `json:"kilometer"`
	Earliest  time.Time `json:"earliest"`
	Expected  time.Time `json:"expected"`
	Latest    time.Time `json:"latest"`
}

// TrackingForecast summarizes the velocity estimate behind the arrival
// windows: which valid points were used, how many readings were excluded as
// out-of-order or across a gap, and how confident the estimate is (0..1).
type TrackingForecast struct {
	RunID       string            `json:"run_id"`
	Generation  int64             `json:"generation"`
	VelocityKmh float64           `json:"velocity_kmh"`
	Confidence  float64           `json:"confidence"`
	Points      []ForecastPoint   `json:"points"`
	Excluded    int               `json:"excluded"`
	Arrivals    []ArrivalForecast `json:"arrivals"`
	UpdatedAt   time.Time         `json:"updated_at"`
}
