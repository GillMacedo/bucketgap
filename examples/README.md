# Examples

`checkout/` is a made-up Go service. `main.go` records request latency in a `prometheus/client_golang` v1.20.5 histogram (buckets 0.05, 0.1, 0.25, 0.5, 1, 2.5 and 5 seconds) and queue wait in a summary (quantiles 0.5, 0.9 and 0.99), and serves them on `/metrics`. Its dependencies are vendored, so it builds offline.

`checkout/metrics.txt` is its real `/metrics` output, saved after 23 requests:

    cd examples/checkout && go build -o /tmp/checkout . && /tmp/checkout --listen 127.0.0.1:8080 &
    for i in $(seq 23); do curl -s 127.0.0.1:8080/checkout > /dev/null; done
    curl -sf 127.0.0.1:8080/metrics > examples/checkout/metrics.txt

`checkout/rules/broken.yaml` and `checkout/rules/dashboard.json` have common mistakes; `checkout/rules/fixed.yaml` is the corrected rule file. See the main README for bucketgap's output on each.
