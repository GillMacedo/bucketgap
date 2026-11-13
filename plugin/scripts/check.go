package main

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type checker struct {
	scrapes []*scrape
	prom    int
	// counts
	checked   int
	unchecked map[string]int // reason -> count
	notIn     map[string]int // metric name not in any scrape -> count
	findings  []*finding
	seen      map[string]bool
}

func newChecker(scrapes []*scrape, prom int) *checker {
	return &checker{scrapes: scrapes, prom: prom, unchecked: map[string]int{}, notIn: map[string]int{}, seen: map[string]bool{}}
}

func (c *checker) add(e expr, sev int, code, sel, msg, fix string) {
	key := e.File + "\x00" + e.Where + "\x00" + code + "\x00" + sel + "\x00" + msg
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.findings = append(c.findings, &finding{sev: sev, Severity: sevNames[sev], Code: code, File: e.File, Line: e.Line, Where: e.Where, Selector: sel, Message: msg, Fix: fix})
}

func (c *checker) checkExpr(e expr) error {
	sels, err := selectors(e.Text)
	if err == nil && strings.Contains(e.Text, "{{") && strings.Contains(e.Text, "}}") {
		err = fmt.Errorf("{{ }} in the expression looks like an unrendered template; render it first (helm template, kustomize build)")
	}
	if err != nil {
		loc := e.File
		if e.Line > 0 {
			loc = fmt.Sprintf("%s:%d", e.File, e.Line)
		}
		return untrusted("%s: %s: can't read the PromQL: %v", loc, e.Where, err)
	}
	for _, s := range sels {
		var ms []matcher
		for _, m := range s.Matchers {
			if (m.Label == "le" || m.Label == "quantile") && (m.Op == "=" || m.Op == "=~") {
				ms = append(ms, m)
			}
		}
		isBucket := strings.HasSuffix(s.Name, "_bucket")
		if len(ms) == 0 && !isBucket {
			continue
		}
		if s.NameVar {
			c.unchecked["the metric name is a variable or a regex"] += max(len(ms), 1)
			continue
		}
		var have []*scrape
		for _, sc := range c.scrapes {
			if sc.names[s.Name] {
				have = append(have, sc)
			}
		}
		if len(have) == 0 {
			if c.missingSeries(e, s, ms) || c.missingByPrefix(e, s, ms) {
				continue
			}
			c.notIn[s.Name] += max(len(ms), 1)
			continue
		}
		if len(ms) == 0 {
			c.checked++ // a _bucket selector whose series exist
			continue
		}
		for _, m := range ms {
			if m.HasVar {
				c.unchecked["the "+m.Label+" value is a dashboard variable"]++
				continue
			}
			if err := c.checkMatcher(e, s.Name, m, have); err != nil {
				return err
			}
		}
	}
	return nil
}

// missingSeries reports a selector whose name doesn't exist although its
// family does, such as x_bucket on a summary. It returns true if it did.
func (c *checker) missingSeries(e expr, s selector, ms []matcher) bool {
	base := s.Name
	for _, suf := range []string{"_bucket", "_count", "_sum"} {
		base = strings.TrimSuffix(base, suf)
	}
	for _, sc := range c.scrapes {
		t, typed := sc.types[base]
		if !typed {
			continue
		}
		var series []string
		for n := range sc.names {
			if familyOf(n, sc.types) == base {
				series = append(series, n)
			}
		}
		if len(series) == 0 {
			continue
		}
		sort.Strings(series)
		c.checked++
		text := s.Name
		if len(ms) > 0 {
			text = selText(s.Name, ms[0])
		}
		c.add(e, sevError, "no-such-series", text,
			fmt.Sprintf("%s doesn't exist in %s: %s is a %s with the series %s, so this selector matches nothing", s.Name, sc.File, base, t, strings.Join(series, ", ")),
			fmt.Sprintf("query the series %s has; a %s doesn't have %s", base, t, strings.TrimPrefix(s.Name, base)))
		return true
	}
	return false
}

func selText(name string, m matcher) string {
	return fmt.Sprintf("%s{%s%s%s}", name, m.Label, m.Op, strconv.Quote(m.Value))
}

// valuesIn returns the stored label values on name in one scrape.
func (c *checker) valuesIn(sc *scrape, name, label string) map[string]bool {
	src := sc.le[name]
	if label == "quantile" {
		src = sc.quantile[name]
	}
	out := map[string]bool{}
	for v, norm := range src {
		out[stored(v, c.prom, norm)] = true
	}
	return out
}

