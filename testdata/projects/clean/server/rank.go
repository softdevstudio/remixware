// Package server ranks the services by how they answer.
package server

import "sort"

// Result is one ping.
type Result struct {
	URL     string
	Status  int
	Latency float64
}

type byLatency []Result

func (r byLatency) Len() int           { return len(r) }
func (r byLatency) Less(i, j int) bool { return r[i].Latency < r[j].Latency }
func (r byLatency) Swap(i, j int)      { r[i], r[j] = r[j], r[i] }

type byURL []Result

func (r byURL) Len() int { return len(r) }
func (r byURL) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}
func (r byURL) Less(i, j int) bool {
	return r[i].URL < r[j].URL
}

// Fastest returns the results ordered by latency, failed pings last.
func Fastest(rs []Result) []Result {
	var ok, failed []Result
	for _, r := range rs {
		if r.Status >= 200 && r.Status < 400 {
			ok = append(ok, r)
		} else {
			failed = append(failed, r)
		}
	}
	sort.Sort(byLatency(ok))
	sort.Sort(byURL(failed))
	return append(ok, failed...)
}

// statusText names a status code for the report.
func statusText(code int) string {
	switch code {
	case 200:
		return "ok"
	case 301, 302, 307, 308:
		return "redirect"
	case 401, 403:
		return "refused"
	case 404:
		return "missing"
	case 500, 502, 503, 504:
		return "server error"
	}
	return "unexpected"
}
