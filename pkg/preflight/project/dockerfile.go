package project

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

// Dockerfile is the requirement view of a parsed Dockerfile.
type Dockerfile struct {
	Path   string
	Syntax string
	Stages []Stage

	// BuildKitFeatures lists syntax the legacy builder rejects.
	BuildKitFeatures []Feature
}

type Stage struct {
	Name string

	// Base is the FROM reference after ARG expansion.
	Base string

	// BaseStage is the index of the earlier stage Base refers to, or -1 when
	// Base is an external image.
	BaseStage int

	// Platform is the FROM --platform value after ARG expansion. It may
	// still contain $BUILDPLATFORM or $TARGETPLATFORM.
	Platform string

	Location Location

	// Copies are COPY/ADD instructions that read from the build context.
	Copies []Copy

	// Deps are indices of other stages this stage reads from.
	Deps []int
}

type Copy struct {
	Instruction string
	Sources     []string
	Location    Location
}

// Unresolved reports whether Base still depends on an ARG with no value.
func (s Stage) Unresolved() bool {
	return strings.Contains(s.Base, "$")
}

// ParseDockerfile parses the file at path. args override ARG defaults, as
// `--build-arg` would.
func ParseDockerfile(path string, args map[string]string) (*Dockerfile, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseDockerfile(path, content, args)
}

func parseDockerfile(path string, content []byte, args map[string]string) (*Dockerfile, error) {
	res, err := parser.Parse(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	stages, metaArgs, err := instructions.Parse(res.AST, nil)
	if err != nil {
		return nil, err
	}

	df := &Dockerfile{Path: path}
	df.Syntax, _, _, _ = parser.DetectSyntax(content)

	vars := map[string]string{}
	for _, a := range metaArgs {
		for _, kv := range a.Args {
			if kv.Value != nil {
				vars[kv.Key] = *kv.Value
			}
		}
	}
	for k, v := range args {
		vars[k] = v
	}

	for _, node := range res.AST.Children {
		if len(node.Heredocs) > 0 {
			df.BuildKitFeatures = append(df.BuildKitFeatures, Feature{
				Name:     "heredoc (<<EOF)",
				Location: Location{File: path, Line: node.StartLine},
			})
		}
	}

	byName := map[string]int{}
	for i, st := range stages {
		s := Stage{
			Name:      st.Name,
			Base:      expand(st.BaseName, vars),
			BaseStage: -1,
			Platform:  expandKeepUnknown(st.Platform, vars),
			Location:  Location{File: path, Line: firstLine(st.Location)},
		}
		if idx, ok := byName[strings.ToLower(s.Base)]; ok {
			s.BaseStage = idx
			s.Deps = append(s.Deps, idx)
		}

		for _, cmd := range st.Commands {
			loc := Location{File: path, Line: firstLine(cmd.Location())}
			switch c := cmd.(type) {
			case *instructions.CopyCommand:
				if c.From != "" {
					if dep, ok := stageRef(c.From, byName, i); ok {
						s.Deps = append(s.Deps, dep)
					}
				} else {
					s.Copies = append(s.Copies, Copy{Instruction: "COPY", Sources: c.SourcePaths, Location: loc})
				}
				df.addCopyFeatures(c.Chmod, c.Link, c.Parents, c.ExcludePatterns, "COPY", loc)
			case *instructions.AddCommand:
				s.Copies = append(s.Copies, Copy{Instruction: "ADD", Sources: c.SourcePaths, Location: loc})
				df.addCopyFeatures(c.Chmod, c.Link, false, c.ExcludePatterns, "ADD", loc)
			case *instructions.RunCommand:
				for _, flag := range c.FlagsUsed {
					df.BuildKitFeatures = append(df.BuildKitFeatures, Feature{
						Name:     "RUN --" + flag,
						Location: loc,
					})
				}
				for _, m := range instructions.GetMounts(c) {
					if m.From != "" {
						if dep, ok := stageRef(m.From, byName, i); ok {
							s.Deps = append(s.Deps, dep)
						}
					}
				}
			}
		}

		if s.Name != "" {
			byName[strings.ToLower(s.Name)] = i
		}
		df.Stages = append(df.Stages, s)
	}
	return df, nil
}

func (df *Dockerfile) addCopyFeatures(chmod string, link, parents bool, exclude []string, instr string, loc Location) {
	add := func(flag string) {
		df.BuildKitFeatures = append(df.BuildKitFeatures, Feature{Name: instr + " --" + flag, Location: loc})
	}
	if chmod != "" {
		add("chmod")
	}
	if link {
		add("link")
	}
	if parents {
		add("parents")
	}
	if len(exclude) > 0 {
		add("exclude")
	}
}

// Reachable returns the indices of stages BuildKit actually builds for
// target (the last stage when target is empty). Unreachable stages are
// skipped by BuildKit, so their problems do not break the build.
func (df *Dockerfile) Reachable(target string) ([]int, error) {
	if len(df.Stages) == 0 {
		return nil, nil
	}
	start := len(df.Stages) - 1
	if target != "" {
		start = -1
		for i, s := range df.Stages {
			if strings.EqualFold(s.Name, target) {
				start = i
			}
		}
		if start < 0 {
			return nil, fmt.Errorf("target stage %q not found", target)
		}
	}

	seen := map[int]bool{}
	var visit func(int)
	visit = func(i int) {
		if seen[i] {
			return
		}
		seen[i] = true
		for _, d := range df.Stages[i].Deps {
			visit(d)
		}
	}
	visit(start)

	var out []int
	for i := range df.Stages {
		if seen[i] {
			out = append(out, i)
		}
	}
	return out, nil
}

func stageRef(ref string, byName map[string]int, current int) (int, bool) {
	if idx, ok := byName[strings.ToLower(ref)]; ok {
		return idx, true
	}
	if n, err := strconv.Atoi(ref); err == nil && n >= 0 && n < current {
		return n, true
	}
	return 0, false
}

func firstLine(r []parser.Range) int {
	if len(r) == 0 {
		return 0
	}
	return r[0].Start.Line
}

var varRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::?([-+])([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// expand substitutes ARG values the way the Dockerfile frontend does for
// FROM lines. Variables with no value are left in place so callers can tell
// the reference is unresolved.
func expand(s string, vars map[string]string) string {
	return varRef.ReplaceAllStringFunc(s, func(m string) string {
		sub := varRef.FindStringSubmatch(m)
		name, op, word := sub[1], sub[2], sub[3]
		if name == "" {
			name = sub[4]
		}
		v, ok := vars[name]
		switch op {
		case "-":
			if !ok || v == "" {
				return word
			}
		case "+":
			if ok && v != "" {
				return word
			}
			return ""
		}
		if !ok {
			return m
		}
		return v
	})
}

// expandKeepUnknown is expand, but it never touches the automatic platform
// ARGs, which only the builder can fill in.
func expandKeepUnknown(s string, vars map[string]string) string {
	filtered := map[string]string{}
	for k, v := range vars {
		if !strings.HasSuffix(k, "PLATFORM") {
			filtered[k] = v
		}
	}
	return expand(s, filtered)
}
