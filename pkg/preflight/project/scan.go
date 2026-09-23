package project

import (
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeMinVersions maps Compose-file syntax to the first Docker Compose v2
// release that accepts it (from the docker/compose release notes). Older
// versions reject the file or silently ignore the key.
var composeMinVersions = struct {
	topLevel  map[string]string
	service   map[string]string
	build     map[string]string
	dependsOn map[string]string
}{
	topLevel: map[string]string{
		"include": "2.20.0",
		"models":  "2.38.0",
	},
	service: map[string]string{
		"develop":    "2.22.0",
		"gpus":       "2.30.0",
		"post_start": "2.30.0",
		"pre_stop":   "2.30.0",
	},
	build: map[string]string{
		"additional_contexts": "2.17.0",
	},
	dependsOn: map[string]string{
		"restart":  "2.17.0",
		"required": "2.20.0",
	},
}

// scanComposeFeatures walks the raw YAML, keeping line numbers that the typed
// loader discards.
func scanComposeFeatures(file string, raw []byte) []Feature {
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	var out []Feature
	add := func(name, version string, n *yaml.Node) {
		out = append(out, Feature{Name: name, MinVersion: version, Location: Location{File: file, Line: n.Line}})
	}

	for key, val := range mappingPairs(root) {
		if v, ok := composeMinVersions.topLevel[key.Value]; ok {
			add(key.Value, v, key)
		}
		if key.Value != "services" {
			continue
		}
		for svcKey, svc := range mappingPairs(val) {
			for k, v := range mappingPairs(svc) {
				if ver, ok := composeMinVersions.service[k.Value]; ok {
					add("services."+svcKey.Value+"."+k.Value, ver, k)
				}
				switch k.Value {
				case "build":
					for bk := range mappingPairs(v) {
						if ver, ok := composeMinVersions.build[bk.Value]; ok {
							add("services."+svcKey.Value+".build."+bk.Value, ver, bk)
						}
					}
				case "depends_on":
					for _, dep := range mappingPairs(v) {
						for dk := range mappingPairs(dep) {
							if ver, ok := composeMinVersions.dependsOn[dk.Value]; ok {
								add("services."+svcKey.Value+".depends_on."+dk.Value, ver, dk)
							}
						}
					}
				}
			}
		}
	}
	return out
}

// mappingPairs iterates key/value nodes of a YAML mapping in file order.
func mappingPairs(n *yaml.Node) func(func(*yaml.Node, *yaml.Node) bool) {
	return func(yield func(*yaml.Node, *yaml.Node) bool) {
		if n == nil || n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !yield(n.Content[i], n.Content[i+1]) {
				return
			}
		}
	}
}

// serviceLines maps each service name to the line defining it.
func serviceLines(raw []byte) map[string]int {
	out := map[string]int{}
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) == 0 {
		return out
	}
	for key, val := range mappingPairs(doc.Content[0]) {
		if key.Value == "services" {
			for svc := range mappingPairs(val) {
				out[svc.Value] = svc.Line
			}
		}
	}
	return out
}

var interpolation = regexp.MustCompile(`\$\$|\$\{([A-Za-z_][A-Za-z0-9_]*)(:?[-?+])?[^}]*\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// scanEnvRefs finds every ${VAR} Compose will interpolate.
func scanEnvRefs(file string, raw []byte) []EnvRef {
	var out []EnvRef
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range interpolation.FindAllStringSubmatch(line, -1) {
			if m[0] == "$$" {
				continue
			}
			name, op := m[1], m[2]
			if name == "" {
				name = m[3]
			}
			out = append(out, EnvRef{
				Name:       name,
				HasDefault: strings.HasSuffix(op, "-") || strings.HasSuffix(op, "+"),
				Required:   strings.HasSuffix(op, "?"),
				Location:   Location{File: file, Line: i + 1},
			})
		}
	}
	return out
}
