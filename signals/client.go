// Package signals is the Prometheus data path. It is internal plumbing, not a
// model-facing interface: in Kepler the equivalent code reads /proc and cgroups.
// Models never see this package; they see resource.Node.
package signals

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Sample struct {
	Labels map[string]string
	Value  float64
}

type Vector []Sample

type Client struct {
	base string
	http *http.Client
}

func New(base string) *Client {
	return &Client{base: base, http: &http.Client{Timeout: 60 * time.Second}}
}

// promResponse covers both the vector and matrix result shapes. A vector
// sample is one pair under "value"; a matrix series is many under "values".
type promResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func (c *Client) get(path string, q url.Values) (*promResponse, error) {
	resp, err := c.http.Get(c.base + path + "?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("prometheus %s: %w", path, err)
	}
	defer resp.Body.Close()

	var body promResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("prometheus %s: decode: %w", path, err)
	}
	// A rejected expression must not read as "no data". Silently returning an
	// empty vector for a typo is the failure that wastes a whole experiment.
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus %s: %s: %s", path, body.Status, body.Error)
	}
	return &body, nil
}

// pair turns a [timestamp, "value"] element into its two halves.
func pair(p []any) (int64, float64, error) {
	if len(p) != 2 {
		return 0, 0, fmt.Errorf("malformed sample of length %d", len(p))
	}
	ts, ok := p[0].(float64)
	if !ok {
		return 0, 0, fmt.Errorf("sample timestamp %v is not a number", p[0])
	}
	s, ok := p[1].(string)
	if !ok {
		return 0, 0, fmt.Errorf("sample value %v is not a string", p[1])
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return int64(ts), 0, fmt.Errorf("sample value %q: %w", s, err)
	}
	return int64(ts), v, nil
}

func (c *Client) Query(expr string, t time.Time) (Vector, error) {
	q := url.Values{"query": {expr}, "time": {strconv.FormatInt(t.Unix(), 10)}}
	body, err := c.get("/api/v1/query", q)
	if err != nil {
		return nil, err
	}
	out := make(Vector, 0, len(body.Data.Result))
	for _, r := range body.Data.Result {
		_, v, err := pair(r.Value)
		if err != nil {
			return nil, fmt.Errorf("query %q: %w", expr, err)
		}
		out = append(out, Sample{Labels: r.Metric, Value: v})
	}
	return out, nil
}

// maxPoints is how many steps one query_range asks for. Prometheus rejects
// more than 11,000 points per series, which at 10 s is about 30 hours.
const maxPoints = 10_000

// QueryRange returns one Vector per step timestamp, keyed by unix seconds.
// Windows longer than maxPoints steps are fetched in consecutive chunks.
func (c *Client) QueryRange(expr string, start, end time.Time, step time.Duration) (map[int64]Vector, error) {
	out := map[int64]Vector{}
	for from := start; !from.After(end); {
		to := from.Add((maxPoints - 1) * step)
		if to.After(end) {
			to = end
		}
		q := url.Values{
			"query": {expr},
			"start": {strconv.FormatInt(from.Unix(), 10)},
			"end":   {strconv.FormatInt(to.Unix(), 10)},
			"step":  {strconv.Itoa(int(step.Seconds()))},
		}
		body, err := c.get("/api/v1/query_range", q)
		if err != nil {
			return nil, err
		}
		for _, r := range body.Data.Result {
			for _, p := range r.Values {
				ts, v, err := pair(p)
				if err != nil {
					return nil, fmt.Errorf("query_range %q: %w", expr, err)
				}
				out[ts] = append(out[ts], Sample{Labels: r.Metric, Value: v})
			}
		}
		from = to.Add(step)
	}
	return out, nil
}
