// Package learn turns observed container failures into preflight rules. A
// failure's decisive log line becomes a signature; the project and host
// features at the time become the rule's conditions. Later failures with the
// same signature generalize the conditions, and successful runs that match
// them mark the rule unreliable.
package learn

import (
	"regexp"
	"strconv"
	"strings"
)

// Failure is what was extracted from a failed run's output.
type Failure struct {
	Line     string
	Service  string
	ExitCode int
}

// composePrefix matches Compose's log prefix, e.g. "db-1  | ".
var composePrefix = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9_.-]*?)(?:-\d+)?\s+\|\s?(.*)$`)

// exitedLine matches Compose's container exit report.
var exitedLine = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9_.-]*?)(?:-\d+)? exited with code (\d+)`)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Keywords that make a line decisive, with weights. Generic wrappers such as
// "exited with code" or "Error response from daemon" score low so the
// specific cause wins.
var cues = []struct {
	re     *regexp.Regexp
	weight int
}{
	{regexp.MustCompile(`(?i)\b(fatal|panic|segmentation fault|illegal instruction|core dumped)\b`), 5},
	{regexp.MustCompile(`(?i)\b(requires?|required|not supported|unsupported|incompatible|too low|at least)\b`), 4},
	{regexp.MustCompile(`(?i)\b(error|failed|failure|invalid|cannot|can't|unable|denied|refused|not found|no such|no space|oom|killed)\b`), 3},
	{regexp.MustCompile(`(?i)\bwarn(ing)?\b`), 1},
}

var generic = regexp.MustCompile(`(?i)(exited with code|error response from daemon:?\s*$|^error:?\s*$|attaching to|gracefully stopping|aborting on container exit)`)

// Extract picks the line that best explains why a run failed.
func Extract(output string) (Failure, bool) {
	var best Failure
	bestScore := 0
	exitCodes := map[string]int{}
	for _, raw := range strings.Split(ansi.ReplaceAllString(output, ""), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := exitedLine.FindStringSubmatch(line); m != nil {
			if code, _ := strconv.Atoi(m[2]); code != 0 {
				exitCodes[m[1]] = code
			}
			continue
		}
		service := ""
		if m := composePrefix.FindStringSubmatch(line); m != nil {
			service, line = m[1], strings.TrimSpace(m[2])
		}
		score := 0
		for _, c := range cues {
			if c.re.MatchString(line) {
				score += c.weight
			}
		}
		if generic.MatchString(line) {
			score -= 3
		}
		// Later lines win ties: the cause is usually printed last.
		if score > 0 && score >= bestScore {
			best, bestScore = Failure{Line: line, Service: service}, score
		}
	}
	if bestScore == 0 {
		return Failure{}, false
	}
	if best.Service == "" && len(exitCodes) == 1 {
		for s := range exitCodes {
			best.Service = s
		}
	}
	best.ExitCode = exitCodes[best.Service]
	if len(best.Line) > 300 {
		best.Line = best.Line[:300]
	}
	return best, true
}

var (
	hexAddr  = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	hexID    = regexp.MustCompile(`\b[0-9a-f]{12,64}\b`)
	absPath  = regexp.MustCompile(`(?:/[^\s"':,]+)+`)
	number   = regexp.MustCompile(`\d+`)
	stampish = regexp.MustCompile(`^\S*\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}\S*\s*`)
)

// Signature turns a log line into a regular expression that matches the same
// failure on another machine: addresses, IDs, paths and numbers vary.
func Signature(line string) string {
	line = stampish.ReplaceAllString(line, "")
	type span struct {
		start, end int
		repl       string
	}
	var spans []span
	taken := make([]bool, len(line))
	mark := func(re *regexp.Regexp, repl string) {
		for _, loc := range re.FindAllStringIndex(line, -1) {
			free := true
			for i := loc[0]; i < loc[1]; i++ {
				if taken[i] {
					free = false
					break
				}
			}
			if !free {
				continue
			}
			for i := loc[0]; i < loc[1]; i++ {
				taken[i] = true
			}
			spans = append(spans, span{loc[0], loc[1], repl})
		}
	}
	mark(hexAddr, `0x[0-9a-fA-F]+`)
	mark(hexID, `[0-9a-f]+`)
	mark(absPath, `\S+`)
	mark(number, `\d+`)

	var b strings.Builder
	for i := 0; i < len(line); {
		matched := false
		for _, sp := range spans {
			if sp.start == i {
				b.WriteString(sp.repl)
				i, matched = sp.end, true
				break
			}
		}
		if !matched {
			b.WriteString(regexp.QuoteMeta(line[i : i+1]))
			i++
		}
	}
	return b.String()
}
