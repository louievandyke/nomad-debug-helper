package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/louie/nomad-debug-helper/internal/bundle"
	"github.com/louie/nomad-debug-helper/internal/layout"
	"github.com/louie/nomad-debug-helper/internal/metrics"
	"github.com/louie/nomad-debug-helper/internal/model"
)

const previewLimit = 512 * 1024

type Server struct {
	ctx          context.Context
	wg           *sync.WaitGroup
	raw          *bundle.Bundle
	view         *model.Bundle
	files        map[string]bundle.FileInfo
	homepath     string
	selfPort     int
	overviewTmpl *template.Template
	filesTmpl    *template.Template

	metricsMu    sync.Mutex
	metricsCache metrics.Collections
	metricsErr   error
	metricsRead  bool

	grafanaMu  sync.Mutex
	grafanaURL string
	grafanaDir string
}

// Cleanup removes the Grafana working directory if one was created. Called
// synchronously after the HTTP server and background subprocesses have
// stopped, so it isn't racing process exit.
func (s *Server) Cleanup() error {
	s.grafanaMu.Lock()
	defer s.grafanaMu.Unlock()

	if s.grafanaDir == "" {
		return nil
	}
	return os.RemoveAll(s.grafanaDir)
}

type filePageData struct {
	SourcePath string
	RootPath   string
	Layout     layout.Info
	Bundle     *model.Bundle
	Files      []model.File
	Current    filePreview
}

type filePreview struct {
	Path         string
	Content      string
	Preview      bool
	Binary       bool
	Truncated    bool
	Error        string
	AnalyzeURL   string
	AnalyzeLabel string
}

func NewServer(ctx context.Context, wg *sync.WaitGroup, raw *bundle.Bundle, view *model.Bundle, homepath string, selfPort int) (*Server, error) {
	files := make(map[string]bundle.FileInfo, len(raw.Files))
	for _, file := range raw.Files {
		files[file.RelPath] = file
	}

	overviewTmpl, err := template.New("overview").Parse(baseTemplate + overviewTemplate)
	if err != nil {
		return nil, err
	}

	filesTmpl, err := template.New("files").Parse(baseTemplate + filesTemplate)
	if err != nil {
		return nil, err
	}

	return &Server{
		ctx:          ctx,
		wg:           wg,
		raw:          raw,
		view:         view,
		files:        files,
		homepath:     homepath,
		selfPort:     selfPort,
		overviewTmpl: overviewTmpl,
		filesTmpl:    filesTmpl,
	}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleOverview)
	mux.HandleFunc("/files", s.handleFiles)
	mux.HandleFunc("/analyze", s.handleAnalyze)
	mux.HandleFunc("/api/metrics/json", s.handleMetricsJSON)
	mux.HandleFunc("/metrics", s.handleMetrics)
	return mux
}

