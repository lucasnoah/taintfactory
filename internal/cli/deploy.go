package cli

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/pipeline"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Manage deploy pipelines",
}

var deployCreateCmd = &cobra.Command{
	Use:   "create <commit-sha>",
	Short: "Create a new deploy pipeline for a commit",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sha := args[0]
		namespace, _ := cmd.Flags().GetString("namespace")

		// Normalize SHA via git rev-parse (ADR 0018)
		fullSHA, err := normalizeCommitSHA(sha)
		if err != nil {
			return fmt.Errorf("invalid commit SHA %q: %w", sha, err)
		}

		cfg, err := config.LoadDefault()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		if cfg.Deploy == nil {
			return fmt.Errorf("no deploy: section found in pipeline config")
		}
		if len(cfg.Deploy.Stages) == 0 {
			return fmt.Errorf("deploy pipeline has no stages")
		}

		firstStage := cfg.Deploy.Stages[0].ID

		// Determine previous SHA from latest completed deploy
		d, cleanupDB, err := openDB()
		if err != nil {
			return err
		}
		defer cleanupDB()

		previousSHA := ""
		if prev, err := d.DeployGetLatestCompleted(namespace); err == nil {
			previousSHA = prev.CommitSHA
		}

		store, err := pipeline.DefaultDeployStore()
		if err != nil {
			return fmt.Errorf("open deploy store: %w", err)
		}

		ds, err := store.Create(pipeline.DeployCreateOpts{
			CommitSHA:   fullSHA,
			Namespace:   namespace,
			FirstStage:  firstStage,
			PreviousSHA: previousSHA,
		})
		if err != nil {
			return err
		}

		if err := d.DeployInsert(namespace, fullSHA, previousSHA, firstStage); err != nil {
			return fmt.Errorf("insert deploy record: %w", err)
		}
		if err := d.LogDeployEvent(fullSHA, namespace, "created", firstStage, 0, ""); err != nil {
			return fmt.Errorf("log deploy event: %w", err)
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Deploy pipeline created for %s\n", shortSHA(fullSHA))
		fmt.Fprintf(w, "  Previous SHA: %s\n", displaySHA(ds.PreviousSHA))
		fmt.Fprintf(w, "  First stage:  %s\n", ds.CurrentStage)
		fmt.Fprintf(w, "  Status:       %s\n", ds.Status)
		return nil
	},
}

// normalizeCommitSHA resolves a commit ref to a full 40-char SHA via git rev-parse.
func normalizeCommitSHA(ref string) (string, error) {
	out, err := exec.Command("git", "rev-parse", "--verify", ref).Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func displaySHA(sha string) string {
	if sha == "" {
		return "(none)"
	}
	return shortSHA(sha)
}

// openDeployDB is a convenience for deploy commands that need DB + deploy store.
func openDeployDB() (*db.DB, *pipeline.DeployStore, func(), error) {
	d, cleanupDB, err := openDB()
	if err != nil {
		return nil, nil, nil, err
	}
	store, err := pipeline.DefaultDeployStore()
	if err != nil {
		d.Close()
		return nil, nil, nil, fmt.Errorf("open deploy store: %w", err)
	}
	return d, store, cleanupDB, nil
}

var deployListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent deploys",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := pipeline.DefaultDeployStore()
		if err != nil {
			return fmt.Errorf("open deploy store: %w", err)
		}

		deploys, err := store.List("")
		if err != nil {
			return fmt.Errorf("list deploys: %w", err)
		}

		limit, _ := cmd.Flags().GetInt("limit")
		if limit > 0 && len(deploys) > limit {
			deploys = deploys[:limit]
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(deploys)
		}

		if len(deploys) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No deploys found.")
			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "SHA\tSTATUS\tSTAGE\tNAMESPACE\tCREATED")
		for _, d := range deploys {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				shortSHA(d.CommitSHA), d.Status, d.CurrentStage,
				displayNamespace(d.Namespace), d.CreatedAt)
		}
		return w.Flush()
	},
}

var deployStatusCmd = &cobra.Command{
	Use:   "status [sha]",
	Short: "Show detailed deploy status",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := pipeline.DefaultDeployStore()
		if err != nil {
			return fmt.Errorf("open deploy store: %w", err)
		}

		var ds *pipeline.DeployState
		if len(args) > 0 {
			ds, err = store.Get(args[0])
			if err != nil {
				// Try prefix match
				ds, err = findDeployByPrefix(store, args[0])
			}
		} else {
			// Get most recent
			deploys, listErr := store.List("")
			if listErr != nil {
				return listErr
			}
			if len(deploys) == 0 {
				return fmt.Errorf("no deploys found")
			}
			d := deploys[0]
			ds = &d
			err = nil
		}
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(ds)
		}

		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "Deploy: %s\n", ds.CommitSHA)
		fmt.Fprintf(w, "  Status:       %s\n", ds.Status)
		fmt.Fprintf(w, "  Stage:        %s\n", ds.CurrentStage)
		fmt.Fprintf(w, "  Attempt:      %d\n", ds.CurrentAttempt)
		fmt.Fprintf(w, "  Previous SHA: %s\n", displaySHA(ds.PreviousSHA))
		if ds.Namespace != "" {
			fmt.Fprintf(w, "  Namespace:    %s\n", ds.Namespace)
		}
		fmt.Fprintf(w, "  Created:      %s\n", ds.CreatedAt)
		fmt.Fprintf(w, "  Updated:      %s\n", ds.UpdatedAt)

		if len(ds.StageHistory) > 0 {
			fmt.Fprintln(w, "  History:")
			for _, h := range ds.StageHistory {
				fmt.Fprintf(w, "    %s: %s (attempt %d, %s)\n",
					h.Stage, h.Outcome, h.Attempt, h.Duration)
			}
		}
		return nil
	},
}

// findDeployByPrefix finds a deploy by SHA prefix.
func findDeployByPrefix(store *pipeline.DeployStore, prefix string) (*pipeline.DeployState, error) {
	deploys, err := store.List("")
	if err != nil {
		return nil, err
	}
	for i := range deploys {
		if strings.HasPrefix(deploys[i].CommitSHA, prefix) {
			return &deploys[i], nil
		}
	}
	return nil, fmt.Errorf("deploy %s not found", prefix)
}

func displayNamespace(ns string) string {
	if ns == "" {
		return "-"
	}
	return ns
}

func init() {
	deployCmd.AddCommand(deployCreateCmd)
	deployCmd.AddCommand(deployListCmd)
	deployCmd.AddCommand(deployStatusCmd)

	deployCreateCmd.Flags().String("namespace", "", "Project namespace (e.g., myorg/myapp)")
	deployListCmd.Flags().Int("limit", 20, "Maximum deploys to show")
	deployListCmd.Flags().String("format", "table", "Output format: table or json")
	deployStatusCmd.Flags().String("format", "text", "Output format: text or json")
}
