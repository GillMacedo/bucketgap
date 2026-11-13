# bucketgap

Claude Code skill and Go CLI that checks the `le` and `quantile` values Prometheus rules, PrometheusRule objects and Grafana dashboards select against real `/metrics` output, the way Prometheus 3 or 2 stores them, and reports selectors that match no series.

## Architecture
`plugin/scripts/bucketgap.go` holds the CLI and the report. `plugin/scripts/scrape.go` reads exposition text strictly (`parseScrape`) and models Prometheus 3's value rewrite (`stored`, `formatOpenMetricsFloat`). `plugin/scripts/promql.go` lexes PromQL and finds vector selectors and their matchers (`selectors`). `plugin/scripts/rules.go` loads rule files, PrometheusRule objects and Grafana dashboards (`loadInputs`). `plugin/scripts/check.go` compares matchers with the scrapes (`checkExpr`, `checkMatcher`). `plugin/scripts/yaml_*.go` is go.yaml.in/yaml/v2 copied in, so the plugin builds with no go.mod. `plugin/skills/checking-histogram-buckets-in-alerts/SKILL.md` tells Claude how to use it. See `docs/architecture.md`.

## Commands
- `make test`: gofmt, vet and tests, including `tests/`, which run the example service in `examples/checkout` and scrape it
- `BUCKETGAP_PROMETHEUS=/path/prometheus3:/path/prometheus2 go test ./tests -run TestAgainstPrometheus -v`: compare with real Prometheus servers
- `make lint`: plugin manifest validation (needs Claude Code)
- `make demo`: run on the examples

## Conventions
- Standard library only, plus the copied YAML parser. Read files only: never connect to Prometheus, Grafana or a cluster, never write files.
- Any input it can't fully read or trust exits 2 with nothing on stdout, whatever `--fail-on` says. So does a run that checked nothing. Add a case to `badScrapes`, `badRules` or `badDashboards` in `tests/inputs_test.go` for each new kind of input.
- How a value is stored must follow Prometheus's source (`model/textparse`, `model/labels`). When that changes, run `TestAgainstPrometheus` with real binaries.
- Tests must not assume the machine they run on: build for the host, and read `runtime` for anything platform-specific.

## Do and don't
- Do run `make test` on Go 1.22 and on the current Go release before calling a change done.
- Don't add `go.mod` requirements to the root module: the installed plugin has no module. `examples/checkout` has its own module with a vendored `client_golang`.
- Don't print "No problems" for a run that checked nothing.

## Definition of done
`make test` and `make lint` pass, README output matches a real run, and `ROADMAP.md` and docs are updated when relevant.
