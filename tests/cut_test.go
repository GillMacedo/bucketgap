package tests

import (
	"reflect"
	"strings"
	"testing"
)

// TestScrapeCutEverywhere cuts the real scrape at every line and every byte.
// The text format has no end marker, so a cut exactly between two metrics
// can't be seen in the file itself. bucketgap must then either refuse, or
// report the missing metric as an error, or (when every metric the rules
// use is complete) give exactly the full scrape's result.
func TestScrapeCutEverywhere(t *testing.T) {
	full := readFile(t, scrapeFile)
	for _, rules := range []string{fixedFile, brokenFile} {
		want, wr := runJSON(t, "--scrape", scrapeFile, rules)
		for n := 1; n < len(full); n++ {
			cut := full[:n]
			p := writeFile(t, "metrics.txt", cut)
			if !strings.HasSuffix(cut, "\n") {
				// Only sample byte cuts; lines are all covered below.
				if n%7 != 0 {
					continue
				}
				r := runTool(t, "--fail-on", "never", "--scrape", p, rules)
				if r.code != 2 || r.stdout != "" {
					t.Fatalf("%s cut at byte %d: exit %d, want 2\n%s", rules, n, r.code, r.stdout)
				}
				continue
			}
			got, r := runJSON(t, "--scrape", p, rules)
			switch r.code {
			case 2:
				continue
			case 1, 0:
				if reflect.DeepEqual(keys(got), keys(want)) && got.Checked == want.Checked && r.code == wr.code {
					continue
				}
				hasMissing := false
				for _, f := range got.Findings {
					if f.Code == "not-in-scrape" && f.Severity == "ERROR" {
						hasMissing = true
					}
				}
				if !hasMissing || r.code != 1 {
					t.Fatalf("%s, scrape cut after byte %d: exit %d with a result that differs from the full scrape and reports nothing missing:\n%s", rules, n, r.code, r.stdout)
				}
			default:
				t.Fatalf("exit %d", r.code)
			}
		}
	}
}

// TestRulesCutEverywhere cuts the rule files and the dashboard at every
// byte. A cut that doesn't end a line is refused. A rule file cut exactly at
// a line boundary may still be a complete, shorter rule file; then the
// findings must be a subset of the full file's.
func TestRulesCutEverywhere(t *testing.T) {
	for _, file := range []string{brokenFile, fixedFile, dashFile} {
		full := readFile(t, file)
		want, _ := runJSON(t, "--scrape", scrapeFile, file)
		wantSet := map[string]bool{}
		for _, f := range want.Findings {
			wantSet[f.Code+" "+f.Selector+" "+f.Where] = true
		}
		name := "rules.yaml"
		if strings.HasSuffix(file, ".json") {
			name = "dash.json"
		}
		for n := 1; n < len(full); n++ {
			cut := full[:n]
			p := writeFile(t, name, cut)
			got, r := runJSON(t, "--fail-on", "never", "--scrape", scrapeFile, p)
			complete := strings.TrimSpace(cut) == strings.TrimSpace(full)
			if (!strings.HasSuffix(cut, "\n") || name == "dash.json") && !complete {
				if r.code != 2 || r.stdout != "" {
					t.Fatalf("%s cut at byte %d: exit %d, want 2\n%s", file, n, r.code, r.stdout)
				}
				continue
			}
			if r.code == 2 {
				continue
			}
			for _, f := range got.Findings {
				if !wantSet[f.Code+" "+f.Selector+" "+f.Where] {
					t.Errorf("%s cut at byte %d: finding not in the full file: %+v", file, n, f)
				}
			}
		}
	}
}

// keys identifies findings without their messages, which name the files.
func keys(r jsonReport) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Severity+" "+f.Code+" "+f.Selector+" "+f.Where)
	}
	return out
}
