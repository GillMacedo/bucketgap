package main

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// scrape is one target's /metrics output in the Prometheus text format or
// OpenMetrics text format.
type scrape struct {
	File string
	// le and quantile values exactly as written, per sample name. The
	// bool is true when Prometheus 3 normalizes the value: le under a
	// # TYPE histogram line, quantile under # TYPE summary. Like the
	// Prometheus text parser, the last # TYPE line applies to every sample
	// after it, whatever the sample's name.
	le       map[string]map[string]bool
	quantile map[string]map[string]bool
	// every sample name, and the declared type of every family.
	names map[string]bool
	types map[string]string
	// samples counts sample lines.
	samples int
}

func loadScrape(path string) (*scrape, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, untrusted("%v", err)
	}
	return parseScrape(path, string(data))
}

// parseScrape reads exposition text strictly. Anything it can't read in full
// makes the whole scrape untrusted: a scrape that is cut off, mixed with
// other output, or two scrapes pasted together could hide buckets.
func parseScrape(file, text string) (*scrape, error) {
	s := &scrape{
		File:     file,
		le:       map[string]map[string]bool{},
		quantile: map[string]map[string]bool{},
		names:    map[string]bool{},
		types:    map[string]string{},
	}
	if strings.TrimSpace(text) == "" {
		return nil, untrusted("%s: the scrape is empty", file)
	}
	if !strings.HasSuffix(text, "\n") {
		return nil, untrusted("%s: the scrape doesn't end with a newline, so it may be cut off; save the full /metrics output (curl -sf URL > file)", file)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	helpSeen := map[string]bool{}
	sawEOF := false
	curType := ""              // type on the last # TYPE line
	lastTyped := ""            // family of the last # TYPE line
	lastTypedHasSample := true // whether a sample of it followed
	type histSeries struct {
		inf, count bool
	}
	hist := map[string]*histSeries{} // family + labels without le
	histOrder := []string{}
	for i, raw := range lines {
		n := i + 1
		line := strings.TrimRight(raw, "\r")
		if sawEOF {
			return nil, untrusted("%s:%d: text after # EOF; pass one scrape per --scrape", file, n)
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			f := strings.Fields(line)
			if len(f) == 2 && f[1] == "EOF" {
				sawEOF = true
				continue
			}
			if len(f) >= 3 && (f[1] == "HELP" || f[1] == "TYPE" || f[1] == "UNIT") {
				if !validMetricName(f[2]) {
					return nil, untrusted("%s:%d: %q is not a valid metric name", file, n, f[2])
				}
				key := f[1] + " " + f[2]
				if helpSeen[key] {
					return nil, untrusted("%s:%d: a second # %s line for %s; the file holds more than one scrape. Pass each target's scrape with its own --scrape", file, n, f[1], f[2])
				}
				helpSeen[key] = true
				if f[1] == "TYPE" {
					if len(f) != 4 {
						return nil, untrusted("%s:%d: malformed # TYPE line", file, n)
					}
					switch f[3] {
					case "counter", "gauge", "histogram", "summary", "untyped", "unknown", "gaugehistogram", "stateset", "info":
					default:
						return nil, untrusted("%s:%d: unknown metric type %q", file, n, f[3])
					}
					if !lastTypedHasSample {
						// A family with no samples is normal for an empty vector
						// in the middle; only the end of the file is suspicious.
						lastTypedHasSample = true
					}
					s.types[f[2]] = f[3]
					curType = f[3]
					lastTyped, lastTypedHasSample = f[2], false
				}
				continue
			}
			// Other comments are allowed by the text format.
			continue
		}
		name, labels, err := parseSample(line)
		if err != nil {
			return nil, untrusted("%s:%d: %v; this doesn't look like /metrics output", file, n, err)
		}
		s.samples++
		s.names[name] = true
		if lastTyped != "" && familyOf(name, s.types) == lastTyped {
			lastTypedHasSample = true
		}
		if v, ok := labels["le"]; ok {
			if s.le[name] == nil {
				s.le[name] = map[string]bool{}
			}
			s.le[name][v] = curType == "histogram"
		}
		if v, ok := labels["quantile"]; ok {
			if s.quantile[name] == nil {
				s.quantile[name] = map[string]bool{}
			}
			s.quantile[name][v] = curType == "summary"
		}
		// Every series of a declared histogram needs its +Inf bucket and its
		// _count, and every summary series its _count; a scrape cut off
		// inside a family loses them.
		for _, suf := range []string{"_bucket", "_count", ""} {
			base := strings.TrimSuffix(name, suf)
			t := s.types[base]
			if (suf != "" && base == name) || (t != "histogram" && t != "summary") {
				continue
			}
			if (t == "histogram" && suf == "") || (t == "summary" && suf == "_bucket") {
				continue
			}
			key := base + "\x00" + seriesKey(labels, map[string]string{"histogram": "le", "summary": "quantile"}[t])
			h := hist[key]
			if h == nil {
				h = &histSeries{inf: t == "summary"} // summaries have no +Inf bucket
				hist[key] = h
				histOrder = append(histOrder, key)
			}
			if suf == "_count" {
				h.count = true
			} else if v := labels["le"]; v != "" {
				if f, err := strconv.ParseFloat(v, 64); err == nil && math.IsInf(f, 1) {
					h.inf = true
				}
			}
		}
	}
	if s.samples == 0 {
		return nil, untrusted("%s: no samples; this isn't /metrics output", file)
	}
	if !lastTypedHasSample {
		return nil, untrusted("%s: the file ends after the # TYPE line of %s with no samples, so it may be cut off", file, lastTyped)
	}
	for _, key := range histOrder {
		h := hist[key]
		fam := key[:strings.IndexByte(key, 0)]
		if !h.inf {
			return nil, untrusted("%s: histogram %s has a series with no le=\"+Inf\" bucket, so the scrape is cut off or broken", file, fam)
		}
		if !h.count {
			return nil, untrusted("%s: %s %s has a series with no %s_count, so the scrape is cut off or broken", file, s.types[fam], fam, fam)
		}
	}
	return s, nil
}

// familyOf maps a sample name to the declared family it belongs to.
func familyOf(name string, types map[string]string) string {
	if _, ok := types[name]; ok {
		return name
	}
	for _, suf := range []string{"_bucket", "_count", "_sum", "_total", "_created", "_gcount", "_gsum", "_info"} {
		if b := strings.TrimSuffix(name, suf); b != name {
			if _, ok := types[b]; ok {
				return b
			}
		}
	}
	return name
}

func seriesKey(labels map[string]string, skip string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if k != skip {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(1)
		b.WriteString(labels[k])
		b.WriteByte(2)
	}
	return b.String()
}

func validMetricName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r == ':' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// parseSample reads one sample line: name{labels} value [timestamp] [# exemplar].
func parseSample(line string) (string, map[string]string, error) {
	i := 0
	for i < len(line) && line[i] != '{' && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	name := line[:i]
	labels := map[string]string{}
	if !validMetricName(name) {
		if name == "" && i < len(line) && line[i] == '{' {
			// OpenMetrics 2 / Prometheus 3 quoted names: {"name",a="b"}
		} else {
			return "", nil, fmt.Errorf("%q is not a valid metric name", truncate(name, 40))
		}
	}
	if i < len(line) && line[i] == '{' {
		end, err := parseLabels(line, i+1, labels, &name)
		if err != nil {
			return "", nil, err
		}
		i = end
	}
	if name == "" {
		return "", nil, fmt.Errorf("sample has no metric name")
	}
	rest := strings.TrimLeft(line[i:], " \t")
	if len(rest) == len(line[i:]) {
		return "", nil, fmt.Errorf("no space before the value")
	}
	if h := strings.Index(rest, " # "); h >= 0 {
		rest = rest[:h] // exemplar
	}
	f := strings.Fields(rest)
	if len(f) < 1 || len(f) > 2 {
		return "", nil, fmt.Errorf("expected a value and an optional timestamp, got %q", truncate(rest, 40))
	}
	if _, err := parseValue(f[0]); err != nil {
		return "", nil, fmt.Errorf("value %q is not a number", truncate(f[0], 40))
	}
	if len(f) == 2 {
		if _, err := strconv.ParseFloat(f[1], 64); err != nil {
			return "", nil, fmt.Errorf("timestamp %q is not a number", truncate(f[1], 40))
		}
	}
	return name, labels, nil
}

func parseValue(s string) (float64, error) {
	switch s {
	case "+Inf", "Inf", "+inf", "inf":
		return math.Inf(1), nil
	case "-Inf", "-inf":
		return math.Inf(-1), nil
	case "NaN", "nan":
		return math.NaN(), nil
	}
	return strconv.ParseFloat(s, 64)
}

// parseLabels reads label pairs starting after '{' and returns the index after '}'.
func parseLabels(line string, i int, labels map[string]string, name *string) (int, error) {
	for {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			return 0, fmt.Errorf("unterminated label set")
		}
		if line[i] == '}' {
			return i + 1, nil
		}
		var key string
		if line[i] == '"' {
			v, end, err := readQuoted(line, i)
			if err != nil {
				return 0, err
			}
			key, i = v, end
		} else {
			j := i
			for j < len(line) && isLabelChar(line[j], j == i) {
				j++
			}
			if j == i {
				return 0, fmt.Errorf("bad label name at column %d", i+1)
			}
			key, i = line[i:j], j
		}
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i < len(line) && (line[i] == ',' || line[i] == '}') && *name == "" && labels[key] == "" {
			// quoted metric name inside the braces
			*name = key
		} else {
			if i >= len(line) || line[i] != '=' {
				return 0, fmt.Errorf("expected = after label %q", key)
			}
			i++
			for i < len(line) && line[i] == ' ' {
				i++
			}
			if i >= len(line) || line[i] != '"' {
				return 0, fmt.Errorf("label %q has no quoted value", key)
			}
			v, end, err := readQuoted(line, i)
			if err != nil {
				return 0, err
			}
			if _, dup := labels[key]; dup {
				return 0, fmt.Errorf("label %q appears twice", key)
			}
			labels[key] = v
			i = end
		}
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i < len(line) && line[i] == ',' {
			i++
			continue
		}
		if i < len(line) && line[i] == '}' {
			return i + 1, nil
		}
		return 0, fmt.Errorf("expected , or } in the label set")
	}
}

