package main

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestFormatOpenMetricsFloat(t *testing.T) {
	// 0.05 to 1e+06 match what Prometheus 3.16.0 stored for the example
	// service and tests/testdata/odd.txt; the rest follow the rule in its
	// labels.FormatOpenMetricsFloat.
	cases := map[float64]string{
		1: "1.0", 0: "0.0", 5: "5.0", 0.05: "0.05", 2.5: "2.5", 10: "10.0",
		100000: "100000.0", 1e6: "1e+06", 1e-05: "1e-05", 0.0001: "0.0001",
		math.Inf(1): "+Inf", 1e21: "1e+21", 123456789: "1.23456789e+08",
	}
	for f, want := range cases {
		if got := formatOpenMetricsFloat(f); got != want {
			t.Errorf("formatOpenMetricsFloat(%v) = %q, want %q", f, got, want)
		}
	}
}

func TestStored(t *testing.T) {
	if got := stored("1", 3, true); got != "1.0" {
		t.Errorf("Prometheus 3 histogram le=1: %q", got)
	}
	if got := stored("1", 3, false); got != "1" {
		t.Errorf("Prometheus 3 untyped le=1: %q", got)
	}
	if got := stored("1.0", 2, true); got != "1.0" {
		t.Errorf("Prometheus 2: %q", got)
	}
	if got := stored("0.30", 3, true); got != "0.3" {
		t.Errorf("Prometheus 3 le=0.30: %q", got)
	}
}

func TestSelectors(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{`histogram_quantile(0.9, sum by (le, job) (rate(a_bucket{job="x"}[5m])))`, []string{`a_bucket job="x"`}},
		{`sum without (le) (a_bucket{le="0.5"}) / on (job) group_left (team) b`, []string{`a_bucket le="0.5"`, `b`}},
		{`{__name__="a_bucket", le=~"1(\\.0)?"} offset 5m`, []string{`a_bucket __name__="a_bucket" le=~"1(\.0)?"`}},
		{`{"a.b_bucket", le="1"}`, []string{`a.b_bucket le="1"`}},
		{`rate(a_bucket{le='0.5'}[$__rate_interval])`, []string{`a_bucket le="0.5"`}},
		{`a_bucket{le="$le"} and b_count @ end()`, []string{`a_bucket le="$le"(var)`, `b_count`}},
		{`max_over_time(a{quantile="0.99"}[1h:5m]) > bool 1e3`, []string{`a quantile="0.99"`}},
		{"sum(a_bucket{le=`0.5`}) # comment {le=\"9\"}", []string{`a_bucket le="0.5"`}},
		{`label_replace(a_bucket, "x", "$1", "le", "(.*)")`, []string{`a_bucket`}},
		{`$metric{le="1"}`, []string{`(var) le="1"`}},
		{`topk by (job) (3, a)`, []string{`a`}},
		{`:a:b{le="1"}`, []string{`:a:b le="1"`}},
	}
	for _, c := range cases {
		sels, err := selectors(c.expr)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		var got []string
		for _, s := range sels {
			parts := []string{s.Name}
			if s.NameVar && s.Name == "" {
				parts[0] = "(var)"
			}
			for _, m := range s.Matchers {
				v := m.Label + m.Op + `"` + m.Value + `"`
				if m.HasVar {
					v += "(var)"
				}
				parts = append(parts, v)
			}
			got = append(got, strings.Join(parts, " "))
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.expr, got, c.want)
		}
	}
}

func TestSelectorErrors(t *testing.T) {
	for _, e := range []string{
		``, `   `, `sum(a`, `a{le="1"`, `a{le="1}`, `a{le=1}`, `a{le}`, `a / `, `a)`, `a[5m`,
		`a{le="\q"}`, `a{le=~"1(\.0)?"}`, `a ! b`, `a{,}`, `{"a", "b"}`, `$`, `a{le="1" job="x"}`,
	} {
		if _, err := selectors(e); err == nil {
			t.Errorf("%q: no error", e)
		}
	}
}

func TestTopBranches(t *testing.T) {
	cases := map[string][]string{
		`0.25|0.3`: {"0.25", "0.3"},
		`1(\.0)?`:  nil,
		`(1|2)|3`:  {"(1|2)", "3"},
		`[|]|a`:    {"[|]", "a"},
		`1\|2`:     nil,
	}
	for re, want := range cases {
		if got := topBranches(re); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %q, want %q", re, got, want)
		}
	}
}

func TestParseSample(t *testing.T) {
	ok := []string{
		`a 1`,
		`a{} 1`,
		`a_bucket{x="y",le="0.5"} 3 1700000000000`,
		`a_bucket{le="+Inf"} 3 # {trace_id="abc"} 0.5 1700000000.000`,
		`{"a.b",le="1"} 3`,
		`a{x="a \"quoted\" \\ value\n"} NaN`,
		`a{x="y",} -Inf`,
	}
	for _, l := range ok {
		if _, _, err := parseSample(l); err != nil {
			t.Errorf("%s: %v", l, err)
		}
	}
	bad := []string{`a`, `a{x="y"}`, `a{x="y"} one`, `a{x=y} 1`, `a{x="y" 1`, `1a 1`, `a 1 2 3`, `a{x="1",x="2"} 1`}
	for _, l := range bad {
		if _, _, err := parseSample(l); err == nil {
			t.Errorf("%s: no error", l)
		}
	}
}
