package renderer

import (
	"fmt"

	"github.com/raphgm/container-preflight/pkg/report"
)

type Terminal struct{}

func NewTerminal() *Terminal {
	return &Terminal{}
}

func (t *Terminal) Render(r report.Report) error {
	fmt.Println()
	fmt.Println("Container Preflight")
	fmt.Println()

	for _, provider := range r.Providers {
		fmt.Println(provider.Name)
		fmt.Println("────────────────────────")

		for _, result := range provider.Results {
			icon := "✓"

			if result.Status == report.Fail {
				icon = "✗"
			} else if result.Status == report.Warn {
				icon = "!"
			}

			fmt.Printf("%s %s\n", icon, result.Name)

			if result.Details != "" {
				fmt.Printf("   %s\n", result.Details)
			}
		}

		fmt.Println()
	}

	return nil
}
