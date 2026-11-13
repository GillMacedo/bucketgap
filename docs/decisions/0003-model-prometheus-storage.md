# 0003: Compare with stored values, checked against real Prometheus

## Context
Whether `le="1"` matches depends on the Prometheus version: Prometheus 3 rewrites `le` and `quantile` values at ingestion, Prometheus 2 doesn't. Prometheus decides which samples to rewrite from the last `# TYPE` line, not from the sample's name.

## Decision
bucketgap computes the stored value with the same rules (`stored`, `formatOpenMetricsFloat`), selected by `--prometheus`, with 3 as the default. `tests/prometheus_test.go` runs real Prometheus 3.16.0 and 2.55.1, scrapes the example service and an odd-spelling fixture, and requires bucketgap to flag a selector exactly when Prometheus returns no series. CI runs it with checksum-pinned release binaries.

## Consequences
Remote storage, relabeling and native histograms are not modeled, and the README says so.
