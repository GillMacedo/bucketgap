package tests

import (
	"sort"
	"strings"
	"testing"
)

func codes(rep jsonReport) []string {
	var out []string
	for _, f := range rep.Findings {
		out = append(out, f.Severity+" "+f.Code+" "+f.Selector)
	}
	sort.Strings(out)
	return out
}

func TestBrokenExample(t *testing.T) {
	rep, r := runJSON(t, "--scrape", scrapeFile, brokenFile, dashFile)
	if r.code != 1 {
		t.Fatalf("exit %d, want 1\n%s%s", r.code, r.stdout, r.stderr)
	}
	want := []string{
		`ERROR le-format checkout_request_duration_seconds_bucket{le="1"}`,
		`ERROR le-format checkout_request_duration_seconds_bucket{le="5"}`,
		`ERROR no-such-bucket checkout_request_duration_seconds_bucket{le="0.3"}`,
		`ERROR no-such-quantile checkout_queue_wait_seconds{quantile="0.95"}`,
		`ERROR no-such-series checkout_queue_wait_seconds_bucket`,
		`WARN regex-branch-matches-nothing checkout_request_duration_seconds_bucket{le=~"0.25|0.3"}`,
	}
	if got := codes(rep); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if rep.NotInScrape["payments_request_duration_seconds_bucket"] != 1 {
		t.Errorf("payments metric should be listed as not checked: %v", rep.NotInScrape)
	}
}

func TestFixedExampleIsQuiet(t *testing.T) {
	for _, failOn := range []string{"error", "warn"} {
		r := runTool(t, "--fail-on", failOn, "--scrape", scrapeFile, fixedFile)
		if r.code != 0 {
			t.Fatalf("--fail-on %s: exit %d, want 0\n%s%s", failOn, r.code, r.stdout, r.stderr)
		}
		if !strings.Contains(r.stdout, "No problems in 5 checked selectors.") || strings.Contains(r.stdout, "WARN") {
			t.Errorf("unexpected output:\n%s", r.stdout)
		}
	}
}

// On Prometheus 2 the le values are kept as exposed, so le="1" is right and
// le="1.0" is the mistake.
func TestPrometheus2(t *testing.T) {
	rep, r := runJSON(t, "--prometheus", "2", "--scrape", scrapeFile, fixedFile, brokenFile)
	if r.code != 1 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	var formats []string
	for _, f := range rep.Findings {
		if f.Code == "le-format" {
			formats = append(formats, f.Selector)
		}
	}
	want := `checkout_request_duration_seconds_bucket{le="1.0"}`
	if len(formats) != 1 || formats[0] != want {
		t.Errorf("le-format findings %v, want only %s", formats, want)
	}
}

func TestFailOn(t *testing.T) {
	cases := []struct {
		failOn string
		want   int
	}{{"error", 1}, {"warn", 1}, {"never", 0}}
	for _, c := range cases {
		r := runTool(t, "--fail-on", c.failOn, "--scrape", scrapeFile, brokenFile)
		if r.code != c.want {
			t.Errorf("--fail-on %s: exit %d, want %d", c.failOn, r.code, c.want)
		}
		if !strings.Contains(r.stdout, "ERROR no-such-bucket") {
			t.Errorf("--fail-on %s: findings must still be printed:\n%s", c.failOn, r.stdout)
		}
	}
	// Warnings alone fail only with --fail-on warn.
	rules := writeFile(t, "w.yaml", "groups:\n  - name: g\n    rules:\n      - record: r\n        expr: sum(checkout_request_duration_seconds_bucket{le=~\"0.25|0.3\"})\n")
	for failOn, want := range map[string]int{"error": 0, "warn": 1, "never": 0} {
		r := runTool(t, "--fail-on", failOn, "--scrape", scrapeFile, rules)
		if r.code != want {
			t.Errorf("warning only, --fail-on %s: exit %d, want %d\n%s", failOn, r.code, want, r.stdout)
		}
	}
}

