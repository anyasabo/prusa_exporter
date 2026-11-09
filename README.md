[![docker](https://img.shields.io/github/actions/workflow/status/pstrobl96/prusa_exporter/docker.yml)](https://github.com/pstrobl96/prusa_exporter/actions/workflows/docker.yml) 
![issues](https://img.shields.io/github/issues/pstrobl96/prusa_exporter) 
![go](https://img.shields.io/github/go-mod/go-version/pstrobl96/prusa_exporter) 
![tag](https://img.shields.io/github/v/tag/pstrobl96/prusa_exporter) 
![license](https://img.shields.io/github/license/pstrobl96/prusa_exporter)

# Table of Contents
- [Table of Contents](#table-of-contents)
- [prusa\_exporter](#prusa_exporter)
  - [UDP metrics](#udp-metrics)
    - [How to manually activate UDP metrics](#how-to-manually-activate-udp-metrics)
    - [UDP counters](#udp-counters)
  - [Prusa Link Metrics](#prusa-link-metrics)
    - [Guide how to get infomration from the printer](#guide-how-to-get-infomration-from-the-printer)
  - [Running](#running)
    - [Docker](#docker)
    - [Standalone](#standalone)
    - [Flags](#flags)
  - [Dashboards](#dashboards)
    - [Prusa MKx (MK3.5(S) / MK3.9(S) / MK4(S))](#prusa-mkx-mk35s--mk39s--mk4s)
    - [Prusa Core One](#prusa-core-one)
    - [Prusa XL](#prusa-xl)
    - [Prusa XL - fancy version for 5 Tools (BETA and WIP)](#prusa-xl---fancy-version-for-5-tools-beta-and-wip)
    - [Prusa Mini](#prusa-mini)
    - [General Overview](#general-overview)
- [Roadmap](#roadmap)
- [FAQ](#faq)
  - [My printer is correctly connected via Prusa Link API but I got no UDP metrics](#my-printer-is-correctly-connected-via-prusa-link-api-but-i-got-no-udp-metrics)
  - [After exporter started my printers returned Warning - The G-code isn't fully compatible](#after-exporter-started-my-printers-returned-warning---the-g-code-isnt-fully-compatible)
  - [After I started the exporter printers wants to print enable\_udp\_metrics.gcode](#after-i-started-the-exporter-printers-wants-to-print-enable_udp_metricsgcode)
  - [My printer has UDP metrics host set as 172.x.x.x](#my-printer-has-udp-metrics-host-set-as-172xxx)
  - [Can I enable all UDP metrics?](#can-i-enable-all-udp-metrics)
  - [Can I get also logs from the printer?](#can-i-get-also-logs-from-the-printer)
  - [Why is there Loki in docker compose? Isn't this exporter of metrics?](#why-is-there-loki-in-docker-compose-isnt-this-exporter-of-metrics)
  - [I don't have Prusa Link metrics. Why?](#i-dont-have-prusa-link-metrics-why)
  - [Why do I have to fill printer model in configuration file?](#why-do-i-have-to-fill-printer-model-in-configuration-file)
  - [Is the docker compose production ready?](#is-the-docker-compose-production-ready)
  - [What metrics are better? UDP or Prusa Link](#what-metrics-are-better-udp-or-prusa-link)
  - [Can I run the exporter on Raspberry Pi?](#can-i-run-the-exporter-on-raspberry-pi)
  - [What UDP metrics are enabled by default?](#what-udp-metrics-are-enabled-by-default)


# prusa_exporter

Prusa Exporter or more known as prusa_exporter is a tool that allows users to expose metrics from the Prusa Research FDM 3D printers. Its approach is to scrape metrics from [Prusa Link](https://help.prusa3d.com/article/prusa-connect-and-prusalink-explained_302608) REST API and also getting [UDP](https://github.com/prusa3d/Prusa-Firmware-Buddy/blob/master/doc/metrics.md) type of metrics. After gettng data it's simply exposes the metrics at `/metrics/prusalink` and `/metrics/udp` endpoints. You can also access `http://localhost:10009`.

**I strongly recommend to connect printers via Ethernet as WiFi is not considered stable**

## UDP metrics

**UDP** is configured in printer - Settings -> Network -> Metrics & Log

By default at start of the exporter will send the configuration gcode to the printers that are configured in prusa.yml. The list is already prepared but you can add additional metrics with the repeatable flag `--udp.extra-metrics`, for example `--udp.extra-metrics=crash --udp.extra-metrics=power_panic`. 

- Host => address where prusa_exporter is running aka your computer / server
- Metrics Port => default 8514 same as prusa_exporter but you can change it
- Enable Metrics => enable
- Metrics List => list of enabled metrics
  - You can select all but it has actual impact on performance so choose wisely


Of course you can configure metrics with gcode as well - that gcode can be found [here](docs/config/config_full.gcode). This list is not complete because there are constant changes in [Prusa-Firmware-Buddy](github.com/prusa3d/Prusa-Firmware-Buddy/). 

### How to manually activate UDP metrics

I've prepared quick guide where you can learn how to manually activate UDP metrics in the printer. You can find it in here, in [UDP.md](docs/readme/udp/UDP.md). If you are using older firmware then you can try to use older guide but beware that it is not supported. That can be found in [UDP_OLD_FW.md](docs/readme/udp_old_fw/UDP_OLD_FW.md).

### UDP counters

When a printer sends its first packet, the exporter makes these counters at 0. Thus a dashboard can tell "no events" from "no data". Counters do not expire.

- `prusa_udp_packets_total{printer_mac,printer_address}`: the UDP datagrams from the printer.
- `prusa_udp_points_total{printer_mac,printer_address}`: the points that the exporter parsed, error points and event points included.
- `prusa_udp_point_errors_all_total{printer_mac,printer_address}`: the error points (`name error="..."`) of all metrics. The exporter drops these points.
- `prusa_udp_point_errors_total{metric,printer_mac,printer_address}`: the error points of each metric. A series starts with the first error of that metric.
- `prusa_<event>_total{printer_mac,printer_address}`: one counter for each event metric in the option `udp_event_metrics` of the `exporter:` section. The default list has all event metrics of the Buddy firmware: `app_start`, `crash_repeated`, `maintask_loop`, `modbus_reqfail`, `power_panic` and `probe_start`. An event metric that is not in the list gets its counter with its first point.

An event metric has two sources for its count:

1. Stock firmware sends one point `name v=T` for each event. The exporter counts these points from the time it starts. The rate limit of the firmware and lost UDP packets can drop events.
2. At a fixed interval, a firmware can also send the count since boot as an integer field `n`: `probe_start n=5i <time>`. If the count is 0, it sends the count too. An event metric is in `udp_event_metrics`, or the exporter received a `v=T` point for it. For an event metric, `prusa_<name>_total` then shows the value of the printer. After the first `n` point of a printer, the exporter ignores the `v=T` points of that metric for that printer. At the switch, the counter can jump to the value of the printer. After a printer restart, the count goes down to 0. `rate()` and `increase()` see this as a counter reset. For a metric that is not an event metric, the field `n` stays the gauge `prusa_<name>_n`.

### Cumulative fields

Some firmware fields are counts since the printer started, for example `stp_stall` (the planner ran out of moves) or the field `t` of `task_cpu` (CPU ms of a task). The option `udp_counter_fields` of the `exporter:` section lists them: a map from the firmware name to its fields, with `v` for a metric without fields. Each one is a counter with the value of the printer:

- `stp_stall v=5i` is `prusa_stp_stall_total 5`.
- `task_cpu,n=IDLE c=230i,t=1000i` is `prusa_task_cpu_t_total{n="IDLE"} 1000` and the gauge `prusa_task_cpu_c{n="IDLE"} 230`.

After a printer restart, the value goes down to 0, and `rate()` and `increase()` see a counter reset. These counters do not expire. If the option is not set, the exporter uses the list `DefaultUDPCounterFields` in `internal/config/config.go`. It has the counters of the Buddy firmware and of the fork `anyasabo/Prusa-Firmware-Buddy`. `TestFirmwareReference` checks each name and type against the firmware builds in `docs/firmware/`. `{}` makes all fields gauges, as before this option.

## Prusa Link Metrics

**Prusa Link** is configured with [prusa.yml](docs/config/prusa.yml) where you need to fill - Settings -> Network -> PrusaLink

- `address` of the printer
- `apikey`, or `username` (default `maker`) and `password` for Prusa Link
- `name` of the printer
- `type` - model of the printer, for example `MK4S`, `XL` or `Core One`. The dashboards filter by it.

The exporter reads the PrusaLink API at each scrape. The requests for one printer stop after `prusalink_scrape_budget` (default 8 s), or before the scrape timeout of the scraper (header `X-Prometheus-Scrape-Timeout-Seconds`), whichever is first. A key in `prusa.yml` that the exporter does not know is an error.

[docs/metrics.md](docs/metrics.md) lists all metrics with their types and labels.

Most PrusaLink metrics have the labels in `prusalink.common_labels`. The default is `printer_address`, `printer_model` and `printer_name`. The job labels `printer_job_name` and `printer_job_path` make new series for each print. Add them only for a specific need. `prusalink.disable_metrics` removes metrics by name.

### Guide how to get infomration from the printer

I've prepared quick guide where you can learn how to get credentials and IP address from the printer for the prusa_exporter. You can find it in here, in [PRUSALINK.md](docs/readme/prusalink/PRUSALINK.md)

## Exporter metrics

`/metrics` has the metrics of the exporter itself: the build info, the Go and process metrics, the duration of each PrusaLink request and of each scrape, and counters for the UDP receiver. [docs/metrics.md](docs/metrics.md) lists them. Scrape `/metrics` as a third job, next to `/metrics/prusalink` and `/metrics/udp`.

## Alert rules

[alerts/prusa_exporter.rules.yml](alerts/prusa_exporter.rules.yml) has alert rules for Prometheus or vmalert. They use only the metrics of the exporter: a printer that waits for a person, PrusaLink that fails while the printer is on, UDP metrics that stop, slow scrapes, and errors of the exporter. `mise run test-alerts` runs their unit tests.

## Running

### Docker

The image is `ghcr.io/anyasabo/prusa_exporter`. It is a distroless static image with only the binary. It runs as a user that is not root. The docker compose file starts the exporter, Prometheus, Loki and Grafana:

```
HOST_IP=192.168.20.20 docker compose up
```

`HOST_IP` is the LAN address of this computer. The G-code tells the printer to send its UDP metrics to it. In a container, the exporter cannot find this address.

### Standalone

Download a binary from Releases, for Linux (`amd64`, `arm64`, `armv7`, `riscv64`) or macOS (`amd64`, `arm64`). `checksums.txt` has the SHA-256 of each file, and each binary has an SPDX SBOM.

```
./prusa_exporter-linux-amd64 --config.file=prusa.yml
```

Without `--udp.ip-override`, the G-code uses the local address of the route to each printer.

### Flags

Run `prusa_exporter --help` for the full list.

| Flag | Default | Description |
| --- | --- | --- |
| `--config.file` | `prusa.yml` | Configuration file |
| `--web.listen-address` | `:10009` | HTTP address. Repeat the flag for more addresses |
| `--web.config.file` | | [Web configuration](https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md) for TLS and basic auth |
| `--web.telemetry-path` | `/metrics` | Metrics of the exporter itself |
| `--exporter.metrics-path` | `/metrics/prusalink` | PrusaLink metrics |
| `--exporter.udp-metrics-path` | `/metrics/udp` | UDP metrics |
| `--udp.listen-address` | `:8514` | UDP address for the metrics of the printers |
| `--udp.prefix` | `prusa_` | Prefix of the UDP metric names |
| `--udp.gcode-enabled` | `true` | At start, send the G-code that enables the UDP metrics. `--no-udp.gcode-enabled` disables it |
| `--udp.ip-override` | | Address of the exporter in the G-code |
| `--udp.extra-metrics` | | A UDP metric to enable in addition to the defaults. Repeat the flag for more metrics |
| `--udp.all-metrics` | `false` | Enable all UDP metrics. This makes the printer slow |
| `--loki.push-url` | | Loki push URL for the job thumbnail. Empty disables the push |
| `--web.enable-pprof` | `false` | Serve the Go profiles on `/debug/pprof/` |
| `--log.level` | `info` | `debug`, `info`, `warn` or `error` |
| `--log.format` | `logfmt` | `logfmt` or `json`. `json` follows the [ECS logging specification](https://github.com/elastic/ecs-logging/blob/main/spec/spec.json) |

### Changes from 2.0.0

- `--udp.extra-metrics` takes one metric. Repeat the flag for more metrics: `--udp.extra-metrics=crash --udp.extra-metrics=power_panic`.
- Removed flags: `--exporter.metrics-port` (use `--web.listen-address`), `--prusalink.scrape-timeout` (use `prusalink_scrape_budget` in `prusa.yml`), `--loki.enabled` (set `--loki.push-url`).
- Renamed metrics: `prusa_status_info` is `prusa_printer_state`, `prusa_job` is `prusa_job_info`, `prusa_axis` is `prusa_axis_position_meters`, `prusa_mmu` is `prusa_mmu_enabled`, `prusa_last_push_timestamp` is `prusa_last_push_timestamp_seconds`. `prusa_files_count` is removed.
- `prusa_material_info` is always 1.
- The default `common_labels` has no job labels.
- The logs are logfmt by default. `--log.format=json` writes ECS: `@timestamp`, `log.level`, `message`, `error.message`, and the custom fields in `prusa.*`, for example `prusa.printer.address`.

## Development

[mise](https://mise.jdx.dev) installs the pinned tools of [mise.toml](mise.toml). CI runs the same tasks.

```
mise install
mise run ci               # linters, go mod tidy check, tests with -race, govulncheck
mise run fuzz             # the fuzz tests of the UDP parser (FUZZTIME=30s)
mise run build            # binary for this platform in dist/
mise run release-dry-run  # all binaries, checksums and SBOMs in dist/
```

A tag `vX.Y.Z` starts the release workflow. GoReleaser makes the GitHub release, and ko pushes the image. The builds are reproducible: the same commit gives the same binary. Renovate keeps the Go modules, the tools in `mise.toml`, the action SHAs and the base image digest current.

## Dashboards

I've prepared cozy [dashboards](docs/dashboards/), but this being Prometheus, you can do whatever you want. Fun fact, Mini dashboard works for MKx and Core One and MKx dashboard works for Core One but not vice versa. XL dashboard is specific for XL.

### Prusa MKx (MK3.5(S) / MK3.9(S) / MK4(S))
![mkxdashboard](docs/readme/mkx_dashboard.png)
[Dashboard](docs/dashboards/MKx.json)

### Prusa Core One
![c1dashboard](docs/readme/c1_dashboard.png)
[Dashboard](docs/dashboards/C1.json)

### Prusa XL
![xldashboard](docs/readme/xl_dashboard.png)
[Dashboard](docs/dashboards/XL.json)

### Prusa XL - fancy version for 5 Tools (BETA and WIP)
![xl5tbetadashboard](docs/readme/xl5tbeta_dashboard.gif)
[Dashboard](docs/dashboards/XL5TBeta.json)

### Prusa Mini
![minidashboard](docs/readme/mini_dashboard.png)
[Dashboard](docs/dashboards/Mini.json)

### General Overview
![minidashboard](docs/readme/overview_dashboard.png)
[Dashboard](docs/dashboards/Overview.json)

# Roadmap

rc1
- [x] create overview dashboard for all printers in system
- [x] Grafana provisioning
- [x] further testing

rc2
- [x] even more testing

final 2.0.0
- [x] 🎉

2.1.0
- [ ] Easier deploy without internet
- [ ] If possible - removal of printer_model in configuration
- [ ] Move list of UDP metrics to separate file
- [ ] Export udp enable g-code to the local filesystem
- [ ] SL printer support
- [ ] etc... list is not final

# FAQ

## My printer is correctly connected via Prusa Link API but I got no UDP metrics

After start of the exporter G-Code containing configuration is sent to the printer but it was not probably loaded properly. You can trigger reload by either restart of the exporter or you can run it manually on the printer.

## After exporter started my printers returned Warning - The G-code isn't fully compatible 

This is correct - just click on PRINT and that's it. It's possible to technically avoid this but even then you will be informed that G-Code is changing metrics configuration so it's pointless.

## After I started the exporter printers wants to print enable_udp_metrics.gcode

That is correct. Don't affraid, it's not going to print anything, just start the "*print*".

## My printer has UDP metrics host set as 172.x.x.x

In a container, the exporter cannot find the address of the host. When you start docker compose, set `HOST_IP`: `HOST_IP=192.168.20.20 docker compose up`.

## Can I enable all UDP metrics?

Yes, but you may face the issue that MCU of the printer will not keep up and it can cause issues with printing.

## Can I get also logs from the printer?

Technically yes but that is not the scode of prusa_exporter. Use [prusa_log_processor](https://github.com/pubeldev/prusa_log_processor) instead.

## Why is there Loki in docker compose? Isn't this exporter of metrics?

Well, yes. But I wanted expose png of actual print file so I've ended with using Loki. It's not necessary and you can disable by just not setting loki.push-url or setting it to `""`.

## I don't have Prusa Link metrics. Why?

Double check the prusa.yml for typos.

## Why do I have to fill printer model in configuration file?

At this moment it's not possible to get information what printer is running at the defined endpoint. So I don't even use this information in the code. If in the future will the information be available I'll use it to generate compatible metrics gcode and the `prusa.yml` will be bit easier to configure.

However printer model is not necessary for run of the exporter itself but it is used in Grafana dashboards where you can filter by model of the printer.

## Is the docker compose production ready?

Absolutely not, you can take it as example but you should properly configure Loki, Grafana and Prometheus.

## What metrics are better? UDP or Prusa Link

UDP metrics have by default 1 second of resultion while Prusa Link are scraped every 60 seconds. So because of this I'd say UDP are better but they have issue with UDP being UDP and sometimes they will just not work. 

## Can I run the exporter on Raspberry Pi?

Yes. There are binaries for Linux `amd64`, `arm64`, `armv7` and `riscv64`, and images for `linux/amd64`, `linux/arm64` and `linux/arm/v7`.

## What UDP metrics are enabled by default?

- active_extruder
- bedlet_target
- dwarfs_mcu_temp
- dwarfs_board_temp
- loadcell
- side_fsensor
- fsensor_raw
- adj_z
- temp_ambient
- temp_bed
- temp_brd
- temp_chamber
- temp_mcu
- temp_noz
- temp_hbr
- temp_psu
- temp_sandwich
- temp_splitter
- dwarf_mcu_temp
- dwarf_board_temp
- buddy_temp
- bedlet_temp
- bed_mcu_temp
- chamber_temp
- ttemp_noz
- ttemp_bed
- chamber_ttemp
- cur_mmu_imp
- curr_inp
- Sandwitch5VCurrent
- splitter_5V_current
- bed_curr
- bedlet_curr
- curr_nozz
- dwarf_heat_curr
- xlbuddy5VCurrent
- eth_in
- eth_out
- esp_in
- esp_out
- volt_bed
- volt_nozz
- 24VVoltage
- 5VVoltage
- loadcell_value
- fan
- fan_hbr_speed
- fan_speed
- xbe_fan
- print_fan_act
- hbr_fan_act
- hbr_fan_enc
- cpu_usage
- heap
- heap_free
- heap_total
- fsensor
- door_sensor
- fw_version
- buddy_revision
- buddy_bom
