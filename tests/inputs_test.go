package tests

import (
	"strings"
	"testing"
)

// expectRefused checks that bucketgap refuses the input under every
// --fail-on value: exit 2, nothing on stdout, and a reason on stderr.
func expectRefused(t *testing.T, name string, args ...string) {
	t.Helper()
	for _, failOn := range []string{"error", "warn", "never"} {
		for _, format := range []string{"text", "json"} {
			a := append([]string{"--fail-on", failOn, "--format", format}, args...)
			r := runTool(t, a...)
			if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "can't trust the input") {
				t.Errorf("%s (--fail-on %s, %s): exit %d, want 2 with empty stdout\nstdout:\n%s\nstderr:\n%s", name, failOn, format, r.code, r.stdout, r.stderr)
			}
		}
	}
}

func histogramPart(t *testing.T, upToLE string) string {
	t.Helper()
	full := readFile(t, scrapeFile)
	i := strings.Index(full, `le="`+upToLE+`"}`)
	if i < 0 {
		t.Fatalf("no le=%s in the scrape", upToLE)
	}
	return full[:i+strings.Index(full[i:], "\n")+1]
}

var badScrapes = map[string]string{
	"empty":           "",
	"blank lines":     "\n\n\n",
	"garbage":         "hello world, this is not metrics\n",
	"binary":          "\x00\x01\x02\xff\xfe\n",
	"html error page": "<html><body><h1>502 Bad Gateway</h1></body></html>\n",
	"json error":      `{"status":"error","error":"unauthorized"}` + "\n",
	"404 text":        "404 page not found\n",
	"unauthorized":    "Unauthorized\n",
	"curl failure":    "curl: (7) Failed to connect to 127.0.0.1 port 8080 after 0 ms: Couldn't connect to server\n",
	"go build failure": "# example.com/checkout\n" +
		"./main.go:17:12: undefined: prometheus.NewHistogramVecc\n",
	"go run panic": "panic: listen tcp 127.0.0.1:8080: bind: address already in use\n\n" +
		"goroutine 1 [running]:\nmain.main()\n\t/src/main.go:44 +0x1d4\nexit status 2\n",
	"log.Fatal output":    "2026/11/13 11:20:05 listen tcp 127.0.0.1:8080: bind: address already in use\nexit status 1\n",
	"only comments":       "# HELP x A thing.\n# just a comment\n",
	"type line at end":    "# HELP x A thing.\n# TYPE x counter\nx 1\n# HELP y Another.\n# TYPE y histogram\n",
	"bad value":           "x_total{a=\"b\"} twelve\n",
	"unterminated labels": "x_bucket{le=\"0.5\" 3\n",
	"after EOF":           "# TYPE x counter\nx_total 1\n# EOF\nx_total 2\n",
}

func TestBadScrapes(t *testing.T) {
	for name, data := range badScrapes {
		p := writeFile(t, "metrics.txt", data)
		expectRefused(t, "scrape: "+name, "--scrape", p, fixedFile)
		// And when it's one of two scrapes, with the other real.
		expectRefused(t, "second scrape: "+name, "--scrape", scrapeFile, "--scrape", p, fixedFile)
	}
}

func TestCutScrapes(t *testing.T) {
	full := readFile(t, scrapeFile)
	cases := map[string]string{
		"cut mid-line":              full[:len(full)/2],
		"cut mid-line in histogram": histogramPart(t, "0.5")[:len(histogramPart(t, "0.5"))-5],
		"cut before +Inf":           histogramPart(t, "5"),
		"cut after +Inf":            histogramPart(t, "+Inf"),
		"cut inside summary":        full[:strings.Index(full, "checkout_queue_wait_seconds_sum")],
		"cut before histogram":      full[:strings.Index(full, "# TYPE checkout_request_duration_seconds")],
		"two scrapes in one":        full + full,
		"no final newline":          strings.TrimSuffix(full, "\n"),
	}
	for name, data := range cases {
		p := writeFile(t, "metrics.txt", data)
		for _, rules := range []string{fixedFile, brokenFile} {
			r := runTool(t, "--scrape", p, rules)
			// A scrape cut before the histogram is readable but lacks it:
			// that must be an error finding, never a clean result.
			if name == "cut before histogram" {
				if r.code != 1 || !strings.Contains(r.stdout, "not-in-scrape") {
					t.Errorf("%s with %s: exit %d, want 1 with not-in-scrape\n%s%s", name, rules, r.code, r.stdout, r.stderr)
				}
				continue
			}
			expectRefused(t, name+" with "+rules, "--scrape", p, rules)
		}
	}
}

// A scrape of a service the rules don't watch checks nothing, which is
// not a clean result.
func TestUnrelatedScrape(t *testing.T) {
	full := readFile(t, scrapeFile)
	other := full[strings.Index(full, "# HELP go_"):]
	p := writeFile(t, "other.txt", other)
	expectRefused(t, "unrelated scrape", "--scrape", p, fixedFile)
}

