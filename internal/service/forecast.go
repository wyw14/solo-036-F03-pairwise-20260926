package service

import (
	"time"

	"github.com/wyw14/solo-036/internal/model"
	"github.com/wyw14/solo-036/internal/tracker"
)

// trackingForecast recomputes the arrival forecast for a run from the
// current-generation tracking evidence. It returns false when the run is not
// actively moving along a reserved route or has no usable readings yet —
// notably right after a tracking revision, which clears the evidence and
// resets the forecast until new readings arrive.
func (r *Runtime) trackingForecast(runID string, snapshot model.ControlSnapshot) (model.TrackingForecast, bool) {
	current, ok := findRun(snapshot, runID)
	if !ok || (current.State != model.StateLaunched && current.State != model.StateTracking) {
		return model.TrackingForecast{}, false
	}
	routeState, ok := findRoute(snapshot, runID)
	if !ok {
		return model.TrackingForecast{}, false
	}
	control := r.ensureControl()
	from, fromOK := control.Stations[routeState.FromStation]
	to, toOK := control.Stations[routeState.ToStation]
	if !fromOK || !toOK {
		return model.TrackingForecast{}, false
	}
	pressure := from.PressureLimit
	if to.PressureLimit < pressure {
		pressure = to.PressureLimit
	}
	path, err := control.Network.Path(routeState.FromStation, routeState.ToStation, pressure)
	if err != nil {
		return model.TrackingForecast{}, false
	}
	stations := make([]model.Station, 0, len(path.Stations))
	for _, stationID := range path.Stations {
		if station, ok := control.Stations[stationID]; ok {
			stations = append(stations, station)
		}
	}
	evidence := r.Tracker.RunEvidence(runID)
	if len(evidence) == 0 {
		return model.TrackingForecast{}, false
	}
	kept, excluded := tracker.SelectForecastPoints(evidence, tracker.ForecastMaxGap)
	if len(kept) == 0 {
		return model.TrackingForecast{}, false
	}
	forecast := tracker.BuildForecast(runID, r.Tracker.Generation(runID), kept, excluded, stations, time.Now().UTC())
	return forecast, true
}

func findRun(snapshot model.ControlSnapshot, runID string) (model.Run, bool) {
	for _, candidate := range snapshot.Runs {
		if candidate.ID == runID {
			return candidate, true
		}
	}
	return model.Run{}, false
}

func findRoute(snapshot model.ControlSnapshot, runID string) (model.RouteState, bool) {
	for _, candidate := range snapshot.Routes {
		if candidate.Reserved && candidate.RunID == runID {
			return candidate, true
		}
	}
	return model.RouteState{}, false
}
