# Security

Report vulnerabilities through [GitHub's private vulnerability reporting](https://github.com/GillMacedo/bucketgap/security/advisories/new), not in a public issue.

`bucketgap` only reads the files you pass it. It doesn't run your code, connect to Prometheus, Grafana or a cluster, or write files. Label values from the scrape are printed in findings, so don't share its output if your metric labels hold secrets.

A missed or wrong finding is a normal bug. If `bucketgap` writes a file, opens a network connection, or reports a clean result for input it couldn't read, report it privately. `bucketgap` is a diagnostic aid and doesn't enforce anything.