func (c *checker) checkMatcher(e expr, name string, m matcher, have []*scrape) error {
	c.checked++
	sel := selText(name, m)
	what := "bucket"
	if m.Label == "quantile" {
		what = "quantile"
	}
	per := make([]map[string]bool, len(have))
	all := map[string]bool{}
	for i, sc := range have {
		per[i] = c.valuesIn(sc, name, m.Label)
		for v := range per[i] {
			all[v] = true
		}
	}
	if len(all) == 0 {
		c.add(e, sevError, "no-label", sel,
			fmt.Sprintf("%s has no %s label in the scrape, so this selector matches nothing", name, m.Label),
			fmt.Sprintf("check the metric name: %s values live on %s", m.Label, map[string]string{"le": "the _bucket series of a histogram", "quantile": "the series of a summary named without a suffix"}[m.Label]))
		return nil
	}
	list := sortedValues(all)
	switch m.Op {
	case "=":
		var hit []int
		for i := range have {
			if per[i][m.Value] {
				hit = append(hit, i)
			}
		}
		if len(hit) == 0 {
			if same := numericTwin(m.Value, all); same != "" {
				c.add(e, sevError, m.Label+"-format", sel, c.formatMsg(name, m, same), c.formatFix(m, same))
				return nil
			}
			c.add(e, sevError, "no-such-"+what, sel,
				fmt.Sprintf("%s has no %s at %s, so this selector matches nothing; the %ss are %s", name, what, m.Value, what, strings.Join(list, " ")),
				nearestFix(m, list, what))
			return nil
		}
		if len(hit) < len(have) {
			c.add(e, sevWarn, "missing-in-some", sel, missingMsg(name, m, have, hit), fmt.Sprintf("give every target the same %ss, or don't rely on %s=%q", what, m.Label, m.Value))
		}
	case "=~":
		re, err := regexp.Compile("^(?:" + m.Value + ")$")
		if err != nil {
			return untrusted("%s: %s: the regex in %s doesn't compile: %v", e.File, e.Where, sel, err)
		}
		var hit []int
		for i := range have {
			for v := range per[i] {
				if re.MatchString(v) {
					hit = append(hit, i)
					break
				}
			}
		}
		if len(hit) == 0 {
			if same := numericTwin(strings.ReplaceAll(m.Value, `\.`, "."), all); same != "" {
				c.add(e, sevError, m.Label+"-format", sel, c.formatMsg(name, m, same), c.formatFix(m, same))
				return nil
			}
			fix := nearestFix(m, list, what)
			code := "no-such-" + what
			msg := fmt.Sprintf("the regex %s matches none of %s's %ss, so this selector matches nothing; the %ss are %s", strconv.Quote(m.Value), name, what, what, strings.Join(list, " "))
			c.add(e, sevError, code, sel, msg, fix)
			return nil
		}
		for _, br := range topBranches(m.Value) {
			bre, err := regexp.Compile("^(?:" + br + ")$")
			if err != nil {
				continue
			}
			var bhit []int
			for i := range have {
				for v := range per[i] {
					if bre.MatchString(v) {
						bhit = append(bhit, i)
						break
					}
				}
			}
			if len(bhit) > 0 {
				if len(bhit) < len(have) && len(hit) == len(have) {
					c.add(e, sevWarn, "missing-in-some", sel, missingMsg(name, matcher{Label: m.Label, Op: "=~", Value: br}, have, bhit), fmt.Sprintf("give every target the same %ss", what))
				}
				continue
			}
			lit := strings.ReplaceAll(br, `\.`, ".")
			if numericTwin(lit, all) != "" {
				continue // a deliberate second spelling, such as 1|1.0
			}
			c.add(e, sevWarn, "regex-branch-matches-nothing", sel,
				fmt.Sprintf("the alternative %q matches none of %s's %ss (%s), so the result leaves out what it was meant to include", br, name, what, strings.Join(list, " ")),
				nearestFix(matcher{Label: m.Label, Op: "=", Value: lit}, list, what))
		}
		if len(hit) < len(have) {
			c.add(e, sevWarn, "missing-in-some", sel, missingMsg(name, m, have, hit), fmt.Sprintf("give every target the same %ss", what))
		}
	}
	return nil
}

func (c *checker) formatMsg(name string, m matcher, same string) string {
	if c.prom >= 3 {
		return fmt.Sprintf("Prometheus 3 stores this %s of %s as %s=%q, so %s=%q matches nothing (Prometheus 2 kept the value as exposed)", map[string]string{"le": "bucket", "quantile": "quantile"}[m.Label], name, m.Label, same, m.Label, m.Value)
	}
	return fmt.Sprintf("Prometheus 2 keeps the %s value as the target exposes it, %s=%q, so %s=%q matches nothing", m.Label, m.Label, same, m.Label, m.Value)
}

