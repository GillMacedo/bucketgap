// Package tests runs bucketgap as a binary, the way the skill does, on the
// example service's real /metrics output and on broken inputs.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var (
	bin      string // bucketgap
	checkout string // the example service
	tmpRoot  string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bucketgap-tests")
	if err != nil {
		panic(err)
	}
	tmpRoot = dir
	bin = filepath.Join(dir, "bucketgap")
	checkout = filepath.Join(dir, "checkout")
	builds := [][3]string{
		{bin, "..", "./plugin/scripts"},
		{checkout, "../examples/checkout", "."},
	}
	for _, b := range builds {
		cmd := exec.Command("go", "build", "-mod=vendor", "-o", b[0], b[2])
		if b[1] == ".." {
			cmd = exec.Command("go", "build", "-o", b[0], b[2])
		}
		cmd.Dir = b[1]
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "building %s: %v\n%s", b[2], err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	code           int
	stdout, stderr string
}

// runTool runs bucketgap from the repository root.
func runTool(t *testing.T, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = ".."
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{code, out.String(), errb.String()}
}

type jsonFinding struct {
	Severity, Code, File, Where, Selector, Message, Fix string
	Line                                                int
}

type jsonReport struct {
	Prometheus  int
	Checked     int
	NotChecked  map[string]int
	NotInScrape map[string]int
	Findings    []jsonFinding
}

func runJSON(t *testing.T, args ...string) (jsonReport, result) {
	t.Helper()
	r := runTool(t, append([]string{"--format", "json"}, args...)...)
	var rep jsonReport
	if r.code == 0 || r.code == 1 {
		if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
			t.Fatalf("bad JSON (%v):\n%s", err, r.stdout)
		}
	}
	return rep, r
}

// writeFile writes data to a new file in a test's temporary directory.
func writeFile(t *testing.T, name, data string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const (
	scrapeFile = "examples/checkout/metrics.txt"
	brokenFile = "examples/checkout/rules/broken.yaml"
	fixedFile  = "examples/checkout/rules/fixed.yaml"
	dashFile   = "examples/checkout/rules/dashboard.json"
)
