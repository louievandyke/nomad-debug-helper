package web

import (
	"fmt"
	"math/rand"
	"net/http"
	"os/exec"
	"time"

	"github.com/louie/nomad-debug-helper/internal/bundle"
)

// handleAnalyze launches `go tool pprof` or `go tool trace` against a bundle
// file and redirects the browser to its web UI, mirroring vault-debug-helper's
// pprof.go launcher. Unlike that implementation, the child process is bound
// to the server's shutdown context so it's killed on exit instead of leaking.
//
// Optional query parameter "mode":
//   - (absent) — open the single file normally
//   - "diff"   — open with -diff_base pointing at interval 0 of the same type;
//                shows what grew/shrank between interval 0 and this file
//   - "merge"  — pass all sibling interval files to go tool pprof so they are
//                merged into one aggregate flame graph
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		http.Error(w, "missing path query parameter", http.StatusBadRequest)
		return
	}

	file, ok := s.files[relPath]
	if !ok {
		http.Error(w, "file not found in inventory", http.StatusNotFound)
		return
	}

	if file.AnalysisTool == bundle.AnalysisToolNone {
		http.Error(w, fmt.Sprintf("%s is not a recognized pprof or trace artifact", relPath), http.StatusBadRequest)
		return
	}

	mode := r.URL.Query().Get("mode")

	var port int
	var err error
	switch mode {
	case "diff":
		port, err = s.launchDiff(file)
	case "merge":
		port, err = s.launchMerge(file)
	default:
		port, err = s.launchAnalysisTool(file)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Give the tool's own HTTP server a moment to come up before redirecting,
	// same fixed delay vault-debug-helper uses for its pprof/Grafana launches.
	time.Sleep(2 * time.Second)
	http.Redirect(w, r, fmt.Sprintf("http://127.0.0.1:%d", port), http.StatusFound)
}

func (s *Server) launchAnalysisTool(file bundle.FileInfo) (int, error) {
	port := 20000 + rand.Intn(45535)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	var cmd *exec.Cmd
	switch file.AnalysisTool {
	case bundle.AnalysisToolPprof:
		cmd = exec.CommandContext(s.ctx, "go", "tool", "pprof", "-http", addr, "-no_browser", file.AbsPath)
	case bundle.AnalysisToolTrace:
		// go tool trace has no -no_browser equivalent (confirmed via
		// `go tool trace -h`): it always opens its own browser tab in
		// addition to the redirect below, so expect two tabs for traces.
		cmd = exec.CommandContext(s.ctx, "go", "tool", "trace", "-http="+addr, file.AbsPath)
	default:
		return 0, fmt.Errorf("%s has no supported analysis tool", file.RelPath)
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", file.AnalysisTool, err)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = cmd.Wait()
	}()

	return port, nil
}

// launchDiff opens `go tool pprof -diff_base <interval0> <file>` so the flame
// graph shows what grew (positive) or shrank (negative) relative to interval 0.
// Only meaningful for pprof files; trace files do not support -diff_base.
func (s *Server) launchDiff(file bundle.FileInfo) (int, error) {
	if file.AnalysisTool != bundle.AnalysisToolPprof {
		return 0, fmt.Errorf("diff mode is only supported for pprof profiles, not %s", file.AnalysisTool)
	}

	siblings := bundle.PprofSiblings(s.raw.Files, file)
	if len(siblings) == 0 {
		return 0, fmt.Errorf("%s has no sibling interval files to diff against", file.RelPath)
	}
	base := siblings[0] // lowest RelPath = interval 0000
	if base.RelPath == file.RelPath {
		return 0, fmt.Errorf("%s is already interval 0; nothing to diff against", file.RelPath)
	}

	port := 20000 + rand.Intn(45535)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.CommandContext(s.ctx, "go", "tool", "pprof",
		"-http", addr, "-no_browser",
		"-diff_base", base.AbsPath,
		file.AbsPath,
	)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start pprof diff: %w", err)
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = cmd.Wait()
	}()
	return port, nil
}

// launchMerge opens `go tool pprof` with all sibling interval files so they
// are automatically merged into one aggregate profile.
func (s *Server) launchMerge(file bundle.FileInfo) (int, error) {
	if file.AnalysisTool != bundle.AnalysisToolPprof {
		return 0, fmt.Errorf("merge mode is only supported for pprof profiles, not %s", file.AnalysisTool)
	}

	siblings := bundle.PprofSiblings(s.raw.Files, file)
	if len(siblings) < 2 {
		return 0, fmt.Errorf("%s has fewer than 2 sibling interval files to merge", file.RelPath)
	}

	port := 20000 + rand.Intn(45535)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	args := []string{"tool", "pprof", "-http", addr, "-no_browser"}
	for _, s := range siblings {
		args = append(args, s.AbsPath)
	}
	cmd := exec.CommandContext(s.ctx, "go", args...)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start pprof merge: %w", err)
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = cmd.Wait()
	}()
	return port, nil
}
