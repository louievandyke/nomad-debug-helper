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
