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

	if view.Metadata.NomadVersion != "1.10.0" {
		t.Fatalf("NomadVersion = %q, want %q", view.Metadata.NomadVersion, "1.10.0")
	}
	for _, warning := range view.Warnings {
		if warning == "No Nomad version was detected from the discovered metadata files." {
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

	if view.Metadata.NomadVersion != "" {
		t.Fatalf("NomadVersion = %q, want empty", view.Metadata.NomadVersion)
	}

	found := false
	for _, warning := range view.Warnings {
		if warning == "No Nomad version was detected from the discovered metadata files." {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected version-not-detected warning when no agent-self.json is present")
	}
}
