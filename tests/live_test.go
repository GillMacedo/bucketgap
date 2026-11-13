package tests

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startCheckout runs the example service built with the real
// prometheus/client_golang and returns its address.
func startCheckout(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	cmd := exec.Command(checkout, "--listen", addr)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the example service didn't start")
	return ""
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d %v", url, resp.StatusCode, err)
	}
	return string(b)
}

// TestLiveScrape checks the rules against a fresh scrape of the running
// service, not the saved file.
func TestLiveScrape(t *testing.T) {
	addr := startCheckout(t)
	for i := 0; i < 5; i++ {
		get(t, fmt.Sprintf("http://%s/checkout", addr))
	}
	p := writeFile(t, "live.txt", get(t, fmt.Sprintf("http://%s/metrics", addr)))

	r := runTool(t, "--scrape", p, fixedFile)
	if r.code != 0 || !strings.Contains(r.stdout, "No problems in 5 checked selectors.") {
		t.Errorf("fixed rules on a live scrape: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	want, _ := runJSON(t, "--scrape", scrapeFile, brokenFile)
	got, r := runJSON(t, "--scrape", p, brokenFile)
	if r.code != 1 || strings.Join(keys(got), "\n") != strings.Join(keys(want), "\n") {
		t.Errorf("broken rules on a live scrape differ from the saved scrape:\n%s\nwant:\n%s", strings.Join(keys(got), "\n"), strings.Join(keys(want), "\n"))
	}
}

// Before its first request, client_golang leaves the histogram vector out of
// /metrics entirely. That scrape must not pass the rules.
func TestLiveScrapeBeforeTraffic(t *testing.T) {
	addr := startCheckout(t)
	body := get(t, fmt.Sprintf("http://%s/metrics", addr))
	if strings.Contains(body, "checkout_request_duration_seconds_bucket") {
		t.Fatal("expected no histogram series before the first request")
	}
	p := writeFile(t, "cold.txt", body)
	for _, rules := range []string{fixedFile, brokenFile} {
		got, r := runJSON(t, "--fail-on", "error", "--scrape", p, rules)
		if r.code == 0 {
			t.Errorf("%s on a scrape without the histogram: exit 0\n%s", rules, r.stdout)
		}
		if r.code == 1 {
			found := false
			for _, f := range got.Findings {
				if f.Code == "not-in-scrape" && strings.HasPrefix(f.Selector, "checkout_request_duration_seconds_bucket") {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: the missing histogram isn't reported: %v", rules, keys(got))
			}
		}
	}
}
