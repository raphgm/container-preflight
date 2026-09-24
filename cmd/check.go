/*
Copyright © 2026 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/raphgm/container-doctor/internal/engine"
	"github.com/raphgm/container-doctor/internal/executor"
	"github.com/raphgm/container-doctor/internal/renderer"
	"github.com/raphgm/container-doctor/pkg/report"
	"github.com/raphgm/container-doctor/providers/buildx"
	buildxChecks "github.com/raphgm/container-doctor/providers/buildx/checks"
	"github.com/raphgm/container-doctor/providers/compose"
	composeChecks "github.com/raphgm/container-doctor/providers/compose/checks"
	"github.com/raphgm/container-doctor/providers/docker"
	dockerChecks "github.com/raphgm/container-doctor/providers/docker/checks"
	"github.com/raphgm/container-doctor/providers/system"
	systemChecks "github.com/raphgm/container-doctor/providers/system/checks"
)

// checkCmd represents the check command
var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run all registered diagnostic checks",
	Long: `Run all diagnostic checks to determine the health of your environment.
This will query all providers (Docker, Kubernetes, etc.) and generate a report.`,
	Run: func(cmd *cobra.Command, args []string) {
		exec := executor.New()

		reg := engine.NewRegistry()

		reg.Register(system.New(
			systemChecks.NewArch(),
			systemChecks.NewCPU(),
			systemChecks.NewMemory(),
			systemChecks.NewDisk(),
		))

		reg.Register(docker.New(
			dockerChecks.NewInstalled(exec),
			dockerChecks.NewDaemon(exec),
			dockerChecks.NewContext(exec),
			dockerChecks.NewVersion(exec),
			dockerChecks.NewSocket(exec),
		))

		reg.Register(compose.New(
			composeChecks.NewInstalled(exec),
		))

		reg.Register(buildx.New(
			buildxChecks.NewInstalled(exec),
		))

		eng := engine.New(reg)
		rep := eng.Run(cmd.Context())

		var rnd renderer.Renderer
		switch format {
		case "json":
			rnd = renderer.NewJSON()
		case "markdown":
			rnd = renderer.NewMarkdown()
		case "html":
			rnd = renderer.NewHTML()
		case "term":
			fallthrough
		default:
			rnd = renderer.NewTerminal()
		}

		if err := rnd.Render(rep); err != nil {
			fmt.Printf("Error rendering report: %v\n", err)
			os.Exit(1)
		}

		for _, p := range rep.Providers {
			for _, r := range p.Results {
				if r.Status == report.Fail {
					os.Exit(1)
				}
			}
		}
	},
}

var format string

func init() {
	rootCmd.AddCommand(checkCmd)
	checkCmd.Flags().StringVarP(&format, "format", "f", "term", "Output format (term, json, markdown, html)")
}
