package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergesIntervalSnapshots(t *testing.T) {
	root := t.TempDir()

	writeInterval := func(name, contents string) {
		t.Helper()
		dir := filepath.Join(root, "interval", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "metrics.json"), []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	writeInterval("0001", `{
		"Timestamp": "2026-01-22 18:33:25 +0000 UTC",
		"Gauges": [{"Name": "nomad.runtime.num_goroutines", "Value": 42, "Labels": {"host": "node-1"}}],
		"Counters": [{"Name": "nomad.raft.barrier", "Count": 3, "Sum": 3, "Labels": {}}],
		"Samples": [{"Name": "nomad.client.acl.resolve_token", "Count": 9, "Mean": 0.003, "Labels": {}}]
	}`)
	writeInterval("0000", `{
		"Timestamp": "2026-01-22 18:32:55 +0000 UTC",
		"Gauges": [{"Name": "nomad.runtime.num_goroutines", "Value": 40, "Labels": {"host": "node-1"}}]
	}`)

	collections, err := Load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(collections) != 2 {
		t.Fatalf("collections = %d, want 2", len(collections))
	}

	// Glob results are sorted by interval directory name, so 0000 must come
	// before 0001 regardless of write order above.
	if collections[0].Timestamp != "2026-01-22 18:32:55 +0000 UTC" {
		t.Fatalf("first collection timestamp = %q, want interval 0000's", collections[0].Timestamp)
	}
	if collections[1].Timestamp != "2026-01-22 18:33:25 +0000 UTC" {
		t.Fatalf("second collection timestamp = %q, want interval 0001's", collections[1].Timestamp)
	}

	if len(collections[1].Counters) != 1 || collections[1].Counters[0].Name != "nomad.raft.barrier" {
		t.Fatalf("unexpected counters in second collection: %+v", collections[1].Counters)
	}
	if len(collections[1].Samples) != 1 || collections[1].Samples[0].Mean != 0.003 {
		t.Fatalf("unexpected samples in second collection: %+v", collections[1].Samples)
	}
}

// TestLoadReadsConsulFlatLayout confirms Load falls back to a single
// metrics.json holding consecutive, non-array-wrapped JSON objects -- how
// `consul debug` writes it, verified against a real capture. This is not
// valid JSON as a whole (`{...}{...}` is not `[{...},{...}]`), so a plain
// json.Unmarshal into a slice would fail; regression-tests the streaming
// decode in loadFlatLayout.
func TestLoadReadsConsulFlatLayout(t *testing.T) {
	root := t.TempDir()

	contents := `{
		"Timestamp": "2026-08-03 20:52:10 +0000 UTC",
		"Gauges": [{"Name": "consul.node.autopilot.healthy", "Value": 1, "Labels": {}}],
		"Counters": [{"Name": "consul.client.rpc", "Count": 2, "Sum": 2, "Labels": {}}],
		"Samples": []
	}{
		"Timestamp": "2026-08-03 20:52:40 +0000 UTC",
		"Gauges": [{"Name": "consul.node.autopilot.healthy", "Value": 1, "Labels": {}}],
		"Counters": [],
		"Samples": [{"Name": "consul.acl.ResolveToken", "Count": 15, "Mean": 0.0327, "Labels": {}}]
	}`
	if err := os.WriteFile(filepath.Join(root, "metrics.json"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write metrics.json: %v", err)
	}

	collections, err := Load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(collections) != 2 {
		t.Fatalf("collections = %d, want 2", len(collections))
	}
	if collections[0].Timestamp != "2026-08-03 20:52:10 +0000 UTC" {
		t.Fatalf("first collection timestamp = %q, want first snapshot's", collections[0].Timestamp)
	}
	if len(collections[1].Samples) != 1 || collections[1].Samples[0].Name != "consul.acl.ResolveToken" {
		t.Fatalf("unexpected samples in second collection: %+v", collections[1].Samples)
	}
}

// TestLoadReadsConsulFlatLayoutFromNestedBundleDir confirms the fallback also
// finds metrics.json one directory below root, for the case where layout
// detection didn't descend into the archive's inner bundle directory (its
// name varies per capture, e.g. "consul-debug-<timestamp>", and isn't a
// recognized bundleRootMarkers entry).
func TestLoadReadsConsulFlatLayoutFromNestedBundleDir(t *testing.T) {
	root := t.TempDir()
	innerDir := filepath.Join(root, "consul-debug-2026-08-03T13-52-10-0700")
	if err := os.MkdirAll(innerDir, 0o755); err != nil {
		t.Fatalf("mkdir inner dir: %v", err)
	}

	contents := `{"Timestamp": "2026-08-03 20:52:10 +0000 UTC", "Gauges": [], "Counters": [], "Samples": []}`
	if err := os.WriteFile(filepath.Join(innerDir, "metrics.json"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write metrics.json: %v", err)
	}

	collections, err := Load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(collections) != 1 {
		t.Fatalf("collections = %d, want 1", len(collections))
	}
}
