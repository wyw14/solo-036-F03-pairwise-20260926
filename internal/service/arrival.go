package service

import (
	"time"

	"github.com/wyw14/solo-036/internal/model"
	"github.com/wyw14/solo-036/internal/tracker"
)

// routeForRun returns the route currently leased by the run.
func routeForRun(control *ControlState, runID string) (string, model.RouteState, bool) {
	for routeID, routeState := range control.Routes {
		if routeState.RunID == runID {
			return routeID, routeState, true
		}
	}
	return "", model.RouteState{}, false
}

// downstreamStations returns the station chain of the run's leased route in
// pipeline order.
func downstreamStations(control *ControlState, routeState model.RouteState) []model.Station {
	from, fromOK := control.Stations[routeState.FromStation]
	to, toOK := control.Stations[routeState.ToStation]
	if !fromOK || !toOK {
		return nil
	}
	limit := from.PressureLimit
	if to.PressureLimit < limit {
		limit = to.PressureLimit
	}
	path, err := control.Network.Path(routeState.FromStation, routeState.ToStation, limit)
	if err != nil {
		return nil
	}
	stations := make([]model.Station, 0, len(path.Stations))
	for _, stationID := range path.Stations {
		if station, ok := control.Stations[stationID]; ok {
			stations = append(stations, station)
		}
	}
	return stations
}

// computeForecast rebuilds the arrival forecast for one run from the readings
// currently held by the tracker. Callers must hold the runtime mutex.
func (r *Runtime) computeForecast(runID string, now time.Time) (model.ArrivalForecast, bool) {
	control := r.control
	routeID, routeState, ok := routeForRun(control, runID)
	if !ok {
		return model.ArrivalForecast{}, false
	}
	generation := r.Tracker.Generation(runID)
	evidence := r.Tracker.EvidenceForRun(runID, generation)
	forecast := tracker.Forecast(tracker.ForecastInput{
		RunID:      runID,
		RouteID:    routeID,
		Generation: generation,
		Evidence:   evidence,
		Stations:   downstreamStations(control, routeState),
		Now:        now,
		MaxGap:     tracker.DefaultMaxGap,
	})
	return forecast, true
}

// refreshArrival recomputes and stores one run's forecast. It is invoked after
// every accepted reading and whenever the tracking generation changes.
func (r *Runtime) refreshArrival(runID string) {
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.control == nil {
		return
	}
	forecast, ok := r.computeForecast(runID, now)
	if !ok {
		delete(r.control.Arrivals, runID)
		return
	}
	if r.control.Arrivals == nil {
		r.control.Arrivals = make(map[string]model.ArrivalForecast)
	}
	r.control.Arrivals[runID] = forecast
	r.control.Revision = model.NewRevision()
}

// refreshAllArrivals rebuilds every forecast, e.g. after persistence recovery.
func (r *Runtime) refreshAllArrivals() {
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.control == nil {
		return
	}
	r.control.Arrivals = make(map[string]model.ArrivalForecast)
	for _, run := range r.Runs.Snapshot() {
		if forecast, ok := r.computeForecast(run.ID, now); ok {
			r.control.Arrivals[run.ID] = forecast
		}
	}
	r.control.Revision = model.NewRevision()
}
