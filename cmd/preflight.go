package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/raphgm/container-doctor/pkg/preflight"
	"github.com/raphgm/container-doctor/pkg/preflight/fact"
	"github.com/raphgm/container-doctor/pkg/preflight/host"
)

var (
	preflightFormat  string
	preflightOffline bool
	preflightHost    string
	preflightRun     string
)

var preflightCmd = &cobra.Command{
	Use:   "preflight [path]",
	Short: "Predict whether a project will build and run on this machine",
	Long: `Preflight reads the project's Compose file and Dockerfiles, profiles this
host (the Docker daemon, its VM, tooling, ports, kernel settings) and queries
registries for image platforms and sizes. It then predicts the failures
` + "`docker compose up`" + ` would hit, before anything runs.

Checks that cannot run because something upstream is broken are grouped
under that root cause instead of being reported as separate failures.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}
		opts := preflight.Options{Dir: dir, Offline: preflightOffline, Command: preflightRun}
		if preflightHost != "" {
			snap, err := host.LoadSnapshot(preflightHost)
			if err != nil {
				return err
			}
			opts.Host = snap
		}
		rep, err := preflight.Run(cmd.Context(), opts)
		if err != nil {
			return err
		}
		switch preflightFormat {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(rep); err != nil {
				return err
			}
		default:
			renderPreflight(os.Stdout, rep, useColor())
		}
		if rep.Errors() > 0 {
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(preflightCmd)
	preflightCmd.Flags().StringVarP(&preflightFormat, "format", "f", "term", "Output format (term, json)")
	preflightCmd.Flags().BoolVar(&preflightOffline, "offline", false, "Skip registry lookups")
	preflightCmd.Flags().StringVar(&preflightRun, "run", "", "Check a `docker run`, `docker create` or `docker pull` command instead of a Compose project")
	preflightCmd.Flags().StringVar(&preflightHost, "host", "", "Predict for the machine in this snapshot file instead of this one (see `snapshot`)")
}

type palette struct{ red, yellow, green, dim, bold, reset string }

func useColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func renderPreflight(w io.Writer, rep *preflight.Report, color bool) {
	c := palette{}
	if color {
		c = palette{"\033[31m", "\033[33m", "\033[32m", "\033[2m", "\033[1m", "\033[0m"}
	}
	icon := func(s fact.Severity) string {
		switch s {
		case fact.Error:
			return c.red + "✗" + c.reset
		case fact.Warning:
			return c.yellow + "!" + c.reset
		}
		return c.dim + "i" + c.reset
	}

	h := rep.Host
	fmt.Fprintf(w, "\n%sContainer Doctor · preflight%s  %s%s%s\n", c.bold, c.reset, c.dim, rep.Dir, c.reset)
	if rep.HostName != "" {
		fmt.Fprintf(w, "%sPredicting for snapshot of %s%s\n", c.yellow, rep.HostName, c.reset)
	}
	var hostBits []string
	if h.OperatingSystem != "" {
		hostBits = append(hostBits, h.OperatingSystem, h.Platform, humanSize(h.MemTotal)+" RAM for containers")
	}
	if h.ComposeVersion != "" {
		hostBits = append(hostBits, "Compose "+h.ComposeVersion)
	}
	if h.BuildxInstalled {
		hostBits = append(hostBits, "buildx "+h.BuildxVersion)
	}
	if len(hostBits) > 0 {
		fmt.Fprintf(w, "%sHost: %s%s\n", c.dim, strings.Join(hostBits, " · "), c.reset)
	}

	if len(rep.Roots) > 0 {
		fmt.Fprintf(w, "\n%sROOT CAUSES%s\n", c.bold, c.reset)
		for _, rc := range rep.Roots {
			fmt.Fprintf(w, "  %s %s\n", icon(rc.Severity), rc.Summary)
			if rc.Detail != "" {
				fmt.Fprintf(w, "      %s%s%s\n", c.dim, rc.Detail, c.reset)
			}
			if rc.Fix != "" {
				fmt.Fprintf(w, "      fix: %s\n", rc.Fix)
			}
			if len(rc.Blocks) > 0 {
				fmt.Fprintf(w, "      %sblocks %d check(s): %s%s\n", c.dim, len(rc.Blocks), strings.Join(rc.Blocks, ", "), c.reset)
			}
		}
	}

	var errs, warns int
	for _, f := range rep.Findings {
		switch f.Severity {
		case fact.Error:
			errs++
		case fact.Warning:
			warns++
		}
	}
	if len(rep.Findings) > 0 {
		fmt.Fprintf(w, "\n%sPREDICTED PROBLEMS%s  %d error(s), %d warning(s)\n", c.bold, c.reset, errs, warns)
		for _, f := range rep.Findings {
			where := ""
			if f.Service != "" {
				where = "[" + f.Service + "] "
			}
			fmt.Fprintf(w, "  %s %s%s", icon(f.Severity), where, f.Title)
			if f.Location != "" {
				fmt.Fprintf(w, "  %s%s%s", c.dim, f.Location, c.reset)
			}
			fmt.Fprintln(w)
			for _, e := range f.Evidence {
				fmt.Fprintf(w, "      %s%s%s\n", c.dim, e, c.reset)
			}
			if f.Predicts != "" {
				fmt.Fprintf(w, "      %swould fail with:%s %q\n", c.dim, c.reset, f.Predicts)
			}
			if f.Fix != "" {
				fmt.Fprintf(w, "      fix: %s\n", f.Fix)
			}
		}
	}

	if len(rep.Passed) > 0 {
		fmt.Fprintf(w, "\n%s✓ passed:%s %s%s%s\n", c.green, c.reset, c.dim, strings.Join(rep.Passed, ", "), c.reset)
	}

	total := rep.Errors()
	fmt.Fprintln(w)
	if total > 0 {
		fmt.Fprintf(w, "%s%s✗ Will fail:%s %d blocking problem(s) predicted  %s(%s)%s\n\n", c.bold, c.red, c.reset, total, c.dim, rep.Duration.Round(1e6), c.reset)
	} else if warns > 0 {
		fmt.Fprintf(w, "%s%s! Should run%s, with %d warning(s)  %s(%s)%s\n\n", c.bold, c.yellow, c.reset, warns, c.dim, rep.Duration.Round(1e6), c.reset)
	} else {
		fmt.Fprintf(w, "%s%s✓ Ready to run%s  %s(%s)%s\n\n", c.bold, c.green, c.reset, c.dim, rep.Duration.Round(1e6), c.reset)
	}
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
