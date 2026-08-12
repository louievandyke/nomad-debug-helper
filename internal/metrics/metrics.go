// Package metrics reads captured go-metrics snapshots out of a debug bundle.
// Two on-disk layouts are supported, confirmed against real captures:
//   - Nomad (`nomad operator debug`): one snapshot object per capture
//     interval, under interval/0000/metrics.json, interval/0001/metrics.json,
//     etc.
//   - Consul (`consul debug`): a single metrics.json at the bundle root
//     holding every snapshot as consecutive, non-array-wrapped JSON objects
//     (`{...}{...}{...}`, not `[{...},{...}]`) -- confirmed against a real
//     `consul debug` capture, this is not valid JSON on its own and needs a
//     streaming decoder rather than json.Unmarshal into a slice.
//
// The field shapes (Name/Value/Labels for gauges; Name/Count/Rate/Sum/Min/
// Max/Mean/Stddev/Labels for counters and samples) match across both, and
// also match Vault's, because all three products use the same underlying
// armon/go-metrics library.
package metrics

import (
	"bytes"
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

// Load reads a debug bundle's captured metrics, trying Nomad's per-interval
// layout first and falling back to a single flat metrics.json (Consul's
// layout) when no interval directories are present.
func Load(rootPath string) (Collections, error) {
	collections, err := loadIntervalLayout(rootPath)
	if err != nil {
		return nil, err
	}
	if len(collections) > 0 {
		return collections, nil
	}

	return loadFlatLayout(rootPath)
}

// loadIntervalLayout reads and merges every interval/*/metrics.json file
// under a Nomad debug bundle root, sorted by interval directory name (0000,
// 0001, ...). Returns an empty, non-nil Collections (not an error) when no
// interval directories exist, so Load can fall back to loadFlatLayout.
func loadIntervalLayout(rootPath string) (Collections, error) {
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

// loadFlatLayout finds a single metrics.json at the bundle root (or one
// directory below it, in case layout detection didn't descend into the
// product's inner bundle directory) and decodes it as Consul's layout:
// consecutive top-level JSON objects rather than a JSON array. Returns nil,
// nil when no such file exists.
func loadFlatLayout(rootPath string) (Collections, error) {
	path, err := findFlatMetricsFile(rootPath)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	collections := make(Collections, 0, 64)
	for dec.More() {
		var collection Collection
		if err := dec.Decode(&collection); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		collections = append(collections, collection)
	}

	return collections, nil
}

func findFlatMetricsFile(rootPath string) (string, error) {
	candidates := []string{filepath.Join(rootPath, "metrics.json")}

	entries, err := os.ReadDir(rootPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", rootPath, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			candidates = append(candidates, filepath.Join(rootPath, entry.Name(), "metrics.json"))
		}
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", nil
}
