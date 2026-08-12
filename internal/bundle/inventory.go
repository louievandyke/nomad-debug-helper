package bundle

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// metadataNames intentionally excludes index.json: confirmed against real
// `nomad operator debug` bundles that it's a flat JSON array of file paths,
// not an object, so it can never satisfy the metadata-document heuristic in
// model.Build (which extracts top-level string fields like nomad_version).
// Treating it as CategoryJSON instead keeps it fully browsable via Raw Files
// without a permanent, unactionable "json decode failed" row on the overview
// page. The Nomad version itself is recovered separately from
// cluster/agent-self.json.
var metadataNames = map[string]struct{}{
	"debug.json":    {},
	"manifest.json": {},
	"meta.json":     {},
	"metadata.json": {},
	"summary.json":  {},
	"version.json":  {},
}

var snapshotNames = map[string]struct{}{
	"allocations.json": {},
	"clients.json":     {},
	"deployments.json": {},
	"evaluations.json": {},
	"jobs.json":        {},
	"leader.json":      {},
	"members.json":     {},
	"node.json":        {},
	"nodes.json":       {},
	"raft.json":        {},
	"server.json":      {},
	"servers.json":     {},
	"state.json":       {},
}

func Inventory(root string) ([]FileInfo, error) {
	files := make([]FileInfo, 0, 64)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relative path for %s: %w", path, err)
		}

		normalized := filepath.ToSlash(relPath)
		role, agentID := detectAgent(normalized)
		category := classify(normalized)
		baseName := filepath.Base(normalized)

		files = append(files, FileInfo{
			RelPath:      normalized,
			AbsPath:      path,
			Size:         info.Size(),
			ModTime:      info.ModTime(),
			BaseName:     baseName,
			Ext:          strings.ToLower(filepath.Ext(normalized)),
			Category:     category,
			ContentType:  detectContentType(normalized),
			AgentRole:    role,
			AgentID:      agentID,
			AnalysisTool: classifyAnalysisTool(category, baseName),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].RelPath < files[j].RelPath
	})

	return files, nil
}

func classify(relPath string) FileCategory {
	lower := strings.ToLower(relPath)
	base := filepath.Base(lower)
	ext := strings.ToLower(filepath.Ext(lower))

	// base == "trace.out" catches Consul's execution trace artifact
	// (`consul debug`), which shares no extension or path fragment with
	// Nomad's trace_%04d.prof but is the same kind of binary profiling
	// artifact -- it must land in CategoryPprof, not CategoryText, so the
	// file browser offers "Analyze" instead of dumping raw trace bytes as
	// text (see preview() in web/server.go).
	if strings.Contains(lower, "pprof") || ext == ".prof" || ext == ".pprof" || base == "trace.out" {
		return CategoryPprof
	}
	if strings.Contains(lower, "event") {
		return CategoryEvent
	}
	if strings.Contains(lower, "journald") || strings.Contains(lower, "/logs/") || strings.Contains(lower, "monitor") || ext == ".log" {
		return CategoryLog
	}
	if _, ok := metadataNames[base]; ok {
		return CategoryMetadata
	}
	if _, ok := snapshotNames[base]; ok || strings.Contains(lower, "snapshot") {
		return CategorySnapshot
	}
	if ext == ".json" {
		if strings.Contains(lower, "state") || strings.Contains(lower, "alloc") || strings.Contains(lower, "eval") || strings.Contains(lower, "deploy") || strings.Contains(lower, "job") {
			return CategorySnapshot
		}
		return CategoryJSON
	}
	if ext == ".txt" || ext == ".md" || ext == ".out" || ext == ".err" || ext == ".csv" {
		return CategoryText
	}
	if ext == ".gz" || ext == ".zip" || ext == ".bin" {
		return CategoryBinary
	}
	return CategoryOther
}

// classifyAnalysisTool picks which `go tool` command can open a pprof
// artifact. Confirmed against a real `nomad operator debug` capture:
// profile_%04d.prof, heap_%04d.prof, goroutine_%04d.prof, and
// threadcreate_%04d.prof are gzip-compressed pprof protobufs; trace_%04d.prof
// is a raw Go execution trace (`file` reports plain "data", not gzip) and
// needs `go tool trace` instead. Confirmed against a real `consul debug`
// capture too: Consul's equivalent execution trace is named trace.out, not
// trace_%04d.prof, so it needs its own check here.
func classifyAnalysisTool(category FileCategory, baseName string) AnalysisTool {
	if category != CategoryPprof {
		return AnalysisToolNone
	}
	if strings.HasPrefix(baseName, "trace_") || baseName == "trace.out" {
		return AnalysisToolTrace
	}
	return AnalysisToolPprof
}

func detectContentType(relPath string) string {
	ext := strings.ToLower(filepath.Ext(relPath))
	switch ext {
	case ".json":
		return "application/json"
	case ".log", ".txt", ".md", ".out", ".err", ".csv":
		return "text/plain"
	case ".prof", ".pprof":
		return "application/octet-stream"
	default:
		return "application/octet-stream"
	}
}

func detectAgent(relPath string) (AgentRole, string) {
	parts := strings.Split(relPath, "/")
	for i, part := range parts {
		switch part {
		case "servers", "server":
			if i+1 < len(parts) {
				return RoleServer, parts[i+1]
			}
			return RoleServer, ""
		case "clients", "client", "nodes", "node":
			if i+1 < len(parts) {
				return RoleClient, parts[i+1]
			}
			return RoleClient, ""
		}
	}
	return RoleUnknown, ""
}

func readPreview(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := make([]byte, limit)
	n, err := file.Read(buf)
	if err != nil && err.Error() != "EOF" {
		return nil, err
	}
	return buf[:n], nil
}
