package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// expr is one PromQL expression and where it came from.
type expr struct {
	File  string `json:"file"`
	Where string `json:"where"` // "group G, alert A" or "panel P, query A"
	Line  int    `json:"line,omitempty"`
	Text  string `json:"-"`
}

type inputs struct {
	exprs      []expr
	ruleFiles  int
	dashboards int
	rules      int
	skipped    []string // files in a directory that hold neither rules nor dashboards
	// otherDS counts dashboard queries for data sources that aren't
	// Prometheus, such as Loki, which aren't PromQL.
	otherDS int
}

// dsType returns the type of a Grafana datasource reference, or "" if it
// doesn't say (a name, a variable or nothing).
func dsType(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	t, _ := m["type"].(string)
	if strings.HasPrefix(t, "$") {
		return ""
	}
	return t
}

// promCompatible lists data source types whose queries are PromQL.
var promCompatible = map[string]bool{"": true, "prometheus": true, "datasource": true}

func loadInputs(paths []string) (*inputs, error) {
	in := &inputs{}
	for _, p := range paths {
		st, err := os.Lstat(p)
		if err != nil {
			return nil, untrusted("%v", err)
		}
		if st.Mode()&fs.ModeSymlink != 0 {
			if st, err = os.Stat(p); err != nil {
				return nil, untrusted("%v", err)
			}
		}
		if !st.IsDir() {
			if err := in.loadFile(p, true); err != nil {
				return nil, err
			}
			continue
		}
		var files []string
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return untrusted("%s is a symbolic link, which bucketgap doesn't follow in a directory; pass its target instead", path)
			}
			if d.IsDir() {
				if path != p && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			switch strings.ToLower(filepath.Ext(d.Name())) {
			case ".yaml", ".yml", ".json":
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			var ie *inputError
			if errors.As(err, &ie) {
				return nil, err
			}
			return nil, untrusted("%v", err)
		}
		if len(files) == 0 {
			return nil, untrusted("%s: no .yaml, .yml or .json files", p)
		}
		sort.Strings(files)
		for _, f := range files {
			if err := in.loadFile(f, false); err != nil {
				return nil, err
			}
		}
	}
	if len(in.exprs) == 0 {
		return nil, untrusted("no rules or dashboard queries found in the input")
	}
	return in, nil
}

// loadFile reads one file. explicit is true when the user named the file,
// so a file that holds no rules is an error instead of being skipped.
func (in *inputs) loadFile(path string, explicit bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return untrusted("%v", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return untrusted("%s: the file is empty", path)
	}
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if strings.ToLower(filepath.Ext(path)) == ".json" || (len(trimmed) > 0 && trimmed[0] == '{') {
		n, err := in.loadDashboard(path, data)
		if err != nil {
			return err
		}
		if n == 0 {
			if explicit {
				return untrusted("%s: no Prometheus queries (targets[].expr) in this JSON file", path)
			}
			in.skipped = append(in.skipped, path)
			return nil
		}
		in.dashboards++
		return nil
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		return untrusted("%s: the file doesn't end with a newline, so it may be cut off", path)
	}
	n, err := in.loadRuleYAML(path, data)
	if err != nil {
		return err
	}
	if n == 0 {
		if explicit {
			return untrusted("%s: no Prometheus rules in this file (expected groups: with rules, or a PrometheusRule)", path)
		}
		in.skipped = append(in.skipped, path)
		return nil
	}
	in.ruleFiles++
	return nil
}

type ruleFile struct {
	Groups []ruleGroup `yaml:"groups"`
}

type ruleGroup struct {
	Name  string           `yaml:"name"`
	Rules []map[string]any `yaml:"rules"`
}

