package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/raphgm/container-preflight/pkg/preflight"
	"github.com/raphgm/container-preflight/pkg/preflight/learn"
	"github.com/raphgm/container-preflight/pkg/preflight/project"
	"github.com/raphgm/container-preflight/pkg/preflight/rules"
)

var (
	learnDir     string
	learnService string
	learnFix     string
	learnLog     string
	learnSuccess bool
	learnForce   bool
)

var learnCmd = &cobra.Command{
	Use:   "learn [flags] -- <command>",
	Short: "Turn a real failure into a preflight rule",
	Long: `Learn runs a command (usually ` + "`docker compose up`" + `), and if it fails, extracts
the decisive error line and records the project and host conditions at the
time as a rule in ` + learn.File + `. Preflight then predicts that failure
wherever the same conditions hold. Commit the file to share it.

A second failure with the same error generalizes the rule (conditions that
differ are dropped). Recording a working run with --success refines rules it
contradicts.

  container-preflight learn -- docker compose up db
  container-preflight learn --log build.log --service web
  container-preflight learn --success -- docker compose up -d`,
	RunE: func(cmd *cobra.Command, args []string) error {
		output, failed, err := learnInput(args)
		if err != nil {
			return err
		}

		env, err := preflight.Prepare(cmd.Context(), preflight.Options{Dir: learnDir})
		if err != nil {
			return err
		}
		store, err := learn.Load(env.Project.Dir)
		if err != nil {
			return err
		}
		hostName, _ := os.Hostname()

		if !failed || learnSuccess {
			return recordSuccess(env, store, hostName)
		}

		f, ok := learn.Extract(output)
		if !ok {
			return errors.New("the command failed, but no line in its output explains why; pass the log with --log and --service")
		}
		if learnService != "" {
			f.Service = learnService
		}
		svc := findService(env.Project, f.Service)
		if svc == nil {
			return fmt.Errorf("cannot tell which service failed (saw %q); pass --service", f.Service)
		}

		fmt.Printf("\nFailure in %s: %s\n", svc.Name, f.Line)

		if !learnForce {
			if rule, ok := alreadyPredicted(cmd, env, svc.Name, f.Line); ok {
				fmt.Printf("✓ Already predicted by built-in rule %s. Nothing new to learn.\n", rule)
				return nil
			}
		}

		out := store.ObserveFailure(f, rules.Features(env, svc), hostName)
		if learnFix != "" {
			out.Rule.Fix = learnFix
		}
		if err := store.Save(); err != nil {
			return err
		}

		if out.Created {
			fmt.Printf("✓ Learned new rule %s\n", out.Rule.ID)
		} else {
			fmt.Printf("✓ Rule %s seen again (%d failures)\n", out.Rule.ID, len(out.Rule.Failures))
			if len(out.Dropped) > 0 {
				fmt.Printf("  generalized: dropped %s\n", strings.Join(out.Dropped, ", "))
			}
		}
		fmt.Printf("  signature: %s\n", out.Rule.Signature)
		fmt.Printf("  predicts it when: %s\n", conditions(out.Rule.When))
		fmt.Printf("  saved to %s — commit it to share with your team\n\n", store.Path())
		return nil
	},
}

func init() {
	rootCmd.AddCommand(learnCmd)
	learnCmd.Flags().StringVarP(&learnDir, "dir", "C", ".", "Project directory")
	learnCmd.Flags().StringVarP(&learnService, "service", "s", "", "Service that failed (detected from Compose output when omitted)")
	learnCmd.Flags().StringVar(&learnFix, "fix", "", "Fix to show when the rule predicts this failure")
	learnCmd.Flags().StringVar(&learnLog, "log", "", "Learn from a saved log file (- for stdin) instead of running a command")
	learnCmd.Flags().BoolVar(&learnSuccess, "success", false, "Record a working run to refine learned rules")
	learnCmd.Flags().BoolVar(&learnForce, "force", false, "Learn even if a built-in rule already predicts the failure")
}

// learnInput runs the command (echoing its output) or reads the log.
func learnInput(args []string) (string, bool, error) {
	if learnLog != "" {
		var r io.Reader = os.Stdin
		if learnLog != "-" {
			f, err := os.Open(learnLog)
			if err != nil {
				return "", false, err
			}
			defer f.Close()
			r = f
		}
		b, err := io.ReadAll(r)
		return string(b), true, err
	}
	if len(args) == 0 {
		if learnSuccess {
			return "", false, nil
		}
		return "", false, errors.New("give a command after --, or --log")
	}

	var buf bytes.Buffer
	c := osexec.Command(args[0], args[1:]...)
	c.Dir = learnDir
	c.Stdin = os.Stdin
	c.Stdout = io.MultiWriter(os.Stdout, &buf)
	c.Stderr = io.MultiWriter(os.Stderr, &buf)
	err := c.Run()
	var exit *osexec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return "", false, err
	}
	// `docker compose up` exits 0 even when a container crashes.
	failed := err != nil || containerExited.MatchString(buf.String())
	return buf.String(), failed, nil
}

var containerExited = regexp.MustCompile(`exited with code [1-9]\d*`)

func recordSuccess(env *rules.Env, store *learn.Store, hostName string) error {
	targets := env.Project.Services
	if learnService != "" {
		svc := findService(env.Project, learnService)
		if svc == nil {
			return fmt.Errorf("no service %q", learnService)
		}
		targets = []*project.Service{svc}
	}
	var touched []*learn.Learned
	for _, svc := range targets {
		touched = append(touched, store.ObserveSuccess(svc.Name, rules.Features(env, svc), hostName)...)
	}
	if len(touched) == 0 {
		fmt.Println("\n✓ Run succeeded; no learned rule predicted a failure here.")
		return nil
	}
	if err := store.Save(); err != nil {
		return err
	}
	fmt.Println("\n✓ Run succeeded where learned rules predicted failure; refined:")
	for _, r := range touched {
		fmt.Printf("  %s → %s (when: %s)\n", r.ID, r.Status, conditions(r.When))
	}
	return nil
}

func findService(p *project.Project, name string) *project.Service {
	for _, s := range p.Services {
		if s.Name == name {
			return s
		}
	}
	if name == "" && len(p.Services) == 1 {
		return p.Services[0]
	}
	return nil
}

// alreadyPredicted reports a built-in rule whose predicted error is the
// failure that happened.
func alreadyPredicted(cmd *cobra.Command, env *rules.Env, service, line string) (string, bool) {
	rep := preflight.Evaluate(cmd.Context(), env, rules.All(), nil, time.Now())
	for _, f := range rep.Findings {
		if f.Predicts == "" || (f.Service != "" && f.Service != service) {
			continue
		}
		if similar(f.Predicts, line) {
			return f.Rule, true
		}
	}
	return "", false
}

var word = regexp.MustCompile(`[a-zA-Z]{3,}`)

// similar is true when most words of the predicted message occur in the
// observed line.
func similar(predicted, observed string) bool {
	have := map[string]bool{}
	for _, w := range word.FindAllString(strings.ToLower(observed), -1) {
		have[w] = true
	}
	words := word.FindAllString(strings.ToLower(predicted), -1)
	if len(words) == 0 {
		return false
	}
	hit := 0
	for _, w := range words {
		if have[w] {
			hit++
		}
	}
	return hit*10 >= len(words)*6
}

func conditions(when map[string]string) string {
	if len(when) == 0 {
		return "(always — record a --success run to refine)"
	}
	var out []string
	for k, v := range when {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