var badRules = map[string]string{
	"empty":                "",
	"blank":                "\n\n",
	"garbage":              "::: this is :: not yaml {\n",
	"binary":               "\x00\x01\x02\xff\n",
	"plain text":           "hello world\n",
	"helm failure":         "Error: INSTALLATION FAILED: template: checkout/templates/rules.yaml:3:5: executing \"x\" at <.Values.x>: nil pointer\n",
	"jsonnet failure":      "RUNTIME ERROR: Field does not exist: alerts\n\tmixin.libsonnet:12:5-20\n",
	"promtool failure":     "Checking rules.yaml\n  FAILED:\nrules.yaml: 5:15: group \"checkout\", rule 1, \"A\": could not parse expression\n",
	"deployment":           "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: checkout\n",
	"no groups":            "groups: []\n",
	"groups not a list":    "groups: {a: 1}\n",
	"no expr":              "groups:\n  - name: g\n    rules:\n      - alert: A\n        for: 5m\n",
	"empty expr":           "groups:\n  - name: g\n    rules:\n      - alert: A\n        expr: |\n",
	"alert and record":     "groups:\n  - name: g\n    rules:\n      - alert: A\n        record: b\n        expr: x\n",
	"expr not string":      "groups:\n  - name: g\n    rules:\n      - record: b\n        expr: [1, 2]\n",
	"unbalanced":           "groups:\n  - name: g\n    rules:\n      - record: b\n        expr: sum(rate(checkout_request_duration_seconds_bucket{le=\"0.5\"}[5m])\n",
	"ends with operator":   "groups:\n  - name: g\n    rules:\n      - record: b\n        expr: sum(checkout_request_duration_seconds_bucket{le=\"0.5\"}) /\n",
	"unterminated string":  "groups:\n  - name: g\n    rules:\n      - record: b\n        expr: 'checkout_request_duration_seconds_bucket{le=\"0.5}'\n",
	"bad escape":           "groups:\n  - name: g\n    rules:\n      - record: b\n        expr: 'checkout_request_duration_seconds_bucket{le=~\"1(\\.0)?\"}'\n",
	"bad regex":            "groups:\n  - name: g\n    rules:\n      - record: b\n        expr: 'checkout_request_duration_seconds_bucket{le=~\"(0.5\"}'\n",
	"unrendered template":  "groups:\n  - name: g\n    rules:\n      - alert: A\n        expr: checkout_request_duration_seconds_bucket{le=\"{{ .Values.le }}\"} > 0\n",
	"helm template syntax": "groups:\n  - name: g\n    rules:\n{{- range .Values.rules }}\n      - alert: {{ .name }}\n        expr: {{ .expr }}\n{{- end }}\n",
	"loki rule":            "groups:\n  - name: loki\n    rules:\n      - alert: Errors\n        expr: sum(rate({app=\"checkout\"} |= \"error\" [5m])) > 0\n",
	"no final newline":     "groups:\n  - name: g\n    rules:\n      - record: b\n        expr: checkout_request_duration_seconds_bucket{le=\"0.5\"}",
	"nothing to check":     "groups:\n  - name: g\n    rules:\n      - record: b\n        expr: sum(rate(checkout_request_duration_seconds_count[5m]))\n",
	"other service only":   "groups:\n  - name: g\n    rules:\n      - record: b\n        expr: sum(rate(payments_request_duration_seconds_bucket{le=\"0.5\"}[5m]))\n",
}

func TestBadRules(t *testing.T) {
	for name, data := range badRules {
		p := writeFile(t, "rules.yaml", data)
		expectRefused(t, "rules: "+name, "--scrape", scrapeFile, p)
		// A bad file next to a good one still makes the run untrusted.
		// Valid files that just have nothing to check are fine there.
		if name == "nothing to check" || name == "other service only" {
			continue
		}
		expectRefused(t, "rules with a good file: "+name, "--scrape", scrapeFile, fixedFile, p)
	}
}

var badDashboards = map[string]string{
	"empty":       "",
	"garbage":     "not json at all\n",
	"cut off":     `{"panels":[{"type":"timeseries","title":"x","targets":[{"expr":"checkout_request_duration_seconds_bucket{le=\"0.5\"}"`,
	"two values":  "{}\n{}\n",
	"no targets":  `{"title":"Empty","panels":[]}` + "\n",
	"loki only":   `{"panels":[{"type":"logs","title":"x","datasource":{"type":"loki","uid":"l"},"targets":[{"expr":"{app=\"a\"}"}]}]}` + "\n",
	"expr number": `{"panels":[{"type":"stat","title":"x","targets":[{"expr":5}]}]}` + "\n",
}

func TestBadDashboards(t *testing.T) {
	for name, data := range badDashboards {
		p := writeFile(t, "dash.json", data)
		expectRefused(t, "dashboard: "+name, "--scrape", scrapeFile, p)
	}
	full := readFile(t, dashFile)
	for _, n := range []int{1, len(full) / 3, len(full) / 2, len(full) - 3} {
		p := writeFile(t, "dash.json", full[:n])
		expectRefused(t, "dashboard cut", "--scrape", scrapeFile, p)
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{fixedFile},
		{"--scrape", scrapeFile},
		{"--prometheus", "4", "--scrape", scrapeFile, fixedFile},
		{"--format", "xml", "--scrape", scrapeFile, fixedFile},
		{"--fail-on", "info", "--scrape", scrapeFile, fixedFile},
		{"--scrape", scrapeFile, "--scrape", scrapeFile, fixedFile},
		{"--scrape", "does-not-exist.txt", fixedFile},
		{"--scrape", scrapeFile, "does-not-exist.yaml"},
	} {
		r := runTool(t, args...)
		if r.code != 2 || r.stdout != "" {
			t.Errorf("%v: exit %d, want 2 with empty stdout\n%s", args, r.code, r.stdout)
		}
	}
}
