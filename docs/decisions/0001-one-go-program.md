# 0001: One Go program in a skill, with no module

## Context
The check reads files and prints a report. An agent needs a command it can run, not a long-lived tool server.

## Decision
Ship a Claude Code skill whose script is one Go program in `plugin/scripts`, built with `go build` from its `.go` files. Use only the standard library plus a copy of go.yaml.in/yaml/v2 (Apache-2.0 and MIT, licenses kept next to it), so the installed plugin has no `go.mod` and builds offline.

## Consequences
No MCP server and no npm package. Anyone with Go 1.22 or newer can run it with `go run`. The PromQL reader is hand-written instead of Prometheus's parser (see 0002).
