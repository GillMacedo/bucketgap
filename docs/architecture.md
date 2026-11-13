# Architecture

bucketgap is one Go program in `plugin/scripts`, with only the standard library and a copied YAML parser, so the installed plugin builds with `go build` and no module.

## Flow

1. **Scrapes** (`scrape.go`). Each `--scrape` file is read line by line. Every sample line must parse; `# HELP`, `# TYPE` and `# UNIT` lines are checked; a repeated `# HELP` or `# TYPE` for the same family means two scrapes were pasted together. For each sample name it records the `le` and `quantile` values, and whether Prometheus 3 rewrites them: Prometheus's text parser takes the metric type from the last `# TYPE` line, whatever the sample's name, and `normalizeFloatsInLabelValues` rewrites `le` under `histogram` and `quantile` under `summary` with `labels.FormatOpenMetricsFloat`. bucketgap does the same. After the last line it checks that every histogram series has its `+Inf` bucket and `_count`, every summary series its `_count`, and that the file doesn't end after a `# TYPE` line.
2. **Rules and dashboards** (`rules.go`). YAML files are decoded document by document. A document with `groups:` is a rule file; a `kind: PrometheusRule` object uses `spec.groups`; other objects are skipped. JSON files are Grafana dashboards: every `targets[].expr` at any depth is a query. A named file that has no rules or queries is refused; in a directory it's skipped and listed.
3. **PromQL** (`promql.go`). A lexer turns the expression into identifiers, strings, numbers, punctuation and Grafana variables. Strings are unquoted with Go's rules, which PromQL uses. Brackets must balance. An identifier followed by `(` is a function or aggregation; the label lists after `by`, `without`, `on`, `ignoring`, `group_left` and `group_right` are skipped; any other identifier is a metric name, followed by optional `{matchers}`. A bare `{...}` selector takes its name from `__name__` or a quoted name.
4. **Checks** (`check.go`). For each selector with an `le` or `quantile` matcher using `=` or `=~`, or a name ending in `_bucket`, the matcher's value is compared with the stored values of that sample name in every scrape that has it. Regexes are anchored as Prometheus anchors them. A metric in no scrape is checked against the families that are there (`no-such-series`) and against the other metrics with the same prefix (`not-in-scrape`); otherwise it's listed as not checked.
5. **Report** (`bucketgap.go`). Text or JSON. Exit 1 when a finding is at or above `--fail-on`, exit 2 for any input error or a run that checked nothing.

## Why not the Prometheus PromQL parser

Using `github.com/prometheus/prometheus/promql/parser` would need a module and many dependencies in the installed plugin. The hand-written reader only needs selectors and matchers. It refuses what it can't read, and `TestSelectors` and `TestSelectorErrors` pin its behavior. Real rule sets were read during development (see the README's Tested on section).

## Tests

- `plugin/scripts/bucketgap_test.go`: the lexer, the matcher reader, value formatting and sample parsing.
- `tests/`: runs the built binary. `examples_test.go` on the example service's saved scrape; `live_test.go` starts the example service and scrapes it; `inputs_test.go` and `cut_test.go` feed broken and cut-off input; `plugin_test.go` builds the plugin as installed; `readme_test.go` runs the README's commands; `prometheus_test.go` compares with real Prometheus when `BUCKETGAP_PROMETHEUS` is set.
