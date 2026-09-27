package route

// Reset clears the tracked progress high-water mark for a run that still
// holds its route lease. Used when tracking is revised: the discarded
// readings must not keep rejecting lower positions from the new generation.
func (c *Coordinator) Reset(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, lease := range c.owners {
		if lease.RunID == runID {
			delete(c.progress, runID)
			return
		}
	}
}
