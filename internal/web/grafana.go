package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/louie/nomad-debug-helper/internal/metrics"
)

//go:embed dashboards
var dashboardFS embed.FS

const (
	templateFrom          = "{{TEMPLATE_TIME_FROM}}"
	templateTo            = "{{TEMPLATE_TIME_TO}}"
	infinityDatasourceUID = "default-infinity"
	infinityPluginID      = "yesoreyeram-infinity-datasource"
	infinityPluginVersion = "3.4.1"
)

// handleMetrics lazily launches Grafana (once per process, guarded by
// grafanaMu) and redirects to its dashboard list, mirroring vault-debug-helper
// production's MetricsHandler pattern of reusing one Grafana instance for the
// whole session instead of spawning one per request.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	url, first, err := s.ensureGrafana()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if first {
		time.Sleep(2 * time.Second)
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) ensureGrafana() (string, bool, error) {
	s.grafanaMu.Lock()
	defer s.grafanaMu.Unlock()

	if s.grafanaURL != "" {
		return s.grafanaURL, false, nil
	}

	collections, err := s.loadMetrics()
	if err != nil {
		return "", false, err
	}
	fromTime, toTime := metricsTimeRange(collections)

	workDir, err := os.MkdirTemp("", "nomad-debug-helper-grafana-*")
	if err != nil {
		return "", false, fmt.Errorf("create grafana work dir: %w", err)
	}
	// Removed synchronously by Cleanup(), not by a ctx.Done() goroutine:
	// verified live that a fire-and-forget goroutine here races os.Exit and
	// isn't guaranteed to run before the process terminates, since it isn't
	// tracked by the WaitGroup the shutdown path actually waits on.
	s.grafanaDir = workDir

	grafPort := 20000 + rand.Intn(45535)
	launcher := &grafanaLauncher{
		workDir:  workDir,
		homepath: s.homepath,
		port:     grafPort,
		httpPort: s.selfPort,
		fromTime: fromTime,
		toTime:   toTime,
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := launcher.start(s.ctx); err != nil {
			if s.ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "grafana: %v\n", err)
			}
		}
	}()

	s.grafanaURL = fmt.Sprintf("http://127.0.0.1:%d/dashboards", grafPort)
	return s.grafanaURL, true, nil
}

