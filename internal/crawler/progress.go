package crawler

import "time"

// Progress describes the latest user-requested refresh. Scheduled polling can
// finish feeds in this batch too; repeated requests join the same batch.
type Progress struct {
	ID         uint64     `json:"id"`
	Running    bool       `json:"running"`
	Total      int        `json:"total"`
	Completed  int        `json:"completed"`
	Inserted   int        `json:"inserted"`
	Failed     int        `json:"failed"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

func (c *Crawler) Progress() Progress {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.progress
}

func (c *Crawler) StartRefresh() (Progress, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.progress.Running {
		return c.progress, nil
	}
	feeds, err := c.store.ListFeeds()
	if err != nil {
		return Progress{}, err
	}
	if err := c.store.ScheduleAll(); err != nil {
		return Progress{}, err
	}
	c.pending = make(map[int64]bool)
	for _, f := range feeds {
		if !f.Paused && !f.Unsubscribed {
			c.pending[f.ID] = true
		}
	}
	c.progress = Progress{ID: c.progress.ID + 1, Running: len(c.pending) > 0, Total: len(c.pending), StartedAt: time.Now().UTC()}
	if !c.progress.Running {
		now := time.Now().UTC()
		c.progress.FinishedAt = &now
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return c.progress, nil
}

func (c *Crawler) complete(ev Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pending[ev.FeedID] {
		return
	}
	delete(c.pending, ev.FeedID)
	c.progress.Completed++
	c.progress.Inserted += ev.Inserted
	if ev.Error != "" {
		c.progress.Failed++
	}
	if len(c.pending) == 0 {
		c.progress.Running = false
		now := time.Now().UTC()
		c.progress.FinishedAt = &now
	}
}
