package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/louie/nomad-debug-helper/internal/bundle"
	"github.com/louie/nomad-debug-helper/internal/layout"
)

type Bundle struct {
	SourcePath string
	RootPath   string
	SourceKind string
	Layout     layout.Info
	Metadata   Metadata
	Files      []File
	Warnings   []string
}

type Metadata struct {
	RootName      string
	AgentVersion  string
	CreatedAt     string
	MetadataFiles []string
	Documents     []Document
}

type Document struct {
	Path  string
	JSON  map[string]any
	Error string
}

type File struct {
	Path         string
	Category     string
	SizeHuman    string
	AgentRole    string
	AgentID      string
	ContentType  string
	AnalyzeURL   string
	AnalyzeLabel string
}

// AnalyzeLabel returns the human-readable action label for a pprof artifact,
// or "" if the file isn't one `go tool pprof`/`go tool trace` can open.
func AnalyzeLabel(tool bundle.AnalysisTool) string {
	switch tool {
	case bundle.AnalysisToolPprof:
		return "Analyze with go tool pprof"
	case bundle.AnalysisToolTrace:
		return "Open with go tool trace"
	default:
		return ""
	}
}

func Build(raw *bundle.Bundle, info layout.Info) (*Bundle, error) {
	view := &Bundle{
		SourcePath: raw.SourcePath,
		RootPath:   raw.RootPath,
		SourceKind: string(raw.SourceKind),
		Layout:     info,
		Metadata: Metadata{
			RootName:      filepath.Base(raw.RootPath),
			MetadataFiles: append([]string(nil), info.MetadataFiles...),
		},
		Files: make([]File, 0, len(raw.Files)),
	}

	for _, file := range raw.Files {
		viewFile := File{
			Path:        file.RelPath,
			Category:    string(file.Category),
			SizeHuman:   humanSize(file.Size),
			AgentRole:   string(file.AgentRole),
			AgentID:     file.AgentID,
			ContentType: file.ContentType,
		}
		if label := AnalyzeLabel(file.AnalysisTool); label != "" {
			viewFile.AnalyzeLabel = label
			viewFile.AnalyzeURL = "/analyze?path=" + url.QueryEscape(file.RelPath)
		}
		view.Files = append(view.Files, viewFile)
	}

	for _, relPath := range info.MetadataFiles {
		file, ok := raw.Lookup(relPath)
		if !ok {
			continue
		}
		document := parseMetadataDocument(file)
		view.Metadata.Documents = append(view.Metadata.Documents, document)

		if view.Metadata.AgentVersion == "" {
			if version := findString(document.JSON, "nomad_version", "version"); version != "" {
				view.Metadata.AgentVersion = version
			}
		}
		if view.Metadata.CreatedAt == "" {
			if createdAt := findString(document.JSON, "created_at", "timestamp", "time"); createdAt != "" {
				view.Metadata.CreatedAt = createdAt
			}
		}
	}

	// Fall back to a product-specific "agent self" document. Neither
	// Nomad's nor Consul's index.json exposes a plain top-level "version"
	// string for the generic heuristic above to find (Nomad's index.json is
	// a flat file-path array; Consul's uses "AgentVersion" alongside other
	// capitalized, differently-shaped fields), so each product's real agent
	// document is read directly instead.
	if view.Metadata.AgentVersion == "" {
		view.Metadata.AgentVersion = detectAgentVersion(raw)
	}

	if view.Metadata.AgentVersion == "" {
		view.Warnings = append(view.Warnings, "No agent version was detected from the discovered metadata files.")
	}

	return view, nil
}

// detectAgentVersion tries each product's "agent self" document in turn,
// since bundle root resolution doesn't always descend into the product's
// inner directory (Consul's top-level files don't match any name in
// bundleRootMarkers), lookups search by basename rather than assuming a
// fixed path like "cluster/agent-self.json".
func detectAgentVersion(raw *bundle.Bundle) string {
	if file, ok := findByBasename(raw, "agent-self.json"); ok {
		if version := nomadAgentSelfVersion(file); version != "" {
			return version
		}
	}
	if file, ok := findByBasename(raw, "agent.json"); ok {
		if version := consulAgentVersion(file); version != "" {
			return version
		}
	}
	return ""
}

// findByBasename returns the shallowest file in the bundle whose basename
// matches name.
func findByBasename(raw *bundle.Bundle, name string) (bundle.FileInfo, bool) {
	var best bundle.FileInfo
	var found bool
	for _, file := range raw.Files {
		if filepath.Base(file.RelPath) != name {
			continue
		}
		if !found || strings.Count(file.RelPath, "/") < strings.Count(best.RelPath, "/") {
			best = file
			found = true
		}
	}
	return best, found
}

// nomadAgentSelfVersion reads Nomad's cluster/agent-self.json. Its
// config.Version is itself a nested VersionInfo object (BuildDate, Revision,
// Version, ...), not a plain string -- confirmed against a real bundle after
// an earlier, wrong assumption that it was a string one level up. The actual
// semver lives at config.Version.Version.
func nomadAgentSelfVersion(file bundle.FileInfo) string {
	if file.Size > 1<<20 {
		return ""
	}

	content, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return ""
	}

	var payload struct {
		Config struct {
			Version struct {
				Version string `json:"Version"`
			} `json:"Version"`
		} `json:"config"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Config.Version.Version)
}

// consulAgentVersion reads `consul debug`'s agent.json, whose Config.Version
// is a plain semver string -- unlike Nomad's nested VersionInfo object --
// confirmed against a real capture.
func consulAgentVersion(file bundle.FileInfo) string {
	if file.Size > 1<<20 {
		return ""
	}

	content, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return ""
	}

	var payload struct {
		Config struct {
			Version string `json:"Version"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Config.Version)
}

func parseMetadataDocument(file bundle.FileInfo) Document {
	document := Document{Path: file.RelPath}
	if file.Size > 1<<20 {
		document.Error = "skipped preview because file is larger than 1 MiB"
		return document
	}

	content, err := os.ReadFile(file.AbsPath)
	if err != nil {
		document.Error = fmt.Sprintf("read failed: %v", err)
		return document
	}

	var raw map[string]any
	if err := json.Unmarshal(content, &raw); err != nil {
		document.Error = fmt.Sprintf("json decode failed: %v", err)
		return document
	}
	document.JSON = raw
	return document
}

func findString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		}
	}
	return ""
}

func humanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}
