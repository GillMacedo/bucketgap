Add a check to bucketgap for: $ARGUMENTS

1. Find the Prometheus source that defines the behavior (the text parser in `model/textparse`, label matching in `model/labels`, or the PromQL parser) and link it in the PR.
2. Add it to `plugin/scripts/check.go` with a code in kebab case, a message that says which selector matches nothing or too little and why, and a fix line.
3. Add a test in `tests/` on real `/metrics` output from `examples/checkout` or `tests/testdata`, plus a case that must stay quiet. If the check depends on how Prometheus stores values, add selectors to `tests/prometheus_test.go` and run it with `BUCKETGAP_PROMETHEUS` set.
4. Add the code to the table in `README.md` and to the skill's list in `plugin/skills/checking-histogram-buckets-in-alerts/SKILL.md`.
5. Run `make test`.
