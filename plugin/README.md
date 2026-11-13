# bucketgap plugin

This plugin adds the `checking-histogram-buckets-in-alerts` skill to Claude Code. The skill runs `scripts/bucketgap.go`, which reads Prometheus rule files, PrometheusRule objects and Grafana dashboards, and checks every `le` and `quantile` value they select against the buckets and quantiles in real `/metrics` output, the way Prometheus 3 (or 2) stores them. It needs Go 1.22 or newer. It reads files only.

See the repository README for examples, exit codes and limits: https://github.com/GillMacedo/bucketgap

## Install

    claude plugin marketplace add GillMacedo/bucketgap
    claude plugin install bucketgap@bucketgap-marketplace
