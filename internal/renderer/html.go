package renderer

import (
	"fmt"
	"io"
	"os"

	"github.com/raphgm/container-preflight/pkg/report"
)

type HTML struct {
	out io.Writer
}

func NewHTML() *HTML {
	return &HTML{
		out: os.Stdout,
	}
}

func (h *HTML) Render(rep report.Report) error {
	fmt.Fprintln(h.out, `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Container Preflight Report</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; line-height: 1.6; color: #333; max-width: 800px; margin: 0 auto; padding: 20px; background-color: #f9f9f9; }
        h1 { color: #2c3e50; border-bottom: 2px solid #3498db; padding-bottom: 10px; }
        h2 { color: #2980b9; margin-top: 30px; }
        .timestamp { color: #7f8c8d; font-style: italic; margin-bottom: 30px; }
        .card { background: #fff; border-radius: 8px; box-shadow: 0 4px 6px rgba(0,0,0,0.1); padding: 20px; margin-bottom: 20px; border-left: 5px solid #bdc3c7; }
        .card.pass { border-left-color: #2ecc71; }
        .card.warn { border-left-color: #f1c40f; }
        .card.fail { border-left-color: #e74c3c; }
        .card-header { display: flex; align-items: center; font-size: 1.2em; font-weight: bold; margin-bottom: 10px; }
        .card-header .icon { margin-right: 10px; }
        .label { font-weight: bold; color: #555; }
        pre { background: #f4f4f4; padding: 10px; border-radius: 4px; overflow-x: auto; font-family: "Courier New", Courier, monospace; font-size: 0.9em; }
        a { color: #3498db; text-decoration: none; }
        a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <h1>Container Preflight Diagnostic Report</h1>
    <div class="timestamp">Generated at: ` + rep.Timestamp.Format("2006-01-02 15:04:05 UTC") + `</div>`)

	for _, provider := range rep.Providers {
		fmt.Fprintf(h.out, "    <h2>Provider: %s</h2>\n", provider.Name)
		
		for _, res := range provider.Results {
			statusClass := "pass"
			icon := "✅"
			if res.Status == report.Fail {
				statusClass = "fail"
				icon = "❌"
			} else if res.Status == report.Warn {
				statusClass = "warn"
				icon = "⚠️"
			}

			fmt.Fprintf(h.out, "    <div class=\"card %s\">\n", statusClass)
			fmt.Fprintf(h.out, "        <div class=\"card-header\"><span class=\"icon\">%s</span> %s</div>\n", icon, res.Name)
			fmt.Fprintf(h.out, "        <div><span class=\"label\">Status:</span> %s</div>\n", res.Status)
			fmt.Fprintf(h.out, "        <div><span class=\"label\">Severity:</span> %s</div>\n", res.Severity)
			fmt.Fprintf(h.out, "        <div><span class=\"label\">Summary:</span> %s</div>\n", res.Summary)
			
			if res.Details != "" {
				fmt.Fprintf(h.out, "        <div><span class=\"label\">Details:</span><pre>%s</pre></div>\n", res.Details)
			}
			
			if res.Recommendation != "" {
				fmt.Fprintf(h.out, "        <div><span class=\"label\">Recommendation:</span> %s</div>\n", res.Recommendation)
			}
			
			if res.Documentation != "" {
				fmt.Fprintf(h.out, "        <div><span class=\"label\">Documentation:</span> <a href=\"%s\" target=\"_blank\">%s</a></div>\n", res.Documentation, res.Documentation)
			}
			fmt.Fprintln(h.out, "    </div>")
		}
	}

	fmt.Fprintln(h.out, "</body>\n</html>")
	return nil
}
