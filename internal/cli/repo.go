package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/lucasnoah/taintfactory/internal/db"
	"github.com/lucasnoah/taintfactory/internal/dbprov"
	"github.com/spf13/cobra"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage registered repositories",
}

var repoAddCmd = &cobra.Command{
	Use:   "add <repo_url>",
	Short: "Register a repository",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoURL := args[0]
		localPath, _ := cmd.Flags().GetString("local-path")
		configPath, _ := cmd.Flags().GetString("config")
		pollLabel, _ := cmd.Flags().GetString("label")
		pollInterval, _ := cmd.Flags().GetInt("poll-interval")
		namespace, _ := cmd.Flags().GetString("namespace")

		if namespace == "" {
			namespace = repoURLToNamespace(repoURL)
		}

		d, cleanup, err := openDB()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := d.RepoAdd(db.RepoRecord{
			Namespace:    namespace,
			RepoURL:      repoURL,
			LocalPath:    localPath,
			ConfigPath:   configPath,
			PollLabel:    pollLabel,
			PollInterval: pollInterval,
			Active:       true,
		}); err != nil {
			return err
		}

		fmt.Printf("Registered %s\n", namespace)

		// Auto-provision database if configured
		if configPath != "" {
			if cfg, err := config.Load(configPath); err == nil && cfg.Pipeline.Database != nil {
				databaseURL := os.Getenv("DATABASE_URL")
				if databaseURL == "" {
					fmt.Fprintln(os.Stderr, "warning: DATABASE_URL not set, skipping database provisioning")
				} else if adminStr, err := dbprov.AdminConnStr(databaseURL); err == nil {
					if adminConn, err := sql.Open("pgx", adminStr); err == nil {
						defer adminConn.Close()
						if err := dbprov.Provision(adminConn, cfg.Pipeline.Database); err != nil {
							fmt.Fprintf(os.Stderr, "warning: database provisioning failed: %v\n", err)
						} else {
							fmt.Printf("Provisioned database %s\n", cfg.Pipeline.Database.Name)
						}
					}
				}
			}
		}

		return nil
	},
}

var repoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered repositories",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, cleanup, err := openDB()
		if err != nil {
			return err
		}
		defer cleanup()

		repos, err := d.RepoList()
		if err != nil {
			return err
		}

		format, _ := cmd.Flags().GetString("format")
		if format == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(repos)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAMESPACE\tREPO URL\tLABEL\tINTERVAL\tACTIVE")
		for _, r := range repos {
			label := r.PollLabel
			if label == "" {
				label = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%ds\t%v\n", r.Namespace, r.RepoURL, label, r.PollInterval, r.Active)
		}
		return w.Flush()
	},
}

var repoRemoveCmd = &cobra.Command{
	Use:   "remove <namespace>",
	Short: "Remove a registered repository",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		d, cleanup, err := openDB()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := d.RepoRemove(args[0]); err != nil {
			return err
		}
		fmt.Printf("Removed %s\n", args[0])
		return nil
	},
}

var repoUpdateCmd = &cobra.Command{
	Use:   "update <namespace>",
	Short: "Update a registered repository",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		d, cleanup, err := openDB()
		if err != nil {
			return err
		}
		defer cleanup()

		opts := db.RepoUpdateOpts{}
		if cmd.Flags().Changed("label") {
			v, _ := cmd.Flags().GetString("label")
			opts.PollLabel = &v
		}
		if cmd.Flags().Changed("poll-interval") {
			v, _ := cmd.Flags().GetInt("poll-interval")
			opts.PollInterval = &v
		}
		if cmd.Flags().Changed("active") {
			v, _ := cmd.Flags().GetString("active")
			b, _ := strconv.ParseBool(v)
			opts.Active = &b
		}

		if err := d.RepoUpdate(args[0], opts); err != nil {
			return err
		}
		fmt.Printf("Updated %s\n", args[0])
		return nil
	},
}

var repoProvisionDBCmd = &cobra.Command{
	Use:   "provision-db [namespace]",
	Short: "Provision databases for registered repositories",
	Long:  "Creates PostgreSQL databases and users for repos with database config. Requires DATABASE_URL env var.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		databaseURL := os.Getenv("DATABASE_URL")
		if databaseURL == "" {
			fmt.Fprintln(os.Stderr, "error: DATABASE_URL environment variable not set")
			os.Exit(1)
		}

		adminStr, err := dbprov.AdminConnStr(databaseURL)
		if err != nil {
			return fmt.Errorf("parse DATABASE_URL: %w", err)
		}

		adminConn, err := sql.Open("pgx", adminStr)
		if err != nil {
			return fmt.Errorf("connect to admin database: %w", err)
		}
		defer adminConn.Close()

		d, cleanup, err := openDB()
		if err != nil {
			return err
		}
		defer cleanup()

		repos, err := d.RepoList()
		if err != nil {
			return err
		}

		var filterNS string
		if len(args) > 0 {
			filterNS = args[0]
		}

		failures := 0
		provisioned := 0
		for _, r := range repos {
			if filterNS != "" && r.Namespace != filterNS {
				continue
			}
			if r.ConfigPath == "" {
				continue
			}
			cfg, err := config.Load(r.ConfigPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s: failed to load config: %v\n", r.Namespace, err)
				failures++
				continue
			}
			if cfg.Pipeline.Database == nil {
				continue
			}
			fmt.Fprintf(os.Stderr, "provisioning database for %s: %s\n", r.Namespace, cfg.Pipeline.Database.Name)
			if err := dbprov.Provision(adminConn, cfg.Pipeline.Database); err != nil {
				fmt.Fprintf(os.Stderr, "error: %s: %v\n", r.Namespace, err)
				failures++
				continue
			}
			provisioned++
			fmt.Printf("provisioned %s/%s\n", r.Namespace, cfg.Pipeline.Database.Name)
		}

		if failures > 0 {
			fmt.Fprintf(os.Stderr, "%d provisioned, %d failed\n", provisioned, failures)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "%d databases provisioned\n", provisioned)
		return nil
	},
}

// repoURLToNamespace extracts "owner/repo" from a GitHub URL.
func repoURLToNamespace(url string) string {
	for _, prefix := range []string{"https://github.com/", "github.com/"} {
		if strings.HasPrefix(url, prefix) {
			return strings.TrimPrefix(url, prefix)
		}
	}
	return url
}

func init() {
	repoCmd.AddCommand(repoAddCmd)
	repoCmd.AddCommand(repoListCmd)
	repoCmd.AddCommand(repoRemoveCmd)
	repoCmd.AddCommand(repoUpdateCmd)
	repoCmd.AddCommand(repoProvisionDBCmd)

	repoAddCmd.Flags().String("local-path", "", "Local path to cloned repo")
	repoAddCmd.Flags().String("config", "", "Path to pipeline.yaml")
	repoAddCmd.Flags().String("label", "", "GitHub label to poll for")
	repoAddCmd.Flags().Int("poll-interval", 120, "Poll interval in seconds")
	repoAddCmd.Flags().String("namespace", "", "Namespace (default: derived from repo URL)")
	_ = repoAddCmd.MarkFlagRequired("local-path")
	_ = repoAddCmd.MarkFlagRequired("config")

	repoListCmd.Flags().String("format", "table", "Output format: table or json")

	repoUpdateCmd.Flags().String("label", "", "GitHub label to poll for")
	repoUpdateCmd.Flags().Int("poll-interval", 120, "Poll interval in seconds")
	repoUpdateCmd.Flags().String("active", "", "Enable/disable repo (true/false)")
}
