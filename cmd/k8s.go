package cmd

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/preflight"
	"github.com/raphgm/container-preflight/pkg/preflight/k8s"
)

var (
	k8sContext string
	k8sCluster string
	k8sSaveTo  string
	k8sOffline bool
	k8sFormat  string
)

var k8sCmd = &cobra.Command{
	Use:   "k8s [manifests...]",
	Short: "Predict what applying Kubernetes manifests to a cluster would fail on",
	Long: `Reads manifests (files, directories, or - for stdin, e.g. from
` + "`helm template`" + ` or ` + "`kustomize build`" + `), profiles the target cluster with
read-only kubectl calls, and queries registries for image platforms. It
predicts FailedScheduling, ImagePullBackOff, "no matches for kind", Pending
volume claims and wrong-architecture images before anything is applied.

  container-preflight k8s ./deploy
  helm template my-release ./chart | container-preflight k8s -
  container-preflight k8s --save-cluster prod.json          # capture a cluster
  container-preflight k8s ./deploy --cluster prod.json      # check against it`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if k8sSaveTo != "" {
			snap := k8s.Capture(cmd.Context(), executor.New(), k8sContext)
			f, err := os.Create(k8sSaveTo)
			if err != nil {
				return err
			}
			defer f.Close()
			enc := json.NewEncoder(f)
			enc.SetIndent("", "  ")
			return enc.Encode(snap)
		}
		if len(args) == 0 {
			args = []string{"."}
		}
		opts := preflight.K8sOptions{Paths: args, Context: k8sContext, Offline: k8sOffline}
		if k8sCluster != "" {
			snap, err := k8s.LoadSnapshot(k8sCluster)
			if err != nil {
				return err
			}
			opts.Snapshot = snap
		}
		rep, err := preflight.RunK8s(cmd.Context(), opts)
		if err != nil {
			return err
		}
		if k8sFormat == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(rep); err != nil {
				return err
			}
		} else {
			renderPreflight(os.Stdout, rep, useColor())
		}
		if rep.Errors() > 0 {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(k8sCmd)
	k8sCmd.Flags().StringVar(&k8sContext, "context", "", "kubeconfig context (default: current)")
	k8sCmd.Flags().StringVar(&k8sCluster, "cluster", "", "Check against a saved cluster snapshot instead of a live cluster")
	k8sCmd.Flags().StringVar(&k8sSaveTo, "save-cluster", "", "Save a snapshot of the cluster to this file and exit")
	k8sCmd.Flags().BoolVar(&k8sOffline, "offline", false, "Skip registry lookups")
	k8sCmd.Flags().StringVarP(&k8sFormat, "format", "f", "term", "Output format (term, json)")
}
