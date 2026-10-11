package signals

import (
	"sync"
	"time"
)

// Cache prefetches an expression over the whole replay window with one
// query_range and serves per-step lookups from memory. A replay steps
// thousands of timestamps; an instant query each would be thousands of
// round trips for data Prometheus can hand over in one.
type Cache struct {
	c     *Client
	start time.Time
	end   time.Time
	step  time.Duration

	mu     sync.Mutex
	frames map[string]map[int64]Vector
}

func NewCache(c *Client, start, end time.Time, step time.Duration) *Cache {
	return &Cache{c: c, start: start, end: end, step: step, frames: map[string]map[int64]Vector{}}
}

// Query returns the samples for expr at t, fetching the whole window on first use.
func (p *Cache) Query(expr string, t time.Time) (Vector, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	f, ok := p.frames[expr]
	if !ok {
		var err error
		f, err = p.c.QueryRange(expr, p.start, p.end, p.step)
		if err != nil {
			return nil, err
		}
		p.frames[expr] = f
	}
	// A step Prometheus had no data for is an empty vector, not an error and
	// not the previous step's value. Callers decide what a gap means.
	return f[t.Unix()], nil
}
