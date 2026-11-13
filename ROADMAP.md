# Roadmap

## 0.1.0 (this release)
- Check `le` and `quantile` values in rules and dashboards against real `/metrics` output, for Prometheus 3 and 2.

## Next
- Follow recording rules: check `job:x_bucket:rate5m{le="0.5"}` against `x_bucket` when the recording rule keeps `le`.
- Read the `le` values of native histograms converted to classic buckets (`convert_classic_histograms_to_nhcb`), and say when a selector targets a metric stored only as a native histogram.
- Check `histogram_quantile` aggregations that drop `le`, since the scrape tells us which metrics are histograms.
- Read Grafana dashboards wrapped in Kubernetes ConfigMaps (kube-prometheus style).
- A `--diff` mode: compare two scrapes (before and after a bucket change in code) and list the rules each change breaks.
- Accept Prometheus's own `/api/v1/series` JSON output as a scrape, for teams that can query the server but can't reach each target.

## Not planned
- Connecting to Prometheus, Grafana or a cluster. bucketgap reads files.
- Rendering Helm templates or jsonnet.
