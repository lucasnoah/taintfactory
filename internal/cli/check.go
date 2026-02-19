package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lucasnoah/taintfactory/internal/checks"
	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run and manage deterministic checks (lint, test, etc.)",
}

var checkRunCmd = &cobra.Command{
	Use:   "run [issue] [check-names...]",
	Short: "Run one or more checks in an issue's worktree",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}
		checkNames := args[1:]
		fix, _ := cmd.Flags().GetBool("fix")
		cont, _ := cmd.Flags().GetBool("continue")

		d, store, cfg, cleanup, err := openCheckDeps()
		if err != nil {
			return err
		}
		defer cleanup()

		ps, err := store.Get(issue)
		if err != nil {
			return fmt.Errorf("get pipeline state: %w", err)
		}

		runner := checks.NewRunner(&checks.ExecRunner{})
		var results []*checks.Result
		var firstErr error

		for _, name := range checkNames {
			checkCfg, ok := cfg.Pipeline.Checks[name]
			if !ok {
				return fmt.Errorf("check %q not defined in pipeline config", name)
			}

			timeout := parseDuration(checkCfg.Timeout, 2*time.Minute)
			rc := checks.CheckConfig{
				Name:       name,
				Command:    checkCfg.Command,
				Parser:     checkCfg.Parser,
				Timeout:    timeout,
				AutoFix:    fix && checkCfg.AutoFix,
				FixCommand: checkCfg.FixCommand,
			}

			result, err := runner.Run(ps.Worktree, rc)
			if err != nil {
				return fmt.Errorf("run check %q: %w", name, err)
			}

			// Save raw output to disk
			saveRawOutput(store, issue, ps.CurrentStage, ps.CurrentAttempt, name, result)

			// Log to DB
			if err := d.LogCheckRun(
				issue, ps.CurrentStage, ps.CurrentAttempt, ps.CurrentFixRound,
				name, result.Passed, result.AutoFixed, result.ExitCode,
				result.DurationMs, result.Summary, result.Findings,
			); err != nil {
				return fmt.Errorf("log check run: %w", err)
			}

			results = append(results, result)
			statusIcon := "PASS"
			if !result.Passed {
				statusIcon = "FAIL"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s — %s (%dms)\n", statusIcon, name, result.Summary, result.DurationMs)

			if !result.Passed && !cont {
				if firstErr == nil {
					firstErr = fmt.Errorf("check %q failed", name)
				}
				break
			}
		}

		if firstErr != nil {
			return firstErr
		}
		return nil
	},
}

var checkGateCmd = &cobra.Command{
	Use:   "gate [issue] [stage]",
	Short: "Run all checks for a pipeline stage",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// gate is implemented in issue #9
		fmt.Printf("factory check gate %s %s — not implemented (issue #9)\n", args[0], args[1])
		return nil
	},
}

var checkResultCmd = &cobra.Command{
	Use:   "result [issue] [check-name]",
	Short: "Show the latest result for a check",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}
		checkName := args[1]

		d, cleanup, err := openDB()
		if err != nil {
			return err
		}
		defer cleanup()

		run, err := d.GetLatestCheckRun(issue, checkName)
		if err != nil {
			return fmt.Errorf("get check result: %w", err)
		}
		if run == nil {
			return fmt.Errorf("no results found for check %q on issue %d", checkName, issue)
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Check:     %s\n", run.CheckName)
		fmt.Fprintf(w, "Issue:     %d\n", run.Issue)
		fmt.Fprintf(w, "Stage:     %s\n", run.Stage)
		fmt.Fprintf(w, "Attempt:   %d\n", run.Attempt)
		fmt.Fprintf(w, "Fix Round: %d\n", run.FixRound)
		passStr := "FAIL"
		if run.Passed {
			passStr = "PASS"
		}
		fmt.Fprintf(w, "Result:    %s\n", passStr)
		if run.AutoFixed {
			fmt.Fprintf(w, "Auto-Fix:  yes\n")
		}
		fmt.Fprintf(w, "Exit Code: %d\n", run.ExitCode)
		fmt.Fprintf(w, "Duration:  %dms\n", run.DurationMs)
		fmt.Fprintf(w, "Summary:   %s\n", run.Summary)
		if run.Findings != "" {
			fmt.Fprintf(w, "Findings:  %s\n", run.Findings)
		}
		fmt.Fprintf(w, "Timestamp: %s\n", run.Timestamp)

		return nil
	},
}

