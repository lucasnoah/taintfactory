package cli

import (
	"fmt"

	"github.com/lucasnoah/taintfactory/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configFile string

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Validate and inspect pipeline configuration",
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate the pipeline configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		errs := config.Validate(cfg)
		if len(errs) == 0 {
			cmd.Println("Configuration is valid.")
			return nil
		}

		cmd.Println("Validation errors:")
		for _, e := range errs {
			cmd.Printf("  - %s\n", e)
		}
		return fmt.Errorf("config has %d validation error(s)", len(errs))
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the resolved configuration with defaults merged",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		data, err := yaml.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshalling config: %w", err)
		}

		cmd.Print(string(data))
		return nil
	},
}

func loadConfig() (*config.PipelineConfig, error) {
	if configFile != "" {
		return config.Load(configFile)
	}
	return config.LoadDefault()
}

func init() {
	configCmd.PersistentFlags().StringVarP(&configFile, "file", "f", "", "path to pipeline config file")
	configCmd.AddCommand(configValidateCmd)
	configCmd.AddCommand(configShowCmd)
}
