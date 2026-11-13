# Changelog

All notable changes are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-11-13

### Added
- `bucketgap` CLI and the `checking-histogram-buckets-in-alerts` skill: checks `le` and `quantile` values in Prometheus rule files, PrometheusRule objects and Grafana dashboards against real `/metrics` output.
- Findings: `no-such-bucket`, `no-such-quantile`, `le-format` and `quantile-format` (Prometheus 3's `1.0` and Prometheus 2's as-exposed values), `no-such-series`, `not-in-scrape`, `no-label`, `regex-branch-matches-nothing` and `missing-in-some`.
- `--prometheus 3|2`, several `--scrape` files, `--format json` and `--fail-on error|warn|never`.
- Refuses with exit 2 on scrapes, rules or dashboards it can't trust, and on runs that checked nothing.
