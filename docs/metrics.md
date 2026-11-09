# Metrics reference

This file is generated. Do not edit it. To write it again, run:

    mise exec -- go test -run TestMetricsReference . -args -update

The test reads the output of the exporter. If a metric name, type, label or help text
changes, the test fails until this file is written again. Then update the dashboards and the
alert rules that use the metric.

## PrusaLink: `/metrics/prusalink`

One request to PrusaLink for each printer at each scrape. The labels `printer_address`, `printer_model` and `printer_name` are the default of `prusalink.common_labels` in `prusa.yml`.

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `prusa_axis_position_meters` | gauge | `printer_address`, `printer_axis`, `printer_model`, `printer_name` | Position of an axis. |
| `prusa_fan_speed_rpm` | gauge | `fan`, `printer_address`, `printer_model`, `printer_name` | Speed of a fan in revolutions per minute. |
| `prusa_info` | gauge | `api_version`, `printer_address`, `printer_hostname`, `printer_location`, `printer_model`, `printer_name`, `prusalink_name`, `serial_number`, `server_version`, `version_text` | Information about the printer. The value is always 1. |
| `prusa_job_info` | gauge | `printer_address`, `printer_job_name`, `printer_job_path`, `printer_model`, `printer_name` | The current print job. The value is always 1. There is no series if there is no job. |
| `prusa_material_info` | gauge | `printer_address`, `printer_filament`, `printer_model`, `printer_name` | The loaded filament ("---" if none). The value is always 1. |
| `prusa_mmu_enabled` | gauge | `printer_address`, `printer_model`, `printer_name` | 1 if the MMU is enabled, else 0. |
| `prusa_nozzle_size_meters` | gauge | `printer_address`, `printer_model`, `printer_name` | Diameter of the selected nozzle. |
| `prusa_print_flow_ratio` | gauge | `printer_address`, `printer_model`, `printer_name` | Flow factor of the printer, 1 is 100 %. |
| `prusa_print_speed_ratio` | gauge | `printer_address`, `printer_model`, `printer_name` | Speed factor of the printer, 1 is 100 %. |
| `prusa_print_time_seconds` | gauge | `printer_address`, `printer_model`, `printer_name` | Time since the start of the current print. |
| `prusa_printer_state` | gauge | `printer_address`, `printer_model`, `printer_name`, `printer_state` | 1 for the current state of the printer, else 0. |
| `prusa_printing_progress_ratio` | gauge | `printer_address`, `printer_model`, `printer_name` | Progress of the current print, from 0 to 1. |
| `prusa_printing_time_remaining_seconds` | gauge | `printer_address`, `printer_model`, `printer_name` | Estimated time until the end of the current print. |
| `prusa_scrape_duration_seconds` | gauge | `printer_address`, `printer_model`, `printer_name` | Duration of the PrusaLink requests of the scrape. |
| `prusa_temperature_celsius` | gauge | `printer_address`, `printer_heated_element`, `printer_model`, `printer_name` | Temperature of a heated element. |
| `prusa_temperature_target_celsius` | gauge | `printer_address`, `printer_heated_element`, `printer_model`, `printer_name` | Target temperature of a heated element. |
| `prusa_udp_metrics_gcode_sent` | gauge | `printer_address`, `printer_model`, `printer_name` | 1 if the exporter sent and started the G-code that enables the UDP metrics, else 0. |
| `prusa_up` | gauge | `printer_address`, `printer_model`, `printer_name` | 1 if all PrusaLink requests of the scrape succeeded, else 0. |

## UDP: `/metrics/udp`

The fixed series. The firmware metrics are not in this list. A firmware metric `NAME` with a number is `prusa_NAME`. An event (`v=T`, or the count `n`) is `prusa_NAME_total`. A string is `prusa_NAME_info` with the label `value`. The events in `exporter.udp_event_metrics` start at 0. A field in `exporter.udp_counter_fields` is the counter `prusa_NAME_FIELD_total` (`prusa_NAME_total` for `v`).

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `prusa_last_push_timestamp_seconds` | gauge | `printer_address`, `printer_mac` | Time of the last UDP message from the printer. |
| `prusa_udp_packets_total` | counter | `printer_address`, `printer_mac` | Number of UDP datagrams from the printer that the exporter received. |
| `prusa_udp_point_errors_all_total` | counter | `printer_address`, `printer_mac` | Number of error points (error="...") from the printer firmware, all metrics. The exporter drops these points. prusa_udp_point_errors_total has the count for each metric. |
| `prusa_udp_point_errors_total` | counter | `metric`, `printer_address`, `printer_mac` | Number of error points (error="...") from the printer firmware, by metric. The exporter drops these points. |
| `prusa_udp_points_total` | counter | `printer_address`, `printer_mac` | Number of points from the printer firmware that the exporter parsed, error points and event points included. |

## Exporter: `/metrics`

The metrics of the exporter itself. The endpoint also has the standard `go_*`, `process_*` and `promhttp_metric_handler_*` metrics, which are not in this list. `prusa_exporter_loki_pushes_total` exists only with `--loki.push-url`.

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `prusa_exporter_build_info` | gauge | `branch`, `goarch`, `goos`, `goversion`, `revision`, `tags`, `version` | A metric with a constant '1' value labeled by version, revision, branch, goversion from which prusa_exporter was built, and the goos and goarch for the build. |
| `prusa_exporter_http_request_duration_seconds` | histogram | `code`, `handler` | Duration of the HTTP requests to the PrusaLink and UDP metrics endpoints. |
| `prusa_exporter_loki_pushes_total` | counter | `result` | Number of job thumbnail pushes to Loki, by result. |
| `prusa_exporter_prusalink_handler_errors_total` | counter |  | Number of errors of the PrusaLink metrics handler while it gathered or encoded metrics. |
| `prusa_exporter_prusalink_request_duration_seconds` | histogram | `code`, `endpoint`, `printer_address` | Duration of the HTTP requests to PrusaLink, including the digest challenges. |
| `prusa_exporter_udp_invalid_messages_total` | counter |  | Number of UDP datagrams without a valid syslog header. |
| `prusa_exporter_udp_packets_received_total` | counter |  | Number of UDP datagrams received. |
| `prusa_exporter_udp_points_total` | counter | `result` | Number of UDP points, by result. "dropped" is a point of a new series above the series limit, or with a conflicting type. |
| `prusa_exporter_udp_read_errors_total` | counter |  | Number of errors while reading from the UDP socket. |
| `prusa_exporter_udp_received_bytes_total` | counter |  | Number of bytes in the UDP datagrams received. |
| `prusa_exporter_udp_series` | gauge |  | Number of UDP series in memory. |
| `prusa_exporter_udp_series_expired_total` | counter |  | Number of UDP series deleted because they had no point in the TTL. |

