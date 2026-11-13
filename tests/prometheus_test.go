package tests

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAgainstPrometheus runs real Prometheus servers and checks that
// bucketgap flags exactly the selectors that match no series. It needs
// Prometheus binaries, so it runs only when BUCKETGAP_PROMETHEUS lists
// their paths, separated by the OS path list separator:
//
//	BUCKETGAP_PROMETHEUS=/opt/prom3/prometheus:/opt/prom2/prometheus go test ./tests -run Prometheus
func TestAgainstPrometheus(t *testing.T) {
	env := os.Getenv("BUCKETGAP_PROMETHEUS")
	if env == "" {
		t.Skip("set BUCKETGAP_PROMETHEUS to Prometheus binaries to run this test")
	}
	for _, binPath := range filepath.SplitList(env) {
		out, err := exec.Command(binPath, "--version").CombinedOutput()
		if err != nil {
			t.Fatalf("%s --version: %v", binPath, err)
		}
		m := regexp.MustCompile(`version (\d+)\.`).FindSubmatch(out)
		if m == nil {
			t.Fatalf("can't read the version of %s:\n%s", binPath, out)
		}
		major, _ := strconv.Atoi(string(m[1]))
		t.Run("prometheus"+string(m[1]), func(t *testing.T) { checkPrometheus(t, binPath, major) })
	}
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func checkPrometheus(t *testing.T, promBin string, major int) {
	co := startCheckout(t)
	for i := 0; i < 3; i++ {
		get(t, "http://"+co+"/checkout")
	}
	odd := readFile(t, "tests/testdata/odd.txt")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write([]byte(odd))
	}))
	defer srv.Close()
	oddAddr := strings.TrimPrefix(srv.URL, "http://")

	dir := t.TempDir()
	cfg := fmt.Sprintf("global:\n  scrape_interval: 1s\nscrape_configs:\n  - job_name: checkout\n    static_configs: [{targets: [%q]}]\n  - job_name: odd\n    static_configs: [{targets: [%q]}]\n", co, oddAddr)
	os.WriteFile(filepath.Join(dir, "prometheus.yml"), []byte(cfg), 0o644)
	web := freeAddr(t)
	cmd := exec.Command(promBin, "--config.file="+filepath.Join(dir, "prometheus.yml"),
		"--storage.tsdb.path="+filepath.Join(dir, "data"), "--web.listen-address="+web)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	query := func(q string) (int, error) {
		resp, err := http.Get("http://" + web + "/api/v1/query?query=" + url.QueryEscape(q))
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		var r struct {
			Status string
			Error  string
			Data   struct{ Result []json.RawMessage }
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return 0, err
		}
		if r.Status != "success" {
			return 0, fmt.Errorf("%s: %s", q, r.Error)
		}
		return len(r.Data.Result), nil
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		n, err := query(`up == 1`)
		if err == nil && n == 2 {
			if m, _ := query(`odd_seconds_count`); m == 1 {
				if m, _ := query(`checkout_request_duration_seconds_count`); m == 1 {
					break
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Prometheus didn't scrape both targets: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// The scrapes bucketgap reads: the live service and the fixture.
	live := writeFile(t, "live.txt", get(t, "http://"+co+"/metrics"))
	oddFile := filepath.Join("tests", "testdata", "odd.txt")

	// One record rule per selector, built from every exposed value and
	// the spellings people write by mistake.
	type sel struct{ name, label, op, value string }
	var sels []sel
	valRe := regexp.MustCompile(`^([a-z_]+)\{(?:[^}]*,)?(le|quantile)="([^"]+)"\}`)
	for _, f := range []string{readFile(t, scrapeFile), odd} {
		for _, line := range strings.Split(f, "\n") {
			m := valRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			name, label, v := m[1], m[2], m[3]
			cands := []string{v, v + ".0", strings.TrimSuffix(v, ".0"), strings.TrimSuffix(v, "0"), v + "1"}
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cands = append(cands, strconv.FormatFloat(f, 'g', -1, 64), strconv.FormatFloat(f, 'f', -1, 64))
			}
			for _, c := range cands {
				if c == "" {
					continue
				}
				sels = append(sels, sel{name, label, "=", c})
			}
			sels = append(sels, sel{name, label, "=~", regexp.QuoteMeta(v) + "|0.333"})
			sels = append(sels, sel{name, label, "=~", regexp.QuoteMeta(strings.TrimSuffix(v, ".0")) + `(\.0)?`})
		}
	}
	seen := map[string]bool{}
	var rules strings.Builder
	rules.WriteString("groups:\n  - name: oracle\n    rules:\n")
	var texts []string
	for _, s := range sels {
		text := fmt.Sprintf("%s{%s%s%s}", s.name, s.label, s.op, strconv.Quote(s.value))
		if seen[text] {
			continue
		}
		seen[text] = true
		texts = append(texts, text)
		fmt.Fprintf(&rules, "      - record: r%d\n        expr: %s\n", len(texts), strconv.Quote(text))
	}
	rulesFile := writeFile(t, "oracle.yaml", rules.String())
	rep, r := runJSON(t, "--prometheus", strconv.Itoa(major), "--fail-on", "never", "--scrape", live, "--scrape", oddFile, rulesFile)
	if r.code != 0 {
		t.Fatalf("bucketgap exit %d\n%s", r.code, r.stderr)
	}
	flagged := map[string]bool{}
	for _, f := range rep.Findings {
		if f.Severity == "ERROR" {
			flagged[f.Selector] = true
		}
	}
	sort.Strings(texts)
	empty, agree := 0, 0
	for _, text := range texts {
		n, err := query("count(" + text + ")")
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			empty++
		}
		if (n == 0) != flagged[text] {
			t.Errorf("Prometheus %d: %s returns %d series, bucketgap flagged it: %v", major, text, n, flagged[text])
			continue
		}
		agree++
	}
	t.Logf("Prometheus %d: %d selectors, %d match nothing, bucketgap agrees on %d", major, len(texts), empty, agree)
}
