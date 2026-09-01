package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/louie/nomad-debug-helper/internal/metrics"
)

// TimeSeriesDataPoint and TimeSeriesMetric mirror the shape vault-debug-helper
// production (~/z/vault-debug-helper-main's metrics_json.go) serves to
// Grafana's Infinity datasource: a flat JSON array of samples, each carrying
// its own top-level timestamp/value so a jq-backend "columns" selector can
// pull them out directly.
type TimeSeriesDataPoint struct {
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
}

type TimeSeriesMetric struct {
	Name       string                `json:"name"`
	Type       string                `json:"type"`
	Labels     map[string]string     `json:"labels"`
	Series     string                `json:"series,omitempty"`
	Timestamp  string                `json:"timestamp"`
	Value      float64               `json:"value"`
	DataPoints []TimeSeriesDataPoint `json:"datapoints"`
}

type metricsJSONResponse struct {
	Metrics []TimeSeriesMetric `json:"metrics"`
}

// handleMetricsJSON serves Nomad's captured metrics as flat JSON for Grafana's
// Infinity datasource. ?name=<metric> filters to one series (a bare array),
// matching how the dashboard panels below query it.
func (s *Server) handleMetricsJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	collections, err := s.loadMetrics()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	series := buildTimeSeries(collections)

	if name := strings.TrimSpace(r.URL.Query().Get("name")); name != "" {
		if err := json.NewEncoder(w).Encode(filterTimeSeries(series, name)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if prefix := strings.TrimSpace(r.URL.Query().Get("prefix")); prefix != "" {
		if err := json.NewEncoder(w).Encode(filterTimeSeriesByPrefix(series, prefix)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if err := json.NewEncoder(w).Encode(metricsJSONResponse{Metrics: series}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func buildTimeSeries(collections metrics.Collections) []TimeSeriesMetric {
	var series []TimeSeriesMetric

	for _, c := range collections {
		timestamp := c.Timestamp
		if parsed, err := time.Parse("2006-01-02 15:04:05 +0000 UTC", c.Timestamp); err == nil {
			timestamp = parsed.UTC().Format(time.RFC3339)
		}

		for _, g := range c.Gauges {
			series = append(series, newTimeSeriesMetric(sanitizeMetricName(g.Name), "gauge", g.Labels, timestamp, g.Value))
		}
		for _, ctr := range c.Counters {
			series = append(series, newTimeSeriesMetric(sanitizeMetricName(ctr.Name), "counter", ctr.Labels, timestamp, ctr.Count))
		}
		for _, smp := range c.Samples {
			base := sanitizeMetricName(smp.Name)
			series = append(series,
				newTimeSeriesMetric(base, "sample", smp.Labels, timestamp, smp.Mean),
				newTimeSeriesMetric(base+"_count", "sample", smp.Labels, timestamp, smp.Count),
				newTimeSeriesMetric(base+"_sum", "sample", smp.Labels, timestamp, smp.Sum),
				newTimeSeriesMetric(base+"_min", "sample", smp.Labels, timestamp, smp.Min),
				newTimeSeriesMetric(base+"_max", "sample", smp.Labels, timestamp, smp.Max),
			)
		}
	}

	return series
}

func filterTimeSeries(series []TimeSeriesMetric, name string) []TimeSeriesMetric {
	filtered := make([]TimeSeriesMetric, 0)
	for _, s := range series {
		if s.Name == name {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// filterTimeSeriesByPrefix returns all series whose names start with prefix.
// Used by the Consul dashboard to match node-qualified metric names like
// "consul_<nodename>_autopilot_healthy" without hardcoding the node name.
func filterTimeSeriesByPrefix(series []TimeSeriesMetric, prefix string) []TimeSeriesMetric {
	filtered := make([]TimeSeriesMetric, 0)
	for _, s := range series {
		if strings.HasPrefix(s.Name, prefix) {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func newTimeSeriesMetric(name, metricType string, labels map[string]string, timestamp string, value float64) TimeSeriesMetric {
	return TimeSeriesMetric{
		Name:       name,
		Type:       metricType,
		Labels:     labels,
		Series:     seriesName(labels),
		Timestamp:  timestamp,
		Value:      value,
		DataPoints: []TimeSeriesDataPoint{{Timestamp: timestamp, Value: value}},
	}
}

func seriesName(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, ",")
}

// sanitizeMetricName replaces Nomad's dotted metric names (nomad.raft.barrier)
// with underscore-separated names (nomad_raft_barrier), matching the
// convention vault-debug-helper-main already uses so dashboard queries stay
// consistent between the two tools.
func sanitizeMetricName(name string) string {
	replacer := strings.NewReplacer(".", "_", "-", "_", "/", "_")
	return replacer.Replace(name)
}
