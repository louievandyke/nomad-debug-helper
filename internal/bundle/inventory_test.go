package bundle

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestInventoryClassifiesFiles(t *testing.T) {
	root := t.TempDir()

	writeFile := func(relPath string) {
		t.Helper()
		fullPath := filepath.Join(root, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", relPath, err)
		}
	}

	writeFile("manifest.json")
	writeFile("servers/server-1/goroutine.prof")
	writeFile("clients/client-1/journald.log")
	writeFile("interval-0001/evaluations.json")
	writeFile("events/stream.json")
	writeFile("client/node-1/profile_0000.prof")
	writeFile("client/node-1/trace_0000.prof")

	files, err := Inventory(root)
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}

	tests := map[string]struct {
		category     FileCategory
		role         AgentRole
		agentID      string
		analysisTool AnalysisTool
	}{
		"manifest.json":                   {category: CategoryMetadata, role: RoleUnknown},
		"servers/server-1/goroutine.prof": {category: CategoryPprof, role: RoleServer, agentID: "server-1", analysisTool: AnalysisToolPprof},
		"clients/client-1/journald.log":   {category: CategoryLog, role: RoleClient, agentID: "client-1"},
		"interval-0001/evaluations.json":  {category: CategorySnapshot, role: RoleUnknown},
		"events/stream.json":              {category: CategoryEvent, role: RoleUnknown},
		"client/node-1/profile_0000.prof": {category: CategoryPprof, role: RoleClient, agentID: "node-1", analysisTool: AnalysisToolPprof},
		"client/node-1/trace_0000.prof":   {category: CategoryPprof, role: RoleClient, agentID: "node-1", analysisTool: AnalysisToolTrace},
	}

	for _, file := range files {
		want, ok := tests[file.RelPath]
		if !ok {
			continue
		}
		if file.Category != want.category {
			t.Fatalf("%s category = %s, want %s", file.RelPath, file.Category, want.category)
		}
		if file.AgentRole != want.role {
			t.Fatalf("%s role = %s, want %s", file.RelPath, file.AgentRole, want.role)
		}
		if file.AgentID != want.agentID {
			t.Fatalf("%s agentID = %q, want %q", file.RelPath, file.AgentID, want.agentID)
		}
		if file.AnalysisTool != want.analysisTool {
			t.Fatalf("%s analysisTool = %q, want %q", file.RelPath, file.AnalysisTool, want.analysisTool)
		}
		delete(tests, file.RelPath)
	}

	if len(tests) != 0 {
		t.Fatalf("missing files from inventory: %v", tests)
	}
}

func TestOpenArchiveExtractsBundle(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "nomad-debug-2026-01-22-183255Z.tar.gz")

	if err := writeTarGz(archivePath, map[string]string{
		"nomad-debug-2026-01-22-183255Z/manifest.json":         `{"nomad_version":"1.10.0"}`,
		"nomad-debug-2026-01-22-183255Z/cluster/leader.json":   `{"leader":"node-1"}`,
		"nomad-debug-2026-01-22-183255Z/server/node-1/app.log": `hello`,
	}); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	bundle, err := Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}

	if bundle.SourceKind != SourceArchive {
		t.Fatalf("source kind = %s, want %s", bundle.SourceKind, SourceArchive)
	}
	if bundle.SourcePath != archivePath {
		t.Fatalf("source path = %s, want %s", bundle.SourcePath, archivePath)
	}
	if filepath.Base(bundle.RootPath) != "nomad-debug-2026-01-22-183255Z" {
		t.Fatalf("root path = %s, want extracted bundle root", bundle.RootPath)
	}
	if _, ok := bundle.Lookup("manifest.json"); !ok {
		t.Fatalf("expected extracted manifest.json in bundle inventory")
	}

	extractedRoot := bundle.RootPath
	if err := bundle.Cleanup(); err != nil {
		t.Fatalf("cleanup bundle: %v", err)
	}
	if _, err := os.Stat(extractedRoot); !os.IsNotExist(err) {
		t.Fatalf("expected extracted root to be removed, stat err = %v", err)
	}
}

func TestOpenDirectoryDescendsIntoSingleBundleSubdirectory(t *testing.T) {
	root := t.TempDir()
	bundleDir := filepath.Join(root, "nomad-debug-2026-01-22-183255Z")
	if err := os.MkdirAll(filepath.Join(bundleDir, "cluster"), 0o755); err != nil {
		t.Fatalf("mkdir bundle dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nomad-debug-2026-01-22-183255Z.tar.gz"), []byte("placeholder"), 0o644); err != nil {
		t.Fatalf("write archive placeholder: %v", err)
	}

	bundle, err := OpenDirectory(root)
	if err != nil {
		t.Fatalf("open directory: %v", err)
	}

	if bundle.RootPath != bundleDir {
		t.Fatalf("root path = %s, want %s", bundle.RootPath, bundleDir)
	}
	if bundle.SourcePath != root {
		t.Fatalf("source path = %s, want %s", bundle.SourcePath, root)
	}
}

func writeTarGz(path string, files map[string]string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	for name, content := range files {
		header := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			return err
		}
	}

	return nil
}