var checkHistoryCmd = &cobra.Command{
	Use:   "history [issue]",
	Short: "Show all check runs for an issue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid issue number %q: %w", args[0], err)
		}
		checkFilter, _ := cmd.Flags().GetString("check")
		stageFilter, _ := cmd.Flags().GetString("stage")

		d, cleanup, err := openDB()
		if err != nil {
			return err
		}
		defer cleanup()

		runs, err := d.GetCheckHistory(issue)
		if err != nil {
			return fmt.Errorf("get check history: %w", err)
		}

		if len(runs) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No check runs found.")
			return nil
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "%-6s %-15s %-12s %-4s %-3s %-6s %-8s %s\n",
			"ID", "CHECK", "STAGE", "ATT", "FIX", "RESULT", "DURATION", "SUMMARY")
		fmt.Fprintf(w, "%s\n", strings.Repeat("-", 80))

		for _, r := range runs {
			if checkFilter != "" && r.CheckName != checkFilter {
				continue
			}
			if stageFilter != "" && r.Stage != stageFilter {
				continue
			}
			result := "FAIL"
			if r.Passed {
				result = "PASS"
			}
			fmt.Fprintf(w, "%-6d %-15s %-12s %-4d %-3d %-6s %-8s %s\n",
				r.ID, r.CheckName, r.Stage, r.Attempt, r.FixRound, result,
				fmt.Sprintf("%dms", r.DurationMs), r.Summary)
		}

		return nil
	},
}

func init() {
	checkRunCmd.Flags().Bool("fix", false, "Run auto-fix before re-checking")
	checkRunCmd.Flags().Bool("continue", false, "Continue running checks after failures")

	checkGateCmd.Flags().Bool("continue", false, "Run all checks even if some fail")
	checkGateCmd.Flags().Int("fix-round", 0, "Tag this gate run with fix round number")
	checkGateCmd.Flags().String("format", "text", "Output format: text or json")

	checkHistoryCmd.Flags().String("check", "", "Filter by check name")
	checkHistoryCmd.Flags().String("stage", "", "Filter by stage")

	checkCmd.AddCommand(checkRunCmd)
	checkCmd.AddCommand(checkGateCmd)
	checkCmd.AddCommand(checkResultCmd)
	checkCmd.AddCommand(checkHistoryCmd)
}

// openDB opens and migrates the DB, returning it with a cleanup func.
func openDB() (*db.DB, func(), error) {
	dbPath, err := db.DefaultDBPath()
	if err != nil {
		return nil, nil, err
	}
	d, err := db.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	if err := d.Migrate(); err != nil {
		d.Close()
		return nil, nil, err
	}
	return d, func() { d.Close() }, nil
}

// openCheckDeps opens DB, pipeline store, and config for check operations.
func openCheckDeps() (*db.DB, *pipeline.Store, *config.PipelineConfig, func(), error) {
	d, cleanupDB, err := openDB()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	store, err := pipeline.DefaultStore()
	if err != nil {
		cleanupDB()
		return nil, nil, nil, nil, fmt.Errorf("open pipeline store: %w", err)
	}

	cfg, err := config.LoadDefault()
	if err != nil {
		cleanupDB()
		return nil, nil, nil, nil, fmt.Errorf("load config: %w", err)
	}

	return d, store, cfg, cleanupDB, nil
}

// parseDuration parses a duration string, falling back to a default.
func parseDuration(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

// saveRawOutput writes stdout/stderr to disk at the appropriate check path.
func saveRawOutput(store *pipeline.Store, issue int, stage string, attempt int, checkName string, result *checks.Result) {
	dir := store.CheckOutputDir(issue, stage, attempt, checkName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "stdout.txt"), []byte(result.Stdout), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "stderr.txt"), []byte(result.Stderr), 0o644)

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(dir, "result.json"), resultJSON, 0o644)
	}
}
