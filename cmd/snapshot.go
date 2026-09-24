package cmd

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/preflight/host"
)

var snapshotOut string

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Save this machine's container capabilities to a file",
	Long: `Snapshot records what this machine offers containers: Docker daemon
platform and memory, emulation, tooling versions, kernel settings, local
images and listening ports. Share the file, then run

  container-preflight preflight --host snapshot.json

on any machine to predict whether a project will work on this one.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		snap := host.Capture(cmd.Context(), executor.New())
		out := os.Stdout
		if snapshotOut != "" && snapshotOut != "-" {
			f, err := os.Create(snapshotOut)
			if err != nil {
				return err
			}
			defer f.Close()
			out = f
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(snap)
	},
}

func init() {
	rootCmd.AddCommand(snapshotCmd)
	snapshotCmd.Flags().StringVarP(&snapshotOut, "output", "o", "", "Write to this file instead of stdout")
}
