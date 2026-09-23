/*
Copyright © 2026 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "container-doctor",
	Short: "Predict and diagnose container failures before they happen",
	Long: `container-doctor checks whether a container project will build and run
on a given machine. ` + "`preflight`" + ` joins what the project needs (from its
Compose file and Dockerfiles) with what the host offers and what registries
publish, and reports each predicted failure once, at its root cause.`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.container-doctor.yaml)")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
}
