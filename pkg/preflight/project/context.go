package project

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// BuildContext answers whether COPY/ADD sources will be visible to the
// builder, applying .dockerignore the way BuildKit does.
type BuildContext struct {
	Dir        string
	IgnoreFile string
	patterns   []string
}

// OpenContext loads the ignore rules for a build. A Dockerfile-specific
// ignore file (<Dockerfile>.dockerignore) takes precedence over the
// context's .dockerignore, as in BuildKit.
func OpenContext(dir, dockerfile string) (*BuildContext, error) {
	bc := &BuildContext{Dir: dir}
	candidates := []string{filepath.Join(dir, ".dockerignore")}
	if dockerfile != "" {
		candidates = append([]string{dockerfile + ".dockerignore"}, candidates...)
	}
	for _, c := range candidates {
		f, err := os.Open(c)
		if err != nil {
			continue
		}
		patterns, err := ignorefile.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		bc.IgnoreFile, bc.patterns = c, patterns
		break
	}
	return bc, nil
}

type SourceStatus int

const (
	SourceOK SourceStatus = iota
	SourceSkipped
	SourceMissing
	SourceIgnored
	SourceOutside
)

// Check reports whether src resolves to at least one file the builder will
// receive. For SourceIgnored it also returns the pattern responsible.
func (bc *BuildContext) Check(src string) (SourceStatus, string) {
	if strings.Contains(src, "$") || strings.Contains(src, "://") || strings.HasPrefix(src, "git@") {
		return SourceSkipped, ""
	}
	rel := filepath.Clean(strings.TrimPrefix(src, "/"))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return SourceOutside, ""
	}

	matches, err := filepath.Glob(filepath.Join(bc.Dir, rel))
	if err != nil || len(matches) == 0 {
		return SourceMissing, ""
	}
	var blocker string
	for _, m := range matches {
		r, _ := filepath.Rel(bc.Dir, m)
		pattern := bc.excludedBy(filepath.ToSlash(r))
		if pattern == "" {
			return SourceOK, ""
		}
		blocker = pattern
	}
	return SourceIgnored, blocker
}

// excludedBy returns the ignore pattern that excludes rel, or "" when rel is
// sent to the builder. The last matching pattern wins, and a `!` pattern
// re-includes.
func (bc *BuildContext) excludedBy(rel string) string {
	if rel == "." {
		return ""
	}
	var last string
	excluded := false
	for _, p := range bc.patterns {
		neg := strings.HasPrefix(p, "!")
		body := strings.TrimPrefix(p, "!")
		ok, err := patternmatcher.MatchesOrParentMatches(rel, []string{body})
		if err != nil || !ok {
			continue
		}
		excluded, last = !neg, p
	}
	if !excluded {
		return ""
	}
	return last
}
