package cli

import (
	"encoding/json"

	"github.com/ducconit/figma-cli/internal/updater"
	"github.com/ducconit/figma-cli/internal/version"
	"github.com/spf13/cobra"
)

func newUpdateCmd(global *GlobalFlags) *cobra.Command {
	var (
		checkOnly   bool
		force       bool
		targetVer   string
		jsonOutput  bool
		testBaseURL string
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update fm CLI to the latest GitHub release",
		Long: `The update command checks GitHub Releases (https://github.com/ducconit/figma-cli/releases)
for a newer release corresponding to the current operating system and architecture,
downloads the static binary, and atomically replaces the current executable.

Examples:
  # Check for updates and automatically upgrade if a newer release is found
  fm update

  # Only check for available updates without downloading
  fm update --check

  # Force re-download and re-install even if already on the latest version
  fm update --force

  # Install a specific release tag
  fm update --version v0.0.2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Get()

			opts := updater.UpdateOptions{
				CurrentVersion: info.Version,
				TargetVersion:  targetVer,
				TargetOS:       info.OS,
				TargetArch:     info.Arch,
				CheckOnly:      checkOnly,
				Force:          force,
				BaseURL:        testBaseURL,
				Proxy:          global.Proxy,
				Out:            cmd.OutOrStdout(),
			}

			// If JSON output requested, silence progressive logs
			if jsonOutput {
				opts.Out = nil
			}

			res, err := updater.Run(cmd.Context(), opts)
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "Check for available updates without downloading or installing")
	cmd.Flags().BoolVar(&force, "force", false, "Force re-download even if already on the latest version")
	cmd.Flags().StringVar(&targetVer, "version", "", "Target a specific release version (e.g. 'v0.0.2')")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output update result in JSON format")
	cmd.Flags().StringVar(&testBaseURL, "test-base-url", "", "Custom GitHub API base URL (for internal testing)")
	_ = cmd.Flags().MarkHidden("test-base-url")

	return cmd
}