// loadRuleYAML reads Prometheus rule files and PrometheusRule objects,
// including multi-document files. It returns the number of rules read.
func (in *inputs) loadRuleYAML(path string, data []byte) (int, error) {
	dec := NewDecoder(bytes.NewReader(data))
	count := 0
	sawDoc := false
	for doc := 0; ; doc++ {
		var raw any
		err := dec.Decode(&raw)
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, untrusted("%s: not valid YAML: %v", path, err)
		}
		if raw == nil {
			continue
		}
		sawDoc = true
		m, ok := raw.(map[any]any)
		if !ok {
			return 0, untrusted("%s: document %d is a %T, not a YAML mapping; this isn't a rule file", path, doc+1, raw)
		}
		var groups any
		if g, ok := m["groups"]; ok {
			groups = g
		} else if kind, _ := m["kind"].(string); kind == "PrometheusRule" {
			spec, _ := m["spec"].(map[any]any)
			if spec == nil || spec["groups"] == nil {
				return 0, untrusted("%s: PrometheusRule %v has no spec.groups", path, nameOf(m))
			}
			groups = spec["groups"]
		} else {
			continue // another kind of object in a rendered chart
		}
		gl, ok := groups.([]any)
		if !ok || len(gl) == 0 {
			return 0, untrusted("%s: groups is empty or not a list", path)
		}
		for gi, g := range gl {
			gm, ok := g.(map[any]any)
			if !ok {
				return 0, untrusted("%s: group %d is not a mapping", path, gi+1)
			}
			gname, _ := gm["name"].(string)
			if gname == "" {
				return 0, untrusted("%s: group %d has no name", path, gi+1)
			}
			rl, ok := gm["rules"].([]any)
			if !ok {
				if gm["rules"] == nil {
					continue // a group with no rules is valid
				}
				return 0, untrusted("%s: group %s: rules is not a list", path, gname)
			}
			for ri, r := range rl {
				rm, ok := r.(map[any]any)
				if !ok {
					return 0, untrusted("%s: group %s: rule %d is not a mapping", path, gname, ri+1)
				}
				alert, _ := rm["alert"].(string)
				record, _ := rm["record"].(string)
				if (alert == "") == (record == "") {
					return 0, untrusted("%s: group %s: rule %d needs exactly one of alert or record", path, gname, ri+1)
				}
				e, ok := rm["expr"].(string)
				if !ok || strings.TrimSpace(e) == "" {
					return 0, untrusted("%s: group %s: rule %s%s has no expr string", path, gname, alert, record)
				}
				where := "group " + gname + ", "
				key := ""
				if alert != "" {
					where += "alert " + alert
					key = "alert: " + alert
				} else {
					where += "record " + record
					key = "record: " + record
				}
				in.exprs = append(in.exprs, expr{File: path, Where: where, Line: lineOf(data, key), Text: e})
				count++
			}
		}
	}
	if !sawDoc {
		return 0, untrusted("%s: the file holds no YAML documents", path)
	}
	return count, nil
}

func nameOf(m map[any]any) any {
	if md, ok := m["metadata"].(map[any]any); ok {
		return md["name"]
	}
	return "(no name)"
}

// lineOf returns the first line holding key, or 0.
func lineOf(data []byte, key string) int {
	for i, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "- "))
		t = strings.ReplaceAll(t, `"`, "")
		t = strings.ReplaceAll(t, `'`, "")
		if t == key {
			return i + 1
		}
	}
	return 0
}

// loadDashboard reads the PromQL queries of a Grafana dashboard: every
// targets[].expr string, in panels, rows and library panels at any depth.
func (in *inputs) loadDashboard(path string, data []byte) (int, error) {
	var root any
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&root); err != nil {
		return 0, untrusted("%s: not valid JSON: %v", path, err)
	}
	if dec.More() {
		return 0, untrusted("%s: more than one JSON value in the file", path)
	}
	n := 0
	var walk func(v any, panel, ds string) error
	walk = func(v any, panel, ds string) error {
		switch x := v.(type) {
		case map[string]any:
			if t, ok := x["title"].(string); ok {
				if _, isPanel := x["type"]; isPanel {
					panel = t
					ds = dsType(x["datasource"])
				}
			}
			if ts, ok := x["targets"].([]any); ok {
				for i, t := range ts {
					tm, ok := t.(map[string]any)
					if !ok {
						continue
					}
					e, has := tm["expr"]
					if !has {
						continue
					}
					tds := dsType(tm["datasource"])
					if tds == "" {
						tds = ds
					}
					if !promCompatible[tds] {
						in.otherDS++
						continue
					}
					es, ok := e.(string)
					if !ok {
						return untrusted("%s: panel %q target %d: expr is not a string", path, panel, i+1)
					}
					if strings.TrimSpace(es) == "" {
						continue // an unused query row in the editor
					}
					ref, _ := tm["refId"].(string)
					if ref == "" {
						ref = fmt.Sprint(i + 1)
					}
					in.exprs = append(in.exprs, expr{File: path, Where: fmt.Sprintf("panel %q, query %s", panel, ref), Text: es})
					n++
				}
			}
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if k == "targets" {
					continue
				}
				if err := walk(x[k], panel, ds); err != nil {
					return err
				}
			}
		case []any:
			for _, e := range x {
				if err := walk(e, panel, ds); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, "", ""); err != nil {
		return 0, err
	}
	return n, nil
}