func (c *checker) formatFix(m matcher, same string) string {
	if c.prom >= 3 && strings.HasSuffix(same, ".0") {
		bare := strings.TrimSuffix(same, ".0")
		return fmt.Sprintf(`write %s="%s"; while some servers still run Prometheus 2, %s=~"%s(\\.0)?" matches both`, m.Label, same, m.Label, bare)
	}
	return fmt.Sprintf("write %s=%q", m.Label, same)
}

func missingMsg(name string, m matcher, have []*scrape, hit []int) string {
	in := map[int]bool{}
	for _, i := range hit {
		in[i] = true
	}
	var miss []string
	for i, sc := range have {
		if !in[i] {
			miss = append(miss, sc.File)
		}
	}
	return fmt.Sprintf("%s%s%q matches in %d of %d scrapes; %s has no such %s in %s, so sums over all targets leave those targets out", m.Label, m.Op, m.Value, len(hit), len(have), name, m.Label, strings.Join(miss, ", "))
}

// numericTwin returns a value in all that is the same number as v but
// spelled differently, or "".
func numericTwin(v string, all map[string]bool) string {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return ""
	}
	for _, s := range sortedValues(all) {
		g, err := strconv.ParseFloat(s, 64)
		if err == nil && s != v && (g == f || (math.IsInf(g, 1) && math.IsInf(f, 1))) {
			return s
		}
	}
	return ""
}

func sortedValues(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		a, ea := strconv.ParseFloat(out[i], 64)
		b, eb := strconv.ParseFloat(out[j], 64)
		if ea == nil && eb == nil && a != b {
			return a < b
		}
		if (ea == nil) != (eb == nil) {
			return ea == nil
		}
		return out[i] < out[j]
	})
	return out
}

func nearestFix(m matcher, list []string, what string) string {
	f, err := strconv.ParseFloat(m.Value, 64)
	if err != nil {
		return fmt.Sprintf("use one of the %ss listed", what)
	}
	below, above := "", ""
	for _, s := range list {
		g, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		if g < f {
			below = s
		}
		if g > f && above == "" {
			above = s
		}
	}
	var opts []string
	if below != "" {
		opts = append(opts, fmt.Sprintf("%s=%q", m.Label, below))
	}
	if above != "" {
		opts = append(opts, fmt.Sprintf("%s=%q", m.Label, above))
	}
	add := "add " + m.Value + " to the buckets where the histogram is defined"
	if what == "quantile" {
		add = "add " + m.Value + " to the summary's objectives"
	}
	if len(opts) == 0 {
		return add
	}
	return "use " + strings.Join(opts, " or ") + ", or " + add
}

// topBranches splits a regex at top-level | outside groups and classes.
func topBranches(re string) []string {
	var out []string
	depth, start := 0, 0
	inClass := false
	for i := 0; i < len(re); i++ {
		switch re[i] {
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '(':
			if !inClass {
				depth++
			}
		case ')':
			if !inClass {
				depth--
			}
		case '|':
			if depth == 0 && !inClass {
				out = append(out, re[start:i])
				start = i + 1
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return append(out, re[start:])
}

// missingByPrefix reports a selected metric that isn't in any scrape while
// other metrics with the same first name segment are, such as
// checkout_request_seconds_bucket next to checkout_queue_wait_seconds. The
// selector then matches nothing on these targets. The same check catches a
// scrape cut off between two metrics, which the text format can't show.
func (c *checker) missingByPrefix(e expr, s selector, ms []matcher) bool {
	i := strings.IndexByte(s.Name, '_')
	if i <= 0 {
		return false
	}
	prefix := s.Name[:i+1]
	var others []string
	for _, sc := range c.scrapes {
		for n := range sc.names {
			if strings.HasPrefix(n, prefix) {
				others = append(others, n)
			}
		}
	}
	if len(others) == 0 {
		return false
	}
	c.checked++
	text := s.Name
	if len(ms) > 0 {
		text = selText(s.Name, ms[0])
	}
	c.add(e, sevError, "not-in-scrape", text,
		fmt.Sprintf("%s isn't in the scrapes, though %d other %s metrics are, so on these targets this selector matches nothing. Causes: a renamed or removed metric, a typo, a vector that hasn't recorded anything yet (client libraries omit it until first use), or a scrape cut off between two metrics", s.Name, len(others), prefix),
		"fix the name, or scrape again after the service has handled traffic; if another service exposes it, pass that service's /metrics with another --scrape")
	return true
}
