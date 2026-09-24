/*
Copyright © 2026 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// versionCmd represents the version command
// Version is set at build time with -ldflags "-X github.com/raphgm/container-preflight/cmd.Version=v1.0.0".
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the container-preflight version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("container-preflight", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// versionCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// versionCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
