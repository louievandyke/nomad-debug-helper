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
	NomadVersion  string
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

		if view.Metadata.NomadVersion == "" {
			if version := findString(document.JSON, "nomad_version", "version"); version != "" {
				view.Metadata.NomadVersion = version
			}
		}
		if view.Metadata.CreatedAt == "" {
			if createdAt := findString(document.JSON, "created_at", "timestamp", "time"); createdAt != "" {
				view.Metadata.CreatedAt = createdAt
			}
		}
	}

	// Fall back to cluster/agent-self.json's nested config.Version.
	// index.json is in metadataNames but is Nomad's flat file-path array
	// (fails json decode above, confirmed live), and agent-self.json isn't a
	// "metadata file" by the generic top-level-string-field heuristic above
	// since its version lives nested under "config", not at the top level.
	if view.Metadata.NomadVersion == "" {
		view.Metadata.NomadVersion = agentSelfVersion(raw)
	}

	if view.Metadata.NomadVersion == "" {
		view.Warnings = append(view.Warnings, "No Nomad version was detected from the discovered metadata files.")
	}

	return view, nil
}

func agentSelfVersion(raw *bundle.Bundle) string {
	file, ok := raw.Lookup("cluster/agent-self.json")
	if !ok || file.Size > 1<<20 {
		return ""
	}

	content, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return ""
	}

	// config.Version is itself a nested VersionInfo object (BuildDate,
	// Revision, Version, ...), not a plain string -- confirmed against a
	// real bundle after an earlier, wrong assumption that it was a string
	// one level up. The actual semver lives at config.Version.Version.
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