// metricsTimeRange derives the dashboard's default time window directly from
// the captured metric timestamps, rather than depending on a manifest field:
// Nomad's index.json is a flat file-path array with no start/duration field
// (unlike Vault's), and cluster/cli-flags.json's "duration" value isn't
// guaranteed present on every bundle (e.g. client-only captures).
func metricsTimeRange(collections metrics.Collections) (time.Time, time.Time) {
	var earliest, latest time.Time
	for _, c := range collections {
		t, err := time.Parse("2006-01-02 15:04:05 +0000 UTC", c.Timestamp)
		if err != nil {
			continue
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
		if latest.IsZero() || t.After(latest) {
			latest = t
		}
	}

	if earliest.IsZero() {
		earliest = time.Now().Add(-15 * time.Minute)
	}
	if !latest.After(earliest) {
		latest = earliest.Add(15 * time.Minute)
	}
	return earliest, latest
}

type grafanaLauncher struct {
	workDir  string
	homepath string
	port     int
	httpPort int
	fromTime time.Time
	toTime   time.Time
}

// start provisions and runs Grafana, blocking until it exits or ctx is
// cancelled. Adapted from vault-debug-helper production's monitoring.go, with
// two changes: the datasource points at this process's own /api/metrics/json
// instead of a separate Prometheus, and the server launch itself falls back
// between the modern unified `grafana server` binary and the legacy
// `grafana-server` binary (confirmed: current Homebrew grafana 13.1.1 only
// ships the unified binary, so vault-debug-helper-main's hardcoded
// "grafana-server" call would fail here).
func (g *grafanaLauncher) start(ctx context.Context) error {
	grafanaDir := filepath.Join(g.workDir, "grafana")
	dataDir := filepath.Join(grafanaDir, "data")
	logsDir := filepath.Join(grafanaDir, "log")
	pluginsDir := filepath.Join(dataDir, "plugins")
	dashboardDir := filepath.Join(grafanaDir, "dashboards")
	provisionDir := filepath.Join(grafanaDir, "provisioning")
	provPluginsDir := filepath.Join(provisionDir, "plugins")
	provNotifiersDir := filepath.Join(provisionDir, "notifiers")
	provDatasourcesDir := filepath.Join(provisionDir, "datasources")
	provDashboardsDir := filepath.Join(provisionDir, "dashboards")

	makeDirs := []string{dataDir, logsDir, pluginsDir, dashboardDir, provDashboardsDir, provPluginsDir, provNotifiersDir, provDatasourcesDir}
	for _, dir := range makeDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	dashboards, err := dashboardFS.ReadDir("dashboards")
	if err != nil {
		return err
	}
	for _, dash := range dashboards {
		if dash.IsDir() {
			continue
		}
		contents, err := dashboardFS.ReadFile(filepath.Join("dashboards", dash.Name()))
		if err != nil {
			return err
		}

		templated := string(contents)
		templated = strings.ReplaceAll(templated, templateFrom, g.fromTime.Format(time.RFC3339))
		templated = strings.ReplaceAll(templated, templateTo, g.toTime.Format(time.RFC3339))

		if err := os.WriteFile(filepath.Join(dashboardDir, dash.Name()), []byte(templated), 0o644); err != nil {
			return err
		}
	}

	if err := g.ensurePlugin(ctx, pluginsDir); err != nil {
		return err
	}

	dataSources := strings.Join([]string{
		"apiVersion: 1",
		"datasources:",
		"- name: default-infinity",
		fmt.Sprintf("  uid: %s", infinityDatasourceUID),
		"  type: " + infinityPluginID,
		"  orgId: 1",
		fmt.Sprintf("  url: http://127.0.0.1:%d", g.httpPort),
		"  isDefault: true",
		"  version: 1",
		"  editable: true",
		"  jsonData:",
		fmt.Sprintf("    url: http://127.0.0.1:%d/api/metrics/json", g.httpPort),
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(provDatasourcesDir, "infinity.yml"), []byte(dataSources), 0o644); err != nil {
		return fmt.Errorf("write grafana datasource cfg: %w", err)
	}

	dashboardsCfg := "\napiVersion: 1\nproviders:\n- name: default\n  orgId: 1\n  folder: debug-metrics\n  type: file\n  disableDeletion: false\n  updateIntervalSeconds: 60\n  editable: true\n  options:\n    path: " + dashboardDir + "\n"
	if err := os.WriteFile(filepath.Join(provDashboardsDir, "dashboards.yml"), []byte(dashboardsCfg), 0o644); err != nil {
		return fmt.Errorf("write grafana dashboard cfg: %w", err)
	}

	cmd, err := g.serverCommand(ctx, provisionDir, dataDir, logsDir)
	if err != nil {
		return err
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("grafana error=%v, output: %s", err, out)
	}
	return nil
}

func (g *grafanaLauncher) serverCommand(ctx context.Context, provisionDir, dataDir, logsDir string) (*exec.Cmd, error) {
	env := append(os.Environ(),
		"GF_PATHS_PROVISIONING="+provisionDir,
		"GF_PATHS_DATA="+dataDir,
		"GF_PATHS_LOGS="+logsDir,
		"GF_PATHS_PLUGINS="+filepath.Join(dataDir, "plugins"),
		"GF_AUTH_DISABLE_LOGIN_FORM=true",
		"GF_AUTH_ANONYMOUS_ENABLED=true",
		"GF_AUTH_ANONYMOUS_ORG_ROLE=Admin",
		"GF_SERVER_HTTP_PORT="+strconv.Itoa(g.port),
	)

	if path, err := exec.LookPath("grafana"); err == nil {
		cmd := exec.CommandContext(ctx, path, "server", "--homepath", g.homepath)
		cmd.Env = env
		configureProcessGroup(cmd)
		return cmd, nil
	}
	if path, err := exec.LookPath("grafana-server"); err == nil {
		cmd := exec.CommandContext(ctx, path, "-homepath", g.homepath)
		cmd.Env = env
		configureProcessGroup(cmd)
		return cmd, nil
	}
	return nil, fmt.Errorf("could not find a grafana or grafana-server executable on PATH")
}

func (g *grafanaLauncher) ensurePlugin(ctx context.Context, pluginsDir string) error {
	installedVersion, err := installedPluginVersion(pluginsDir, infinityPluginID)
	if err != nil {
		return err
	}
	if installedVersion == infinityPluginVersion {
		return nil
	}
	if installedVersion != "" {
		if err := os.RemoveAll(filepath.Join(pluginsDir, infinityPluginID)); err != nil {
			return fmt.Errorf("remove existing grafana plugin %s version %s: %w", infinityPluginID, installedVersion, err)
		}
	}

	type cliCandidate struct {
		name string
		args []string
	}
	candidates := []cliCandidate{
		{
			name: "grafana",
			args: []string{"cli", "--homepath", g.homepath, "--pluginsDir", pluginsDir, "plugins", "install", infinityPluginID, infinityPluginVersion},
		},
		{
			name: "grafana-cli",
			args: []string{"--homepath", g.homepath, "--pluginsDir", pluginsDir, "plugins", "install", infinityPluginID, infinityPluginVersion},
		},
	}

	var lastErr error
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate.name); err != nil {
			continue
		}

		cmd := exec.CommandContext(ctx, candidate.name, candidate.args...)
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err == nil || strings.Contains(string(out), "already installed") {
			return nil
		}
		lastErr = fmt.Errorf("install grafana plugin %s version %s via %s: %v, output: %s", infinityPluginID, infinityPluginVersion, candidate.name, err, out)
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("could not find a grafana CLI to install plugin %s version %s", infinityPluginID, infinityPluginVersion)
}

func installedPluginVersion(pluginsDir, pluginID string) (string, error) {
	pluginDir := filepath.Join(pluginsDir, pluginID)
	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}

	pluginJSONPath := filepath.Join(pluginDir, "plugin.json")
	contents, err := os.ReadFile(pluginJSONPath)
	if err != nil {
		return "", fmt.Errorf("read grafana plugin metadata %s: %w", pluginJSONPath, err)
	}

	var pluginMetadata struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(contents, &pluginMetadata); err != nil {
		return "", fmt.Errorf("parse grafana plugin metadata %s: %w", pluginJSONPath, err)
	}
	return pluginMetadata.Version, nil
}
