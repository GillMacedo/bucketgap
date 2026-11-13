// Command bucketgap checks the le and quantile values that Prometheus rules
// and Grafana dashboards select against the buckets and quantiles a real
// /metrics scrape exposes, the way Prometheus 2 or 3 stores them. A selector
// such as x_bucket{le="0.3"} on a histogram with no 0.3 bucket, or le="1"
// on Prometheus 3, which stores "1.0", matches nothing, and an alert built on
// it never fires. bucketgap reads files only; it never connects anywhere.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const version = "0.1.0"

const (
	sevNote = iota
	sevWarn
	sevError
)

var sevNames = map[int]string{sevNote: "note", sevWarn: "WARN", sevError: "ERROR"}

type finding struct {
	sev      int
	Severity string `json:"severity"`
	Code     string `json:"code"`
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Where    string `json:"where"`
	Selector string `json:"selector"`
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
}

// inputError means the input can't be trusted; nothing is reported.
type inputError struct{ msg string }

func (e *inputError) Error() string { return e.msg }

func untrusted(format string, a ...any) error {
	return &inputError{fmt.Sprintf(format, a...)}
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

type report struct {
	Version     string         `json:"version"`
	Prometheus  int            `json:"prometheus"`
	Scrapes     []string       `json:"scrapes"`
	RuleFiles   int            `json:"ruleFiles"`
	Dashboards  int            `json:"dashboards"`
	Queries     int            `json:"queries"`
	Checked     int            `json:"checked"`
	NotChecked  map[string]int `json:"notChecked"`
	NotInScrape map[string]int `json:"notInScrape"`
	Skipped     []string       `json:"skippedFiles,omitempty"`
	Findings    []*finding     `json:"findings"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("bucketgap", flag.ContinueOnError)
	fl.SetOutput(stderr)
	var scrapes multiFlag
	fl.Var(&scrapes, "scrape", "a file holding one target's /metrics output; repeat for more targets (required)")
	prom := fl.Int("prometheus", 3, "the Prometheus major version that stores the data: 3 or 2")
	format := fl.String("format", "text", "output format: text or json")
	failOn := fl.String("fail-on", "error", "exit 1 when a finding is at or above this level: error, warn or never")
	showVersion := fl.Bool("version", false, "print the version")
	fl.Usage = func() {
		fmt.Fprintf(stderr, `usage: bucketgap --scrape METRICS.txt [--scrape ...] RULES|DASHBOARD|DIR ...

Checks the le and quantile values that Prometheus rule files, PrometheusRule
objects and Grafana dashboards select against the buckets and quantiles in
real /metrics output.
Exit 0: nothing at or above --fail-on. Exit 1: findings. Exit 2: input can't be trusted, nothing was checked, or bad usage.

`)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "bucketgap", version)
		return 0
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintln(stderr, "bucketgap: --format must be text or json")
		return 2
	}
	if *failOn != "error" && *failOn != "warn" && *failOn != "never" {
		fmt.Fprintln(stderr, "bucketgap: --fail-on must be error, warn or never")
		return 2
	}
	if *prom != 2 && *prom != 3 {
		fmt.Fprintln(stderr, "bucketgap: --prometheus must be 2 or 3")
		return 2
	}
	if len(scrapes) == 0 || fl.NArg() == 0 {
		fl.Usage()
		return 2
	}

	var scs []*scrape
	seenFile := map[string]bool{}
	for _, p := range scrapes {
		if seenFile[p] {
			fmt.Fprintf(stderr, "bucketgap: %s is given twice with --scrape\n", p)
			return 2
		}
		seenFile[p] = true
		sc, err := loadScrape(p)
		if err != nil {
			return refuse(stderr, err)
		}
		scs = append(scs, sc)
	}
	in, err := loadInputs(fl.Args())
	if err != nil {
		return refuse(stderr, err)
	}
	c := newChecker(scs, *prom)
	for _, e := range in.exprs {
		if err := c.checkExpr(e); err != nil {
			return refuse(stderr, err)
		}
	}
	if in.otherDS > 0 {
		c.unchecked["a dashboard query for a data source that isn't Prometheus"] += in.otherDS
	}
	rep := &report{
		Version: version, Prometheus: *prom, RuleFiles: in.ruleFiles, Dashboards: in.dashboards,
		Queries: len(in.exprs), Checked: c.checked, NotChecked: c.unchecked, NotInScrape: c.notIn,
		Skipped: in.skipped, Findings: c.findings,
	}
	for _, sc := range scs {
		rep.Scrapes = append(rep.Scrapes, sc.File)
	}
	if c.checked == 0 {
		total := 0
		for _, n := range c.unchecked {
			total += n
		}
		for _, n := range c.notIn {
			total += n
		}
		if total == 0 {
			return refuse(stderr, untrusted("nothing to check: none of the %d queries selects an le or quantile value or a _bucket series", len(in.exprs)))
		}
		var names []string
		for n := range c.notIn {
			names = append(names, n)
		}
		sort.Strings(names)
		msg := fmt.Sprintf("nothing checked: %d le or quantile selectors, and none names a metric in the scrapes", total)
		if len(names) > 0 {
			msg += "; metrics not in any scrape: " + strings.Join(names, ", ")
		}
		return refuse(stderr, untrusted("%s. Pass the /metrics output of the service these rules watch", msg))
	}
	sort.SliceStable(c.findings, func(i, j int) bool {
		a, b := c.findings[i], c.findings[j]
		if a.sev != b.sev {
			return a.sev > b.sev
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	if *format == "json" {
		if rep.Findings == nil {
			rep.Findings = []*finding{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
	} else {
		printText(stdout, rep)
	}
	limit := map[string]int{"error": sevError, "warn": sevWarn, "never": sevError + 1}[*failOn]
	for _, f := range c.findings {
		if f.sev >= limit {
			return 1
		}
	}
	return 0
}

func refuse(stderr io.Writer, err error) int {
	var ie *inputError
	if errors.As(err, &ie) {
		fmt.Fprintf(stderr, "bucketgap: can't trust the input, nothing was reported: %s\n", ie.msg)
	} else {
		fmt.Fprintf(stderr, "bucketgap: %v\n", err)
	}
	return 2
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func printText(w io.Writer, r *report) {
	fmt.Fprintf(w, "bucketgap %s, Prometheus %d: %s and %s (%s), %s\n", r.Version, r.Prometheus,
		plural(r.RuleFiles, "rule file", "rule files"), plural(r.Dashboards, "dashboard", "dashboards"),
		plural(r.Queries, "query", "queries"), plural(len(r.Scrapes), "scrape", "scrapes"))
	fmt.Fprintf(w, "checked %s against the scrapes\n", plural(r.Checked, "bucket or quantile selector", "bucket or quantile selectors"))
	var notes []string
	if len(r.NotInScrape) > 0 {
		var names []string
		total := 0
		for n, k := range r.NotInScrape {
			names = append(names, n)
			total += k
		}
		sort.Strings(names)
		notes = append(notes, fmt.Sprintf("not checked, metric not in any scrape: %d (%s)", total, strings.Join(names, ", ")))
	}
	var reasons []string
	for k := range r.NotChecked {
		reasons = append(reasons, k)
	}
	sort.Strings(reasons)
	for _, k := range reasons {
		notes = append(notes, fmt.Sprintf("not checked, %s: %d", k, r.NotChecked[k]))
	}
	for _, s := range r.Skipped {
		notes = append(notes, "skipped "+s+": no rules or dashboard queries")
	}
	for _, n := range notes {
		fmt.Fprintln(w, "note: "+n)
	}
	if len(r.Findings) == 0 {
		fmt.Fprintf(w, "\nNo problems in %s.\n", plural(r.Checked, "checked selector", "checked selectors"))
		return
	}
	errs, warns := 0, 0
	for _, f := range r.Findings {
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		fmt.Fprintf(w, "\n%s %s  %s  %s\n  %s\n  %s\n", f.Severity, f.Code, loc, f.Where, f.Selector, f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		}
		switch f.sev {
		case sevError:
			errs++
		case sevWarn:
			warns++
		}
	}
	fmt.Fprintf(w, "\n%s, %s\n", plural(errs, "error", "errors"), plural(warns, "warning", "warnings"))
}
