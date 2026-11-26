# About this fork

This repository is a fork of [pubeldev/prusa_exporter](https://github.com/pubeldev/prusa_exporter) (formerly `pstrobl96/prusa_exporter`). It is a rewrite of the exporter with idiomatic Go and Prometheus conventions, and it fixes several metric bugs. It runs on one home server with a Prusa CORE One L.

**Status:** upstream has had no release since 2.0.0 (2025-12), so this fork is maintained on its own. Its releases start at `v3.0.0`, because the metric names and flags differ from upstream 2.0.0. The upstream dashboards and alerts need changes (see "Changes that you see as a user"). The earlier fork releases are `v2.x.y-sabo.N`.

## Branches

| Branch | Content |
| --- | --- |
| `fork` (default) | The exporter that this fork publishes. Releases are tags on this branch. |
| `main` | A copy of upstream `main`. It has no fork changes. |
| `metrics-fixes` | The bug fixes as small commits on upstream `main`, for upstream PRs. `fork` contains them too. |

To see all differences from upstream:

```bash
git remote add upstream https://github.com/pubeldev/prusa_exporter.git
git fetch upstream
git log --oneline upstream/main..fork
```

## Changes that you see as a user

- Renamed metrics and removed flags: see "Changes from 2.0.0" in the [README](../README.md#changes-from-200). [docs/metrics.md](metrics.md) lists all metrics. A test generates it from the real output.
- Bug fixes in the metric values (also on `metrics-fixes`):
  - `prusa_nozzle_size_meters` is in meters.
  - `prusa_up` needs a successful status request and a successful info request.
  - A scrape of a printer that does not answer ends at a shared deadline.
  - With Loki off, the exporter does not download the job thumbnail.
  - UDP metrics: each tag set is its own series. Error points are dropped and counted. A series with no new point is deleted after a TTL. Events are counters, string metrics are info series, and the counters start at 0.
- New: `/metrics` has the metrics of the exporter itself, `--web.enable-pprof`, `--log.format=json` (ECS), alert rules with tests in [alerts/](../alerts/).
- The root page shows the correct version.

## Install

- Binaries for Linux (amd64, arm64, armv7, riscv64) and macOS: the [releases of this fork](https://github.com/anyasabo/prusa_exporter/releases). Each release has `checksums.txt` and an SPDX SBOM for each binary. Releases after `v2.2.0-sabo.1` also have `third_party_licenses.csv`.
- With Go: `go install github.com/anyasabo/prusa_exporter/v3@latest`. The module path has the suffix `/v3`. Go cannot install the `v2.x.y-sabo.N` tags, because their module path has no `/v2`.
- Container image: `ghcr.io/anyasabo/prusa_exporter:VERSION` (distroless, non-root).
- The builds are reproducible: the same commit gives the same binary.

## Checks

`mise run ci` runs the linters, the license check, the tests with the race detector and the alert rule tests. CI also runs the fuzz tests and `govulncheck`. `govulncheck` only reports: a new advisory does not fail an unrelated change. CI runs each Monday for new advisories.

The exporter is AGPL-3.0-only. `mise run licenses` fails for a dependency with no known license, or with a license that AGPL-3.0 does not allow. It allows permissive licenses, MPL-2.0, LGPL, GPL-3.0 and AGPL-3.0. It does not allow GPL-2.0-only.

## Upstream

Upstream has had no push since 2026-08-25, and outside PRs since March 2026 have no review. The plan: first ask the maintainer whether a PR is welcome, then send the fixes of `metrics-fixes` as small PRs. The rewrite on `fork` is too large for one PR.
