package layout

import (
	"testing"

	"github.com/louie/nomad-debug-helper/internal/bundle"
)

func TestDetectProductConsul(t *testing.T) {
	raw := &bundle.Bundle{
		SourcePath: "/tmp/test",
		RootPath:   "/tmp/test",
		SourceKind: bundle.SourceDirectory,
		Files: []bundle.FileInfo{
			{RelPath: "agent.json"},
			{RelPath: "consul.log"},
			{RelPath: "metrics.json"},
		},
	}

	info := Detect(raw)

	if info.Product != "consul" {
		t.Fatalf("Product = %q, want %q", info.Product, "consul")
	}
	if info.Kind != "consul-debug-output-dir" {
		t.Fatalf("Kind = %q, want %q", info.Kind, "consul-debug-output-dir")
	}
}

func TestDetectProductConsulArchive(t *testing.T) {
	raw := &bundle.Bundle{
		SourcePath: "/tmp/consul.tar.gz",
		RootPath:   "/tmp/consul",
		SourceKind: bundle.SourceArchive,
		Files: []bundle.FileInfo{
			{RelPath: "trace.out"},
		},
	}

	info := Detect(raw)

	if info.Product != "consul" {
		t.Fatalf("Product = %q, want %q", info.Product, "consul")
	}
	if info.Kind != "consul-debug-output-archive" {
		t.Fatalf("Kind = %q, want %q", info.Kind, "consul-debug-output-archive")
	}
}

func TestDetectProductNomad(t *testing.T) {
	raw := &bundle.Bundle{
		SourcePath: "/tmp/nomad",
		RootPath:   "/tmp/nomad",
		SourceKind: bundle.SourceDirectory,
		Files: []bundle.FileInfo{
			{RelPath: "manifest.json"},
			{RelPath: "cluster/leader.json"},
		},
	}

	info := Detect(raw)

	if info.Product != "nomad" {
		t.Fatalf("Product = %q, want %q", info.Product, "nomad")
	}
	if info.Kind != "nomad-debug-output-dir" {
		t.Fatalf("Kind = %q, want %q", info.Kind, "nomad-debug-output-dir")
	}
}
