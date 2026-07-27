# nomad-debug-helper

`nomad-debug-helper` is a local explorer for Nomad debug captures.

This initial `v0` focuses on a thin vertical slice:

- open a `nomad operator debug -output` directory or `.tar.gz` archive
- detect likely bundle layout
- inventory and classify files
- browse an overview page and raw files in a local web UI
- analyze pprof profiles (`go tool pprof`) and execution traces (`go tool trace`) directly from the file browser
- explore captured metrics in Grafana, via a bundled dashboard backed by Grafana's Infinity JSON datasource (no Prometheus/promtool dependency)

Grafana is required for the metrics view (`brew install grafana`, or point
`-homepath` at your install). Tested against Grafana 13.1.1, which ships a
single unified `grafana` binary (`grafana server`, `grafana cli`) rather than
the older separate `grafana-server`/`grafana-cli` binaries; both forms are
supported.

## Usage

```bash
go run ./cmd/nomad-debug-helper -listen 127.0.0.1:7676 /path/to/nomad-debug-output
```

Or point it directly at a customer-provided archive:

```bash
go run ./cmd/nomad-debug-helper -listen 127.0.0.1:7676 /path/to/nomad-debug-2026-01-22-183255Z.tar.gz
```

Then open `http://127.0.0.1:7676`.

## Current scope

- directory or `.tar.gz` / `.tgz` input
- best-effort metadata discovery
- raw file browser for JSON, text, logs, and other readable artifacts
- archive input is extracted into a temporary working directory and cleaned up on exit

Deeper Nomad-specific parsing can layer on top of this scaffold.