func isLabelChar(c byte, first bool) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (!first && c >= '0' && c <= '9')
}

// readQuoted reads an exposition-format string starting at the quote.
func readQuoted(line string, i int) (string, int, error) {
	var b strings.Builder
	for j := i + 1; j < len(line); j++ {
		c := line[j]
		switch c {
		case '\\':
			if j+1 >= len(line) {
				return "", 0, fmt.Errorf("unterminated label value")
			}
			j++
			switch line[j] {
			case 'n':
				b.WriteByte('\n')
			case '\\', '"':
				b.WriteByte(line[j])
			default:
				b.WriteByte('\\')
				b.WriteByte(line[j])
			}
		case '"':
			return b.String(), j + 1, nil
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, fmt.Errorf("unterminated label value")
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// formatOpenMetricsFloat matches labels.FormatOpenMetricsFloat in
// Prometheus 3, which rewrites le and quantile values at ingestion.
func formatOpenMetricsFloat(f float64) string {
	switch {
	case f == 1:
		return "1.0"
	case f == 0:
		return "0.0"
	case f == -1:
		return "-1.0"
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, "e.") {
		s += ".0"
	}
	return s
}

// stored returns the label value Prometheus keeps for an exposed le or
// quantile value. Prometheus 3 rewrites le only on typed histograms and
// quantile only on typed summaries (textparse.normalizeFloatsInLabelValues).
func stored(v string, promVersion int, normalize bool) string {
	if promVersion < 3 || !normalize {
		return v
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return v
	}
	return formatOpenMetricsFloat(f)
}
