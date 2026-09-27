package tracker

import (
	"github.com/wyw14/solo-036/internal/model"
	"sort"
	"time"
)

func (c *Coordinator) Observe(runID, station, source string, position float64, at time.Time) model.PassageEvidence {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation[runID] == 0 {
		c.generation[runID] = 1
	}
	e := model.PassageEvidence{RunID: runID, Station: station, SourceID: source, Generation: c.generation[runID], Position: position, At: at, Valid: true}
	key := runID + "/" + station
	c.points[key] = append(c.points[key], e)
	if c.sources[key] == nil {
		c.sources[key] = make(map[string]bool)
	}
	c.sources[key][source] = true
	return e
}
func (c *Coordinator) Revise(runID string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation[runID]++
	for key := range c.points {
		if len(key) >= len(runID) && key[:len(runID)] == runID {
			delete(c.points, key)
			delete(c.sources, key)
		}
	}
	return c.generation[runID]
}
func (c *Coordinator) Generation(runID string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation[runID] == 0 {
		c.generation[runID] = 1
	}
	return c.generation[runID]
}

// SeedGeneration records the current tracking generation for a run without
// creating evidence. It keeps arrival forecasts tied to the right generation
// immediately after a revision, before any new reading arrives.
func (c *Coordinator) SeedGeneration(runID string, generation int64) {
	if generation <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if current := c.generation[runID]; current == 0 || current < generation {
		c.generation[runID] = generation
	}
}

// EvidenceForRun returns every stored reading of one run that belongs to the
// given tracking generation, sorted by observation time.
func (c *Coordinator) EvidenceForRun(runID string, generation int64) []model.PassageEvidence {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := runID + "/"
	out := make([]model.PassageEvidence, 0)
	for key, points := range c.points {
		if len(key) < len(prefix) || key[:len(prefix)] != prefix {
			continue
		}
		for _, point := range points {
			if point.Generation == generation {
				out = append(out, point)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			if out[i].Position == out[j].Position {
				return out[i].SourceID < out[j].SourceID
			}
			return out[i].Position < out[j].Position
		}
		return out[i].At.Before(out[j].At)
	})
	return out
}
func (c *Coordinator) Evidence(runID, station string) []model.PassageEvidence {
	c.mu.Lock()
	defer c.mu.Unlock()
	in := c.points[runID+"/"+station]
	out := append([]model.PassageEvidence(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Restore rebuilds stored readings after recovery. Only readings of the
// supplied generation are kept, since older generations were superseded.
func (c *Coordinator) Restore(runID string, generation int64, evidence []model.PassageEvidence) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation > c.generation[runID] {
		c.generation[runID] = generation
	}
	for _, e := range evidence {
		if e.Generation != generation {
			continue
		}
		key := runID + "/" + e.Station
		c.points[key] = append(c.points[key], e)
	}
}
