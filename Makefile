.PHONY: test lint demo

test:
	test -z "$$(gofmt -l plugin tests examples/checkout/main.go)"
	go vet ./...
	cd examples/checkout && go vet ./...
	go test -race ./...

lint:
	claude plugin validate ./plugin --strict
	claude plugin validate . --strict

# Exit 1 means it found problems, which broken.yaml and dashboard.json have on purpose.
demo:
	go run ./plugin/scripts --scrape examples/checkout/metrics.txt examples/checkout/rules/broken.yaml examples/checkout/rules/dashboard.json || test $$? -eq 1
	go run ./plugin/scripts --scrape examples/checkout/metrics.txt examples/checkout/rules/fixed.yaml
