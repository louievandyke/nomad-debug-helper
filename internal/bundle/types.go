package bundle

import (
	"os"
	"time"
)

type SourceKind string

const (
	SourceDirectory SourceKind = "directory"
	SourceArchive   SourceKind = "archive"
)

type AgentRole string

const (
	RoleUnknown AgentRole = "unknown"
	RoleServer  AgentRole = "server"
	RoleClient  AgentRole = "client"
)

type FileCategory string

const (
	CategoryMetadata FileCategory = "metadata"
	CategorySnapshot FileCategory = "snapshot"
	CategoryLog      FileCategory = "log"
	CategoryEvent    FileCategory = "event"
	CategoryPprof    FileCategory = "pprof"
	CategoryJSON     FileCategory = "json"
	CategoryText     FileCategory = "text"
	CategoryBinary   FileCategory = "binary"
	CategoryOther    FileCategory = "other"
)

// AnalysisTool identifies which `go tool` command can open a given pprof
// artifact. Nomad's debug output uses two incompatible binary formats under
// the same .prof extension: profile/heap/goroutine/threadcreate are standard
// pprof protobufs, but trace_*.prof is a raw Go execution trace and requires
// `go tool trace` instead of `go tool pprof`.
type AnalysisTool string

const (
	AnalysisToolNone  AnalysisTool = ""
	AnalysisToolPprof AnalysisTool = "pprof"
	AnalysisToolTrace AnalysisTool = "trace"
)

type Bundle struct {
	SourcePath string
	RootPath   string
	SourceKind SourceKind
	Files      []FileInfo

	cleanupDir string
}

type FileInfo struct {
	RelPath      string
	AbsPath      string
	Size         int64
	ModTime      time.Time
	BaseName     string
	Ext          string
	Category     FileCategory
	ContentType  string
	AgentRole    AgentRole
	AgentID      string
	AnalysisTool AnalysisTool
}

func (b *Bundle) Lookup(relPath string) (FileInfo, bool) {
	for _, file := range b.Files {
		if file.RelPath == relPath {
			return file, true
		}
	}
	return FileInfo{}, false
}

func (b *Bundle) Cleanup() error {
	if b.cleanupDir == "" {
		return nil
	}
	return os.RemoveAll(b.cleanupDir)
}
