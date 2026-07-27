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

	port, err := s.launchAnalysisTool(file)
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
