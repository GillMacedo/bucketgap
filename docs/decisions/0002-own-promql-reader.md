# 0002: A small PromQL reader instead of Prometheus's parser

## Context
bucketgap only needs vector selectors and their label matchers. Prometheus's parser would bring the Prometheus module and its dependencies into a plugin that must build with no module.

## Decision
Write a lexer and a selector reader that understand strings, comments, grouping clauses, function calls, subqueries, `offset`, `@`, quoted metric names and Grafana variables, and that refuse anything unbalanced or unterminated.

## Consequences
The reader may refuse an unusual but valid expression; that is a bug to fix, and it never produces a clean result by mistake. Its behavior is pinned by unit tests, and real rule sets (awesome-prometheus-alerts, kube-prometheus) were read without a refusal.
