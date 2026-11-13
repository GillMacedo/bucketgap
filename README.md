# bucketgap: find Prometheus alerts and dashboards whose le or quantile selectors match nothing

[![CI](https://github.com/GillMacedo/bucketgap/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/bucketgap/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**A Claude Code skill and Go CLI for SREs and backend teams who run Prometheus. It checks every `le` and `quantile` value in your alerting rules, recording rules, PrometheusRule objects and Grafana dashboards against the buckets and quantiles in real `/metrics` output, the way Prometheus 3 or 2 stores them, and reports the selectors that match no series.**

A latency SLO alert reads `x_bucket{le="0.3"}`, but the histogram's buckets are 0.25 and 0.5. Prometheus 3 stores `le="1"` as `le="1.0"`, so every rule and panel written as `le="1"` or `le="5"` went empty on upgrade. Someone changes the buckets in code and the rules aren't updated. A rule asks a summary for `quantile="0.95"` when it exports 0.9 and 0.99, or runs `histogram_quantile` over `x_bucket` when `x` is a summary. In each case the selector returns nothing, the alert's expression returns nothing, and the alert never fires. Nothing reports it: `promtool check rules` only checks syntax, and the server just returns an empty result.

bucketgap reads the files, finds each selector with an `le` or `quantile` matcher (and every `_bucket` selector), and compares the values with the label values in the scrape, after applying Prometheus 3's rewrite of `le` and `quantile` values. It reads files only and never connects to Prometheus, Grafana or a cluster.

## What you can ask Claude

- "Our checkout latency alert has never fired. Do its le values exist in the service's histogram?"
- "We're moving to Prometheus 3. Which rules and dashboards use le or quantile values that will stop matching?"
- "I changed the request duration buckets in this Go service. Which alerts and panels still use the old ones?"
- "Review this PrometheusRule change against the /metrics output I saved."

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/bucketgap
    claude plugin install bucketgap@bucketgap-marketplace

Or run it with Go 1.22 or newer. It has no dependencies outside this repository:

    curl -sf http://localhost:8080/metrics > metrics.txt
    go run github.com/GillMacedo/bucketgap/plugin/scripts@v0.1.0 --scrape metrics.txt rules/ dashboards/

## Example

`examples/checkout` is a made-up Go service that records request latency in a `prometheus/client_golang` histogram with buckets 0.05 to 5, and queue wait in a summary with quantiles 0.5, 0.9 and 0.99. `examples/checkout/metrics.txt` is its real `/metrics` output after 23 requests. `rules/broken.yaml` and `rules/dashboard.json` have common mistakes. `--fail-on never` keeps the exit code at 0 for this demo; without it, the run exits 1:

    $ go run ./plugin/scripts --fail-on never --scrape examples/checkout/metrics.txt examples/checkout/rules/broken.yaml examples/checkout/rules/dashboard.json
    bucketgap 0.1.0, Prometheus 3: 1 rule file and 1 dashboard (9 queries), 1 scrape
    checked 8 bucket or quantile selectors against the scrapes
    note: not checked, metric not in any scrape: 1 (payments_request_duration_seconds_bucket)

    ERROR no-such-bucket  examples/checkout/rules/broken.yaml:5  group checkout, alert CheckoutSlowRequests
      checkout_request_duration_seconds_bucket{le="0.3"}
      checkout_request_duration_seconds_bucket has no bucket at 0.3, so this selector matches nothing; the buckets are 0.05 0.1 0.25 0.5 1.0 2.5 5.0 +Inf
      fix: use le="0.25" or le="0.5", or add 0.3 to the buckets where the histogram is defined

    ERROR le-format  examples/checkout/rules/broken.yaml:12  group checkout, record checkout:requests_under_1s:ratio_rate5m
      checkout_request_duration_seconds_bucket{le="1"}
      Prometheus 3 stores this bucket of checkout_request_duration_seconds_bucket as le="1.0", so le="1" matches nothing (Prometheus 2 kept the value as exposed)
      fix: write le="1.0"; while some servers still run Prometheus 2, le=~"1(\\.0)?" matches both

    ERROR no-such-quantile  examples/checkout/rules/broken.yaml:20  group checkout, alert CheckoutQueueWaitHigh
      checkout_queue_wait_seconds{quantile="0.95"}
      checkout_queue_wait_seconds has no quantile at 0.95, so this selector matches nothing; the quantiles are 0.5 0.9 0.99
      fix: use quantile="0.9" or quantile="0.99", or add 0.95 to the summary's objectives

    ERROR no-such-series  examples/checkout/rules/broken.yaml:25  group checkout, alert CheckoutQueueWaitP99High
      checkout_queue_wait_seconds_bucket
      checkout_queue_wait_seconds_bucket doesn't exist in examples/checkout/metrics.txt: checkout_queue_wait_seconds is a summary with the series checkout_queue_wait_seconds, checkout_queue_wait_seconds_count, checkout_queue_wait_seconds_sum, so this selector matches nothing
      fix: query the series checkout_queue_wait_seconds has; a summary doesn't have _bucket

    ERROR le-format  examples/checkout/rules/dashboard.json  panel "Requests under 5 s", query A
      checkout_request_duration_seconds_bucket{le="5"}
      Prometheus 3 stores this bucket of checkout_request_duration_seconds_bucket as le="5.0", so le="5" matches nothing (Prometheus 2 kept the value as exposed)
      fix: write le="5.0"; while some servers still run Prometheus 2, le=~"5(\\.0)?" matches both

    WARN regex-branch-matches-nothing  examples/checkout/rules/broken.yaml:31  group checkout, record checkout:requests_fast:rate5m
      checkout_request_duration_seconds_bucket{le=~"0.25|0.3"}
      the alternative "0.3" matches none of checkout_request_duration_seconds_bucket's buckets (0.05 0.1 0.25 0.5 1.0 2.5 5.0 +Inf), so the result leaves out what it was meant to include
      fix: use le="0.25" or le="0.5", or add 0.3 to the buckets where the histogram is defined

    5 errors, 1 warning

`payments_request_duration_seconds_bucket` belongs to another service, so it's listed as not checked. Pass that service's `/metrics` with a second `--scrape` to check it too.

The corrected rules stay quiet:

    $ go run ./plugin/scripts --scrape examples/checkout/metrics.txt examples/checkout/rules/fixed.yaml
    bucketgap 0.1.0, Prometheus 3: 1 rule file and 0 dashboards (5 queries), 1 scrape
    checked 5 bucket or quantile selectors against the scrapes

    No problems in 5 checked selectors.

Prometheus 2 keeps the value as the service exposes it, so the same file has one mistake there:

    $ go run ./plugin/scripts --prometheus 2 --fail-on never --scrape examples/checkout/metrics.txt examples/checkout/rules/fixed.yaml
    bucketgap 0.1.0, Prometheus 2: 1 rule file and 0 dashboards (5 queries), 1 scrape
    checked 5 bucket or quantile selectors against the scrapes

    ERROR le-format  examples/checkout/rules/fixed.yaml:12  group checkout, record checkout:requests_under_1s:ratio_rate5m
      checkout_request_duration_seconds_bucket{le="1.0"}
      Prometheus 2 keeps the le value as the target exposes it, le="1", so le="1.0" matches nothing
      fix: write le="1"

    1 error, 0 warnings

Add `--format json` for machine-readable output.

## What it reads

- **Scrapes (`--scrape`, repeatable):** one target's `/metrics` output per file, in the Prometheus text format or OpenMetrics text. Pass one file per target, or one per version during a rollout.
- **Rules and dashboards:** Prometheus rule files (`groups:`), PrometheusRule objects (including multi-document `helm template` or `kustomize build` output, where other objects are skipped), Grafana dashboard JSON (every `targets[].expr`, in panels and rows at any depth), and directories of `.yaml`, `.yml` and `.json` files.
- **`--prometheus 3` (default) or `2`:** Prometheus 3 rewrites `le` values of typed histograms and `quantile` values of typed summaries to its float format at ingestion: `1` becomes `1.0`, `0.30` becomes `0.3`, `1000000` becomes `1e+06`. Like Prometheus's text parser, bucketgap takes the type from the last `# TYPE` line before the sample. Prometheus 2 stores the value as exposed.

Grafana variables (`$route`, `${le}`, `[[x]]`) are understood: a selector whose `le` value or metric name is a variable is listed as not checked. Dashboard queries for a data source whose type isn't Prometheus, such as Loki, are skipped and counted in a note.

## What it reports

| Code | Severity | Meaning |
|---|---|---|
| `no-such-bucket` | ERROR | The `le` value (or every value an `le=~` regex allows) isn't a bucket of that histogram. The fix names the nearest buckets |
| `no-such-quantile` | ERROR | The same for a summary's `quantile` |
| `le-format`, `quantile-format` | ERROR | The number exists but is spelled differently from how Prometheus stores it: `le="1"` on Prometheus 3, `le="1.0"` on Prometheus 2 |
| `no-such-series` | ERROR | `x_bucket` when `x` is a summary, or a series name the family doesn't have |
| `not-in-scrape` | ERROR | The metric isn't in the scrape although other metrics with the same prefix are: a rename, a typo, a vector that hasn't recorded anything yet, or a scrape cut off between two metrics |
| `no-label` | ERROR | The metric exists but has no `le` or `quantile` label |
| `regex-branch-matches-nothing` | WARN | One alternative of `le=~"a\|b"` matches nothing. Alternatives that only spell an existing value differently, as in `le=~"1\|1.0"`, are allowed |
| `missing-in-some` | WARN | With several scrapes, the value exists on some targets and not on others, so sums over all targets leave some out |

Only `=` and `=~` matchers are checked. `!=` and `!~` can't make a selector empty this way.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Nothing at or above `--fail-on`, which is `error` by default. Use `--fail-on warn` to fail on warnings too, or `never`. |
| 1 | Findings at or above `--fail-on`. |
| 2 | The input can't be trusted, nothing could be checked, or the flags are wrong. Stdout is empty. |

## When it refuses

bucketgap exits 2 and prints only the reason when it can't trust what it read, whatever `--fail-on` says. For example, a scrape file that holds curl's error because the service was down:

    printf 'curl: (7) Failed to connect to 127.0.0.1 port 8080 after 0 ms: Could not connect to server\n' > failed.txt
    go run ./plugin/scripts --scrape failed.txt examples/checkout/rules/fixed.yaml

prints this on stderr. The program exits 2 (under `go run`, Go adds a line `exit status 2` and itself exits 1):

    bucketgap: can't trust the input, nothing was reported: failed.txt:1: expected a value and an optional timestamp, got "(7) Failed to connect to 127.0.0.1 port ..."; this doesn't look like /metrics output

On the scrape side it does the same for an empty file, an HTML or JSON error page, build, panic or log output, a file without a final line break, a histogram series without its `+Inf` bucket or `_count`, a summary series without `_count`, a file that ends after a `# TYPE` line, two scrapes pasted into one file, and text after `# EOF`. On the rules side: an empty file, invalid or cut-off YAML or JSON, a YAML file without a final line break, failed `helm`, `jsonnet` or `promtool` output, a file with no rules, a rule without `expr` or with both `alert` and `record`, an unrendered Helm template, LogQL in a Loki rule file, and PromQL it can't read: unbalanced brackets, unterminated strings, bad escapes, or a regex that doesn't compile. A single bad file among good ones fails the whole run. It also refuses when nothing was checked, such as when no selector names a metric in the scrapes, so a run that checked nothing never looks clean.

Two kinds of cut can't be seen in the file. The text format has no end marker, so a scrape cut exactly between two metrics looks complete; bucketgap then reports the metrics the rules use that are missing with `not-in-scrape` when other metrics with the same prefix are present. A rule file cut exactly at a line break can still be a valid, shorter rule file. The header line says how many queries were read, so check that number.

## Limits

- **One scrape per target.** The check is only as good as the scrape. Client libraries leave a histogram or summary vector out of `/metrics` until it records its first value, so scrape after the service has handled traffic.
- **Raw metrics only.** Selectors on series made by recording rules (`job:x_bucket:rate5m{le="0.5"}`) are listed as not checked. bucketgap doesn't follow a recording rule back to the metric it reads.
- **Prometheus's own storage.** It models how Prometheus 2.55 and 3.x store scraped values. Remote storage (Thanos, Mimir, VictoriaMetrics), `metric_relabel_configs` that rewrite `le`, OTLP ingestion and native histograms are not modeled. On Prometheus 2, a server that scrapes with the protobuf format (for native histograms) stores `1.0` like Prometheus 3; pass `--prometheus 3` for that case.
- **Values, not meaning.** A clean result means each `le` and `quantile` value exists. It doesn't check thresholds, `for` durations, label joins or whether `histogram_quantile` keeps `le` in its aggregation.
- **Templates.** Helm templates and jsonnet must be rendered first.

## FAQ

**Why did my latency alert stop firing after upgrading to Prometheus 3?** Prometheus 3 normalizes `le` and `quantile` label values when it ingests them, so a bucket exposed as `le="1"` is stored as `le="1.0"`. A rule with `le="1"` then matches nothing. Write `le="1.0"`, or `le=~"1(\\.0)?"` while some servers still run Prometheus 2. The Prometheus 3 migration guide describes the change.

**Why does `x_bucket{le="0.3"}` return nothing?** An `le` selector picks one bucket boundary exactly; Prometheus doesn't interpolate between buckets for it. If the histogram has no 0.3 bucket, there's no such series. Use an existing boundary or add 0.3 to the buckets in code.

**Does `histogram_quantile` work on a summary?** No. A summary has no `_bucket` series, so `histogram_quantile(0.99, rate(x_bucket[5m]))` returns nothing. Query the summary's own `quantile` series.

**Does it need a running Prometheus?** No. It needs the `/metrics` output of your service, saved to a file, and the rule files. The tests compare it with real Prometheus servers, but using it doesn't.

**Is it an MCP server?** No. It's a skill with one Go program, so `go run` or `go build` is all it needs.

## Tested on

- **Real Prometheus:** `tests/prometheus_test.go` starts the example service and a fixture with unusual spellings (`1e-05`, `0.00010`, `0.30`, `1e+06`, untyped families, a sample after a histogram without its own `# TYPE`), lets real Prometheus servers scrape both, then checks 152 selectors made from every exposed value and common misspellings. For each one, bucketgap must flag it exactly when Prometheus returns no series. On Prometheus 3.16.0 (80 of 152 empty) and 2.55.1 (65 of 152 empty), linux arm64, bucketgap agreed on all 152. CI runs the same test with the linux amd64 binaries.
- **The example service, live:** tests build `examples/checkout` with the vendored `prometheus/client_golang` v1.20.5, scrape it while it runs, and check that the result matches the saved scrape, that the fixed rules stay quiet, and that a scrape taken before the first request (when the histogram isn't exposed yet) doesn't pass.
- **Real rule sets, by hand:** all 116 rule files in samber/awesome-prometheus-alerts at 4b14ccb (1,189 queries), the kube-prometheus manifests at d88939b, and the 33 Grafana dashboards in those manifests (581 queries) are read without a parse refusal. With the example service's scrape, the GoGcDurationHigh alert in awesome-prometheus-alerts' Go rules, `go_gc_duration_seconds{quantile="1.0"}`, is reported on `--prometheus 2` (the Go client exposes `quantile="1"`) and is clean on Prometheus 3. Their Prometheus self-monitoring rules stay quiet against a real Prometheus 3.16.0 scrape. These checks need network and aren't part of `make test`.
- **Input it can't trust:** tests pass 17 kinds of bad scrape (alone and next to a real one), 26 kinds of bad rule file (alone and next to a good one) and 7 kinds of bad dashboard, under every `--fail-on` value and both formats, and check for exit 2 with nothing on stdout. Others cut the real scrape, the rule files and the dashboard at every line break and every byte (every seventh byte for the scrape): a mid-line cut always exits 2, a scrape cut on a line break exits 2, reports `not-in-scrape`, or gives exactly the full scrape's findings, and a rule file cut on a line break gives a subset of the full file's findings.
- **The installed plugin:** a test copies `plugin/` out of the repository and builds it with the skill's own command.
- **Go versions:** the tests pass on Go 1.22.12 and Go 1.27.3 on Linux arm64.

Not tested: macOS and Windows; remote storage such as Thanos, Mimir or VictoriaMetrics; scrapes from client libraries other than Go's (the Prometheus behavior is the same, but exposition quirks may differ); a fresh Claude Code session using the skill. The `go run ...@v0.1.0` line works only once the repository is published.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/bucketgap/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes, and a small problem may become a follow-up issue instead. A PR with no new commit 14 days after a change request is closed, and you can reopen it. PRs that change CI, agent settings, npm or Python dependencies, vendored code or build files can't be merged this way, and a comment says so. The same goes for anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about two hours, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `bucketgap` saved you some prompts, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)
