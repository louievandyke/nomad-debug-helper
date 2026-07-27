// Package metrics reads Nomad's captured go-metrics snapshots out of a debug
// bundle. Confirmed against a real `nomad operator debug` capture: unlike
// Vault, which writes one metrics.json holding an array of timestamped
// snapshots, Nomad writes one snapshot object per capture interval under
// interval/0000/metrics.json, interval/0001/metrics.json, etc. The field
// shapes (Name/Value/Labels for gauges; Name/Count/Rate/Sum/Min/Max/Mean/
// Stddev/Labels for counters and samples) match Vault's because both use the
// same underlying armon/go-metrics library.
package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Gauge struct {
	Name   string
	Value  float64
	Labels map[string]string
}

type Counter struct {
	Name   string
	Count  float64
	Rate   float64
	Sum    float64
	Min    float64
	Max    float64
	Mean   float64
	Stddev float64
	Labels map[string]string
}

type Sample struct {
	Name   string
	Count  float64
	Rate   float64
	Sum    float64
	Min    float64
	Max    float64
	Mean   float64
	Stddev float64
	Labels map[string]string
}

type Collection struct {
	Timestamp string
	Gauges    []Gauge
	Counters  []Counter
	Samples   []Sample
}

type Collections []Collection

// Load reads and merges every interval/*/metrics.json file under a Nomad
// debug bundle root, sorted by interval directory name (0000, 0001, ...).
func Load(rootPath string) (Collections, error) {
	matches, err := filepath.Glob(filepath.Join(rootPath, "interval", "*", "metrics.json"))
	if err != nil {
		return nil, fmt.Errorf("glob interval metrics: %w", err)
	}
	sort.Strings(matches)

	collections := make(Collections, 0, len(matches))
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		var collection Collection
		if err := json.Unmarshal(raw, &collection); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		collections = append(collections, collection)
	}

	return collections, nil
}
