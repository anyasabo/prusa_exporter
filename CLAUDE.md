# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

prusa_exporter is a Prometheus exporter for Prusa 3D printers. This repo is the fork
`anyasabo/prusa_exporter` of `pubeldev/prusa_exporter`. The fork diverges on purpose: the module
path is `github.com/anyasabo/prusa_exporter`, and the code structure, flags and metric names are
different from upstream. `main` of the fork follows upstream. Do the work on a work branch.

## Commands

mise pins all tools (`mise.toml`). CI runs the same tasks, so a task that passes locally passes in CI.

```
mise install                 # install the pinned tools
mise run ci                  # lint + test + vulncheck, the same as CI
mise run lint                # golangci-lint, go mod tidy -diff, goreleaser check, actionlint, zizmor
mise run test                # go test -race -shuffle=on with coverage
mise run fmt                 # gofumpt and goimports through golangci-lint
mise run fuzz                # FuzzParsePoint and FuzzParseMessage (FUZZTIME, default 30s)
mise run build               # binary for this platform in dist/
mise run release-dry-run     # all binaries, checksums and SBOMs in dist/, no publish
```

One test: `mise exec -- go test -run 'TestExporterPrinting' ./internal/prusalink`.
Benchmarks: `mise exec -- go test -run '^$' -bench . ./internal/udp`.
Run the exporter: `mise exec -- go run . --config.file=prusa.yml --no-udp.gcode-enabled`.

A tag `vX.Y.Z` starts `.github/workflows/release.yml`: GoReleaser makes the GitHub release
(binaries `prusa_exporter-OS-ARCH`, `checksums.txt`, SBOMs), and ko pushes a distroless image to
ghcr.io. The builds are reproducible. Renovate updates `go.mod`, `mise.toml`, the action SHAs and
the ko base image digest in `.goreleaser.yaml`.

## Architecture

`main.go` parses the kingpin flags, loads `prusa.yml` (`internal/config`) and starts two
independent data paths. Each path has its own HTTP endpoint and registry:

| Endpoint | Data | Source |
| --- | --- | --- |
| `/metrics/prusalink` | printer state from the PrusaLink HTTP API | pull, at each scrape |
| `/metrics/udp` | the metrics that the firmware sends | push, UDP syslog datagrams |
| `/metrics` | the exporter itself (`prusa_exporter_*`, Go, process, build info) | self registry |

All self-instrumentation registers on the self registry, which `main.go` gives to the constructors.
There is no global state and no default registry.

### PrusaLink path (`internal/prusalink`)

- `Client` is one HTTP client for each printer, for the life of the program. The digest transport
  keeps the challenge, so only the first request gets a 401. An instrumented transport records
  each request on the wire (`prusa_exporter_prusalink_request_duration_seconds`).
- `Exporter.Handler()` makes a new registry for each scrape, with a `scrape` collector that has the
  request context. The time budget is the smaller of `prusalink_scrape_budget` and the header
  `X-Prometheus-Scrape-Timeout-Seconds`. The printers are scraped in parallel.
- The metrics are a table (`allMetrics` in `collector.go`). Metrics with `common: true` get the
  label list from `prusalink.common_labels`. The others always have the printer labels.
- If all requests of the scrape succeed, `prusa_up` is 1. Else it is 0. `/api/job` is requested only if
  `/api/v1/status` has a job.
- `gcode.go` uploads and starts a G-code that sets the printer's metrics host to this exporter.
  `RunGcode` tries again until it succeeds. The metric lists in `gcode_metrics.go` are written
  by hand from the firmware.
- `loki.go` pushes the job thumbnail to Loki: one time for each job, and again each 5 minutes.

### UDP path (`internal/udp`)

- The firmware format is in Prusa-Firmware-Buddy `src/common/metric_handlers.cpp`:
  `<14>1 - MAC buddy - - - msg=N,tm=T,v=4 ` and then one InfluxDB line protocol point on each
  line, with a time offset in µs. `syslog.go` parses the header and `lineproto.go` parses the
  points.
- `Collector` keeps the last value of each series and makes const metrics at each scrape. It is
  an unchecked collector (`Describe` sends nothing), because one metric name can have different
  label sets.
- The field type sets the metric type. A number is a gauge. A bool `v=T` is an event, counted in
  `<name>_total`. A string is `<name>_info` with the label `value`. A point with only an `error`
  field goes to `prusa_udp_point_errors_total`. An integer field `n` of an event metric is the
  printer's own event count, and after it the `v=T` points stop changing that counter.
- Gauges and info series expire after `udp_series_ttl`. Counters do not expire.
  `udp_max_series` limits the series count.
- **The datagram path must stay almost free of allocations**, because the exporter can run on a
  very small host. `TestHandleAllocations` allows 1 allocation for each datagram. The parsed
  strings refer to the datagram, and the collector writes keys into byte buffers and looks them up
  with `m[string(buf)]`. Copy (`strings.Clone`) each string that the collector keeps, and build
  the help text only for a new series.

## Things that are easy to break

- **Metric names and labels are a contract.** The dashboards in `docs/dashboards/` and the
  homeserver (`~/dotfiles/homeserver`: dashboards, alert rules, `scripts/print-recorder`) query
  them. The tests compare full expositions with `testutil.CollectAndCompare`, so a rename
  changes a test. Then update the README metric table, "Changes from 2.0.0" and the dashboards.
- `prusa.yml` is strict: an unknown key is an error. `exporter.scrape_timeout` and
  `exporter.log_level` are accepted and ignored, because old files have them.
  `docs/config/prusa.yml` must stay valid (`TestSampleConfig`).
- The label names are constants: `config.Label*` for PrusaLink and Loki, `labelMAC` and
  `labelAddress` in `internal/udp`.
- The PrusaLink test fixtures in `internal/prusalink/testdata/` follow the Buddy firmware
  (`lib/WUI/link_content/basic_gets.cpp`, `src/state/printer_state.cpp`). The fake server in
  `fake_test.go` implements digest auth and counts the challenges.

## Lint rules that shape the code

The linters are in `.golangci.yml`. The ones that affect how to write code:

- `wrapcheck`: wrap each error from another module with context. Errors from our own packages,
  `http.Client.Do`, `errgroup.Wait` and a `RoundTripper` that passes the error through are
  exempt.
- `sloglint`: key-value pairs, snake_case keys, constant messages, no global logger. Give
  loggers as parameters, not in a context.
- `nolintlint`: each `//nolint` must have a reason and must be necessary.
- `contextcheck`: give a context down the call chain. Do not make `context.Background()` in a
  callee.
