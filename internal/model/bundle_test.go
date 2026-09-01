package model

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/louie/nomad-debug-helper/internal/bundle"
	"github.com/louie/nomad-debug-helper/internal/layout"
)

func TestBuildDetectsVersionFromAgentSelf(t *testing.T) {
	root := t.TempDir()
	clusterDir := filepath.Join(root, "cluster")
	if err := os.MkdirAll(clusterDir, 0o755); err != nil {
		t.Fatalf("mkdir cluster: %v", err)
	}

	// Real shape confirmed against a live bundle: config.Version is a nested
	// VersionInfo object, not a plain string -- the semver is one level
	// deeper at config.Version.Version. Getting this wrong once already
	// caused "Nomad Version: unknown" to silently persist on every real
	// bundle.
	agentSelfPath := filepath.Join(clusterDir, "agent-self.json")
	agentSelfContents := `{
		"config": {
			"Version": {
				"BuildDate": "2025-04-09T16:40:54Z",
				"Revision": "e26a2bd2acac2dcdcb623f4d293bac096beef478",
				"Version": "1.10.0",
				"VersionMetadata": "",
				"VersionPrerelease": ""
			}
		}
	}`
	if err := os.WriteFile(agentSelfPath, []byte(agentSelfContents), 0o644); err != nil {
		t.Fatalf("write agent-self.json: %v", err)
	}

	raw := &bundle.Bundle{
		SourcePath: root,
		RootPath:   root,
		SourceKind: bundle.SourceDirectory,
		Files: []bundle.FileInfo{
			{RelPath: "cluster/agent-self.json", AbsPath: agentSelfPath, Size: int64(len(agentSelfContents))},
		},
	}

	view, err := Build(raw, layout.Info{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if view.Metadata.AgentVersion != "1.10.0" {
		t.Fatalf("AgentVersion = %q, want %q", view.Metadata.AgentVersion, "1.10.0")
	}
	for _, warning := range view.Warnings {
		if warning == "No agent version was detected from the discovered metadata files." {
			t.Fatalf("unexpected version-not-detected warning despite a valid agent-self.json")
		}
	}
}

func TestBuildWarnsWhenNoVersionFound(t *testing.T) {
	root := t.TempDir()

	raw := &bundle.Bundle{
		SourcePath: root,
		RootPath:   root,
		SourceKind: bundle.SourceDirectory,
	}

	view, err := Build(raw, layout.Info{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if view.Metadata.AgentVersion != "" {
		t.Fatalf("AgentVersion = %q, want empty", view.Metadata.AgentVersion)
	}

	found := false
	for _, warning := range view.Warnings {
		if warning == "No agent version was detected from the discovered metadata files." {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected version-not-detected warning when no agent-self.json is present")
	}
}

// TestBuildDetectsVersionFromConsulAgent confirms the Consul fallback path:
// agent.json's Config.Version is a plain semver string (unlike Nomad's
// nested VersionInfo object at cluster/agent-self.json's config.Version),
// and it lives at the bundle root, not under a "cluster" directory --
// confirmed against a real `consul debug` capture.
func TestBuildDetectsVersionFromConsulAgent(t *testing.T) {
	root := t.TempDir()

	agentPath := filepath.Join(root, "agent.json")
	agentContents := `{
		"Config": {
			"Datacenter": "dc1",
			"NodeName": "test-node",
			"Server": true,
			"Version": "1.21.0"
		}
	}`
	if err := os.WriteFile(agentPath, []byte(agentContents), 0o644); err != nil {
		t.Fatalf("write agent.json: %v", err)
	}

	raw := &bundle.Bundle{
		SourcePath: root,
		RootPath:   root,
		SourceKind: bundle.SourceDirectory,
		Files: []bundle.FileInfo{
			{RelPath: "agent.json", AbsPath: agentPath, Size: int64(len(agentContents))},
		},
	}

	view, err := Build(raw, layout.Info{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if view.Metadata.AgentVersion != "1.21.0" {
		t.Fatalf("AgentVersion = %q, want %q", view.Metadata.AgentVersion, "1.21.0")
	}
}

// TestBuildPopulatesConsulMetadata confirms that Build extracts Datacenter,
// NodeName, DebugInterval, DebugDuration, and DebugTargets when the layout
// product is "consul", using agent.json and index.json respectively --
// confirmed shapes against a real `consul debug` capture.
func TestBuildPopulatesConsulMetadata(t *testing.T) {
	root := t.TempDir()

	agentPath := filepath.Join(root, "agent.json")
	agentContents := `{
		"Config": {
			"Datacenter": "dc1",
			"NodeName": "my-node",
			"Server": true,
			"Version": "1.21.0"
		}
	}`
	if err := os.WriteFile(agentPath, []byte(agentContents), 0o644); err != nil {
		t.Fatalf("write agent.json: %v", err)
	}

	indexPath := filepath.Join(root, "index.json")
	indexContents := `{
		"Version": 1,
		"AgentVersion": "1.21.0",
		"Interval": "30s",
		"Duration": "5m0s",
		"Targets": ["metrics", "logs", "pprof"]
	}`
	if err := os.WriteFile(indexPath, []byte(indexContents), 0o644); err != nil {
		t.Fatalf("write index.json: %v", err)
	}

	raw := &bundle.Bundle{
		SourcePath: root,
		RootPath:   root,
		SourceKind: bundle.SourceDirectory,
		Files: []bundle.FileInfo{
			{RelPath: "agent.json", AbsPath: agentPath, Size: int64(len(agentContents))},
			{RelPath: "index.json", AbsPath: indexPath, Size: int64(len(indexContents))},
		},
	}

	view, err := Build(raw, layout.Info{Product: "consul"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if view.Metadata.Datacenter != "dc1" {
		t.Fatalf("Datacenter = %q, want %q", view.Metadata.Datacenter, "dc1")
	}
	if view.Metadata.NodeName != "my-node" {
		t.Fatalf("NodeName = %q, want %q", view.Metadata.NodeName, "my-node")
	}
	if view.Metadata.DebugInterval != "30s" {
		t.Fatalf("DebugInterval = %q, want %q", view.Metadata.DebugInterval, "30s")
	}
	if view.Metadata.DebugDuration != "5m0s" {
		t.Fatalf("DebugDuration = %q, want %q", view.Metadata.DebugDuration, "5m0s")
	}
	if len(view.Metadata.DebugTargets) != 3 || view.Metadata.DebugTargets[0] != "metrics" {
		t.Fatalf("DebugTargets = %v, want [metrics logs pprof]", view.Metadata.DebugTargets)
	}
}

// TestBuildDoesNotPopulateConsulMetadataForNomad confirms that Consul-specific
// fields remain empty when the layout product is "nomad".
func TestBuildDoesNotPopulateConsulMetadataForNomad(t *testing.T) {
	root := t.TempDir()

	raw := &bundle.Bundle{
		SourcePath: root,
		RootPath:   root,
		SourceKind: bundle.SourceDirectory,
	}

	view, err := Build(raw, layout.Info{Product: "nomad"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if view.Metadata.Datacenter != "" {
		t.Fatalf("Datacenter = %q, want empty for Nomad bundle", view.Metadata.Datacenter)
	}
	if view.Metadata.DebugInterval != "" {
		t.Fatalf("DebugInterval = %q, want empty for Nomad bundle", view.Metadata.DebugInterval)
	}
}
