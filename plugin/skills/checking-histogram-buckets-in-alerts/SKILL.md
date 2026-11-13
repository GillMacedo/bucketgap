---
name: checking-histogram-buckets-in-alerts
description: Checks the le and quantile values that Prometheus alerting rules, recording rules, PrometheusRule objects and Grafana dashboard queries select against the buckets and quantiles a service really exposes in its /metrics output, the way Prometheus 3 or 2 stores them. Finds selectors that match no series, so an SLO or latency alert never fires and a panel stays empty, such as le="0.3" on a histogram without a 0.3 bucket, le="1" or le="5" after Prometheus 3 stores "1.0" and "5.0", quantile="0.95" on a summary that exports 0.9 and 0.99, histogram_quantile over x_bucket when x is a summary, and buckets that only some targets have. Use when an SLO or latency alert never fires or a dashboard panel is empty, before or after upgrading to Prometheus 3, when changing histogram buckets or summary objectives in code, and when reviewing a change to rules or dashboards.
---

# Checking histogram buckets in alerts and dashboards

bucketgap reads rule files and dashboards, finds every selector with an `le` or `quantile` matcher (and every `_bucket` selector), and compares the values with real `/metrics` output. It reads files only: no Prometheus server, no cluster, no network. It needs Go 1.22 or newer.

## Get the inputs

1. **Real /metrics output of the service the rules watch**, saved to a file, one file per target: `curl -sf http://HOST:PORT/metrics > metrics.txt`. Check that curl exited 0. Scrape after the service has handled some traffic: client libraries leave a histogram or summary vector out of `/metrics` until it records its first value. For a pod, `kubectl port-forward` or `kubectl exec ... -- wget -qO- localhost:PORT/metrics` work. If several versions run at once (a rollout, a canary), scrape one of each and pass each with its own `--scrape`.
2. **Rules and dashboards:** Prometheus rule files, rendered PrometheusRule YAML (`helm template` or `kustomize build` output, other objects are skipped), Grafana dashboard JSON, or directories of them. Render templates first; bucketgap doesn't read Helm templates or jsonnet.
3. **The Prometheus version** that stores the data: `--prometheus 3` (default) or `--prometheus 2`. If unsure, ask the user or check the server's `/api/v1/status/buildinfo`. If the data is in Thanos, Mimir or VictoriaMetrics, say that bucketgap models Prometheus's own storage and the result may differ.

## Run it

Build it once, then run it:

    go build -o "${TMPDIR:-/tmp}/bucketgap" $(ls "${CLAUDE_PLUGIN_ROOT}"/scripts/*.go | grep -v _test.go)
    "${TMPDIR:-/tmp}/bucketgap" --scrape metrics.txt rules/ dashboards/

Add `--format json` for machine-readable output and `--fail-on warn` to fail on warnings too.

## Read the exit code first

- **0:** nothing at or above `--fail-on` (default `error`). Read the notes: selectors on metrics that aren't in any scrape were not checked.
- **1:** findings at or above `--fail-on`.
- **2:** nothing was reported. The message says why: a scrape that is empty, not /metrics output (an error page, curl or build output), cut off, or two scrapes in one file; a rule file or dashboard that is empty, invalid, cut off or not rules; PromQL it can't read; or nothing to check because no selector names a metric in the scrapes. Fix the input and run again. Never describe exit 2 as a clean result.

## Check each finding

- **ERROR `no-such-bucket`, `no-such-quantile`:** the value isn't a bucket boundary or quantile of that metric. The selector matches nothing. The fix names the nearest values; the other fix is to add the value in the code that defines the histogram or summary.
- **ERROR `le-format`, `quantile-format`:** the number exists but is spelled differently from how Prometheus stores it. Prometheus 3 stores `1.0`, `5.0`, `10.0` for typed histograms and summaries; Prometheus 2 keeps the value as exposed. During a migration, `le=~"1(\\.0)?"` matches both.
- **ERROR `no-such-series`:** `x_bucket` when `x` is a summary (or the reverse). Summaries have no buckets, so `histogram_quantile` over them returns nothing.
- **ERROR `not-in-scrape`:** the metric isn't in the scrape although other metrics with the same prefix are: a rename, a typo, a vector that hasn't recorded anything yet, or a scrape cut off between two metrics. Scrape again after traffic before you call it a rename.
- **ERROR `no-label`:** the metric exists but has no `le` (or `quantile`) label.
- **WARN `regex-branch-matches-nothing`:** one alternative of `le=~"a|b"` matches nothing, so the sum leaves out what it meant to include.
- **WARN `missing-in-some`:** the value exists on some targets and not others, so sums over all targets silently leave some out.

## Report

1. Errors first: the file and rule (or dashboard panel), the selector, what the alert or panel does now (an alert whose expression returns nothing never fires; a ratio with an empty numerator returns nothing too), and the fix.
2. Then warnings.
3. Say what wasn't checked: metrics not in any scrape, selectors with dashboard variables, and recording rules used by other rules (bucketgap checks the raw metric selectors, not series produced by recording rules).

## Don't

Don't say the alerts work because bucketgap found nothing: it checks label values against one scrape, not thresholds, `for` durations, routing or whether the query makes sense. Don't run it on a scrape from a different version of the service than the one deployed, unless the user wants to check that version.