// loadMetrics reads and caches interval/*/metrics.json once per process,
// since Grafana panels re-poll this endpoint on every dashboard refresh.
func (s *Server) loadMetrics() (metrics.Collections, error) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()

	if s.metricsRead {
		return s.metricsCache, s.metricsErr
	}

	s.metricsCache, s.metricsErr = metrics.Load(s.raw.RootPath)
	s.metricsRead = true
	return s.metricsCache, s.metricsErr
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if err := s.overviewTmpl.ExecuteTemplate(w, "overview", s.view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	data := filePageData{
		SourcePath: s.view.SourcePath,
		RootPath:   s.view.RootPath,
		Layout:     s.view.Layout,
		Bundle:     s.view,
		Files:      s.view.Files,
	}
	if relPath := r.URL.Query().Get("path"); relPath != "" {
		data.Current = s.preview(relPath)
	}

	if err := s.filesTmpl.ExecuteTemplate(w, "files", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) preview(relPath string) filePreview {
	file, ok := s.files[relPath]
	if !ok {
		return filePreview{
			Path:  relPath,
			Error: "file not found in inventory",
		}
	}

	preview := filePreview{
		Path:    relPath,
		Preview: true,
	}

	if file.Category == bundle.CategoryPprof || file.Category == bundle.CategoryBinary {
		preview.Binary = true
		if label := model.AnalyzeLabel(file.AnalysisTool); label != "" {
			preview.AnalyzeLabel = label
			preview.AnalyzeURL = "/analyze?path=" + url.QueryEscape(relPath)
		}
		return preview
	}

	content, err := os.ReadFile(file.AbsPath)
	if err != nil {
		preview.Error = fmt.Sprintf("read failed: %v", err)
		return preview
	}

	if len(content) > previewLimit {
		content = content[:previewLimit]
		preview.Truncated = true
	}

	if file.ContentType == "application/json" {
		var decoded any
		if err := json.Unmarshal(content, &decoded); err == nil {
			formatted, err := json.MarshalIndent(decoded, "", "  ")
			if err == nil {
				preview.Content = string(formatted)
				return preview
			}
		}
	}

	preview.Content = strings.TrimSpace(string(content))
	return preview
}

const baseTemplate = `
{{define "base"}}
<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>{{if eq .Layout.Product "consul"}}consul-debug-helper{{else}}nomad-debug-helper{{end}}</title>
    <style>
      :root {
        color-scheme: light;
        --bg: #f5f1e8;
        --panel: #fffaf0;
        --ink: #1e1a16;
        --muted: #6f675f;
        --line: #d8cfc2;
        --accent: #9c4f2f;
        --accent-soft: #f2d8cb;
      }
      body {
        margin: 0;
        font-family: "Iowan Old Style", "Palatino Linotype", "Book Antiqua", serif;
        color: var(--ink);
        background:
          radial-gradient(circle at top right, #f7dfc6 0, transparent 30%),
          linear-gradient(180deg, #efe7db, var(--bg));
      }
      a {
        color: var(--accent);
        text-decoration: none;
      }
      a:hover {
        text-decoration: underline;
      }
      .shell {
        max-width: 1180px;
        margin: 0 auto;
        padding: 24px;
      }
      .masthead {
        display: flex;
        justify-content: space-between;
        align-items: baseline;
        gap: 16px;
        margin-bottom: 24px;
      }
      .title {
        font-size: 32px;
        font-weight: 700;
      }
      .subtitle {
        color: var(--muted);
      }
      .nav {
        display: flex;
        gap: 16px;
      }
      .panel {
        background: color-mix(in srgb, var(--panel) 94%, white 6%);
        border: 1px solid var(--line);
        border-radius: 18px;
        padding: 18px 20px;
        box-shadow: 0 10px 30px rgba(40, 20, 0, 0.06);
        margin-bottom: 18px;
      }
      .grid {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
        gap: 12px;
      }
      .stat {
        padding: 12px;
        border-radius: 12px;
        background: rgba(156, 79, 47, 0.07);
      }
      .eyebrow {
        display: block;
        color: var(--muted);
        font-size: 12px;
        text-transform: uppercase;
        letter-spacing: 0.08em;
        margin-bottom: 6px;
      }
      .value {
        font-size: 20px;
        font-weight: 600;
      }
      .table {
        width: 100%;
        border-collapse: collapse;
      }
      .table th,
      .table td {
        text-align: left;
        padding: 10px 8px;
        border-bottom: 1px solid var(--line);
        vertical-align: top;
      }
      .pill {
        display: inline-block;
        padding: 4px 8px;
        border-radius: 999px;
        background: var(--accent-soft);
        font-size: 12px;
      }
      pre {
        margin: 0;
        white-space: pre-wrap;
        word-break: break-word;
        font-family: "SFMono-Regular", "Menlo", monospace;
        font-size: 13px;
      }
      .warning {
        color: #7b341e;
      }
      .muted {
        color: var(--muted);
      }
    </style>
  </head>
  <body>
    <div class="shell">
      <div class="masthead">
        <div>
          <div class="title">{{if eq .Layout.Product "consul"}}consul-debug-helper{{else}}nomad-debug-helper{{end}}</div>
          <div class="subtitle">{{.SourcePath}}</div>
          {{if ne .SourcePath .RootPath}}
          <div class="subtitle">Resolved bundle root: {{.RootPath}}</div>
          {{end}}
        </div>
        <div class="nav">
          <a href="/">Overview</a>
          <a href="/files">Raw Files</a>
          <a href="/metrics" target="_blank">Metrics</a>
        </div>
      </div>
      {{block "content" .}}{{end}}
    </div>
  </body>
</html>
{{end}}
`

const overviewTemplate = `
{{define "overview"}}
  {{template "base" .}}
{{end}}

{{define "content"}}
  <section class="panel">
    <div class="grid">
      <div class="stat">
        <span class="eyebrow">Layout</span>
        <div class="value">{{.Layout.Kind}}</div>
      </div>
      <div class="stat">
        <span class="eyebrow">Confidence</span>
        <div class="value">{{.Layout.Confidence}}</div>
      </div>
      <div class="stat">
        <span class="eyebrow">Source</span>
        <div class="value">{{.SourceKind}}</div>
      </div>
      <div class="stat">
        <span class="eyebrow">{{if eq .Layout.Product "consul"}}Consul Version{{else}}Nomad Version{{end}}</span>
        <div class="value">{{if .Metadata.AgentVersion}}{{.Metadata.AgentVersion}}{{else}}unknown{{end}}</div>
      </div>
      {{if eq .Layout.Product "consul"}}
      {{if .Metadata.Datacenter}}
      <div class="stat">
        <span class="eyebrow">Datacenter</span>
        <div class="value">{{.Metadata.Datacenter}}</div>
      </div>
      {{end}}
      {{if .Metadata.NodeName}}
      <div class="stat">
        <span class="eyebrow">Node</span>
        <div class="value">{{.Metadata.NodeName}}</div>
      </div>
      {{end}}
      {{if .Metadata.DebugDuration}}
      <div class="stat">
        <span class="eyebrow">Capture Duration</span>
        <div class="value">{{.Metadata.DebugDuration}}</div>
      </div>
      {{end}}
      {{if .Metadata.DebugInterval}}
      <div class="stat">
        <span class="eyebrow">Interval</span>
        <div class="value">{{.Metadata.DebugInterval}}</div>
      </div>
      {{end}}
      {{end}}
    </div>
  </section>

  <section class="panel">
    <h2>File Categories</h2>
    <table class="table">
      <thead>
        <tr>
          <th>Category</th>
          <th>Count</th>
        </tr>
      </thead>
      <tbody>
        {{range .Layout.Counts}}
        <tr>
          <td><span class="pill">{{.Category}}</span></td>
          <td>{{.Count}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </section>

  <section class="panel">
    <h2>Metadata Files</h2>
    {{if .Metadata.MetadataFiles}}
    <table class="table">
      <thead>
        <tr>
          <th>Path</th>
          <th>Status</th>
        </tr>
      </thead>
      <tbody>
        {{range .Metadata.Documents}}
      <tr>
        <td><a href="/files?path={{.Path}}">{{.Path}}</a></td>
        <td>{{if .Error}}{{if eq .Error "array (not a metadata object)"}}<span class="muted">{{.Error}}</span>{{else}}<span class="warning">{{.Error}}</span>{{end}}{{else}}parsed{{end}}</td>
      </tr>
      {{end}}
      </tbody>
    </table>
    {{else}}
    <div class="muted">No obvious metadata files were discovered.</div>
    {{end}}
  </section>

  {{if .Warnings}}
  <section class="panel">
    <h2>Warnings</h2>
    <ul>
      {{range .Warnings}}
      <li class="warning">{{.}}</li>
      {{end}}
    </ul>
  </section>
  {{end}}

  <section class="panel">
    <h2>Detection Notes</h2>
    <ul>
      {{range .Layout.Notes}}
      <li>{{.}}</li>
      {{end}}
    </ul>
  </section>
{{end}}
`

const filesTemplate = `
{{define "files"}}
  {{template "base" .}}
{{end}}

{{define "content"}}
  <section class="panel">
    <h2>Raw Files</h2>
    <table class="table">
      <thead>
        <tr>
          <th>Path</th>
          <th>Category</th>
          <th>Size</th>
          <th>Agent</th>
          <th>Action</th>
        </tr>
      </thead>
      <tbody>
        {{range .Files}}
        <tr>
          <td><a href="/files?path={{.Path}}">{{.Path}}</a></td>
          <td><span class="pill">{{.Category}}</span></td>
          <td>{{.SizeHuman}}</td>
          <td>{{if and .AgentID (ne .AgentRole "unknown")}}{{.AgentRole}}/{{.AgentID}}{{else}}<span class="muted">n/a</span>{{end}}</td>
          <td>
            {{if .AnalyzeURL}}<a href="{{.AnalyzeURL}}" target="_blank">{{.AnalyzeLabel}}</a>{{end}}
            {{if .MergeURL}}&nbsp;·&nbsp;<a href="{{.MergeURL}}" target="_blank" title="Merge all intervals into one flame graph">Merge all</a>{{end}}
            {{if .DiffURL}}&nbsp;·&nbsp;<a href="{{.DiffURL}}" target="_blank" title="Diff vs interval 0 — shows what grew or shrank">Diff vs 0</a>{{end}}
            {{if not .AnalyzeURL}}<span class="muted">n/a</span>{{end}}
          </td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </section>

  {{if .Current.Path}}
  <section class="panel">
    <h2>Preview: {{.Current.Path}}</h2>
    {{if .Current.Error}}
      <div class="warning">{{.Current.Error}}</div>
    {{else if .Current.Binary}}
      {{if .Current.AnalyzeURL}}
      <p><a href="{{.Current.AnalyzeURL}}" target="_blank">{{.Current.AnalyzeLabel}}</a></p>
      <p class="muted">Opens in a new tab. The tool keeps running as a subprocess until nomad-debug-helper exits.</p>
      {{else}}
      <div class="muted">Binary file preview is intentionally skipped for this artifact type.</div>
      {{end}}
    {{else}}
      {{if .Current.Truncated}}<p class="muted">Preview truncated at 512 KiB.</p>{{end}}
      <pre>{{.Current.Content}}</pre>
    {{end}}
  </section>
  {{end}}
{{end}}
`
