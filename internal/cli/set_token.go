package cli

import (
	"fmt"
	"strings"

	"github.com/ducconit/figma-cli/internal/config"
	"github.com/spf13/cobra"
)

func newSetTokenCmd() *cobra.Command {
	var (
		isOAuth bool
		homeDir string
	)

	cmd := &cobra.Command{
		Use:   "set-token <token>",
		Short: "Save Figma access token to ~/.figma/.env",
		Long: `Save or update your Figma Personal Access Token (FIGMA_API_KEY) in ~/.figma/.env.
This command exclusively updates ~/.figma/.env (with 0600 file permissions).

At runtime, fm resolves credentials in this strict priority order:
  1. CLI flags (--token / --oauth-token)
  2. Open Project .env file (./.env in the active project root)
  3. User global ~/.figma/.env (configured by 'fm set-token <token>')
  4. Device / OS Environment variables (FIGMA_API_KEY / FIGMA_OAUTH_TOKEN)

Examples:
  # Save a Figma Personal Access Token to ~/.figma/.env
  fm set-token figd_YOUR_PERSONAL_ACCESS_TOKEN

  # Save an OAuth2 Bearer Token to ~/.figma/.env
  fm set-token --oauth YOUR_OAUTH_BEARER_TOKEN`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rawToken := strings.TrimSpace(args[0])
			envPath, err := config.SaveHomeToken(rawToken, isOAuth, homeDir)
			if err != nil {
				return err
			}
			keyName := "FIGMA_API_KEY"
			if isOAuth {
				keyName = "FIGMA_OAUTH_TOKEN"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved %s (%s) to %s\n", keyName, config.MaskToken(rawToken), envPath)
			return nil
		},
	}

	cmd.Flags().BoolVar(&isOAuth, "oauth", false, "Save token as FIGMA_OAUTH_TOKEN instead of FIGMA_API_KEY")
	cmd.Flags().StringVar(&homeDir, "home-dir", "", "Override user home directory")
	_ = cmd.Flags().MarkHidden("home-dir")

	return cmd
}