func TestDirectoryInput(t *testing.T) {
	rep, r := runJSON(t, "--scrape", scrapeFile, "examples/checkout/rules")
	if r.code != 1 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	if len(rep.Findings) != 6 {
		t.Errorf("%d findings, want 6: %v", len(rep.Findings), codes(rep))
	}
}

// Two targets with different buckets: a value only some have makes sums
// over all targets silently leave the others out.
func TestMixedBuckets(t *testing.T) {
	old := strings.ReplaceAll(readFile(t, scrapeFile), `le="0.25"`, `le="0.3"`)
	oldFile := writeFile(t, "old.txt", old)
	rep, r := runJSON(t, "--scrape", scrapeFile, "--scrape", oldFile, fixedFile)
	if r.code != 0 {
		t.Fatalf("exit %d, want 0 (warnings only)\n%s%s", r.code, r.stdout, r.stderr)
	}
	n := 0
	for _, f := range rep.Findings {
		if f.Code == "missing-in-some" {
			n++
			if !strings.Contains(f.Message, oldFile) {
				t.Errorf("message should name the scrape without the bucket: %s", f.Message)
			}
		}
	}
	if n != 2 {
		t.Errorf("%d missing-in-some findings, want 2 (le=\"0.25\" and le=~\"0.1|0.25\"): %v", n, codes(rep))
	}
}

func TestPrometheusRuleObject(t *testing.T) {
	rules := readFile(t, brokenFile)
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: other\ndata:\n  a: b\n---\n")
	b.WriteString("apiVersion: monitoring.coreos.com/v1\nkind: PrometheusRule\nmetadata:\n  name: checkout\nspec:\n")
	for _, l := range strings.Split(strings.TrimRight(rules, "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}
	p := writeFile(t, "pr.yaml", b.String())
	rep, r := runJSON(t, "--scrape", scrapeFile, p)
	if r.code != 1 || len(rep.Findings) != 5 {
		t.Fatalf("exit %d, %d findings, want 1 and 5: %v\n%s", r.code, len(rep.Findings), codes(rep), r.stderr)
	}
}

func TestNotInScrapeWithSamePrefix(t *testing.T) {
	rules := writeFile(t, "typo.yaml", "groups:\n  - name: g\n    rules:\n      - alert: A\n        expr: sum(rate(checkout_request_duration_secs_bucket{le=\"0.5\"}[5m])) > 0\n")
	rep, r := runJSON(t, "--scrape", scrapeFile, rules)
	if r.code != 1 || len(rep.Findings) != 1 || rep.Findings[0].Code != "not-in-scrape" {
		t.Fatalf("exit %d, findings %v; want one not-in-scrape", r.code, codes(rep))
	}
}

func TestTextOutput(t *testing.T) {
	r := runTool(t, "--scrape", scrapeFile, brokenFile)
	for _, want := range []string{
		"ERROR no-such-bucket  examples/checkout/rules/broken.yaml:5  group checkout, alert CheckoutSlowRequests",
		`fix: use le="0.25" or le="0.5"`,
		"4 errors, 1 warning",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
}

// Loki and other non-Prometheus queries in a dashboard are skipped and
// counted, not read as PromQL.
func TestMixedDashboard(t *testing.T) {
	rep, r := runJSON(t, "--scrape", scrapeFile, "tests/testdata/mixed-dashboard.json")
	if r.code != 1 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	if got := codes(rep); len(got) != 1 || got[0] != `ERROR no-such-bucket checkout_request_duration_seconds_bucket{le="0.3"}` {
		t.Errorf("findings %v", got)
	}
	if rep.Checked != 2 || rep.NotChecked["a dashboard query for a data source that isn't Prometheus"] != 2 {
		t.Errorf("checked %d, not checked %v", rep.Checked, rep.NotChecked)
	}
}
