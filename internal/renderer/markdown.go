package renderer

import (
	"fmt"
	"io"
	"os"

	"github.com/raphgm/container-preflight/pkg/report"
)

type Markdown struct {
	out io.Writer
}

func NewMarkdown() *Markdown {
	return &Markdown{
		out: os.Stdout,
	}
}

func (m *Markdown) Render(rep report.Report) error {
	fmt.Fprintln(m.out, "# Container Preflight Diagnostic Report")
	fmt.Fprintf(m.out, "\n**Timestamp:** %s\n", rep.Timestamp.Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintln(m.out, "\n---")

	for _, provider := range rep.Providers {
		fmt.Fprintf(m.out, "\n## Provider: %s\n\n", provider.Name)
		
		for _, res := range provider.Results {
			statusIcon := "✅"
			if res.Status == report.Fail {
				statusIcon = "❌"
			} else if res.Status == report.Warn {
				statusIcon = "⚠️"
			}

			fmt.Fprintf(m.out, "### %s %s\n\n", statusIcon, res.Name)
			fmt.Fprintf(m.out, "**Status:** %s\n\n", res.Status)
			fmt.Fprintf(m.out, "**Severity:** %s\n\n", res.Severity)
			fmt.Fprintf(m.out, "**Summary:** %s\n\n", res.Summary)
			
			if res.Details != "" {
				fmt.Fprintf(m.out, "**Details:**\n```\n%s\n```\n\n", res.Details)
			}
			
			if res.Recommendation != "" {
				fmt.Fprintf(m.out, "**Recommendation:** %s\n\n", res.Recommendation)
			}
			
			if res.Documentation != "" {
				fmt.Fprintf(m.out, "**Documentation:** [%s](%s)\n\n", res.Documentation, res.Documentation)
			}
			fmt.Fprintln(m.out, "---")
		}
	}

	return nil
}
