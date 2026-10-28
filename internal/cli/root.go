package cli

import (
	"context"
	"fmt"

	"github.com/ducconit/figma-cli/internal/config"
	"github.com/ducconit/figma-cli/internal/extractors"
	"github.com/ducconit/figma-cli/internal/figma"
	"github.com/ducconit/figma-cli/internal/version"
	"github.com/spf13/cobra"
)

// GlobalFlags holds persistent CLI flags shared across subcommands.
type GlobalFlags struct {
	Token      string
	OAuthToken string
	EnvFile    string
	Proxy      string
}

// initFigmaClient resolves target fileKey/nodeID, validates credentials, and constructs an authenticated Figma client.
func initFigmaClient(global *GlobalFlags, args []string, flagFileKey, flagNodeID, baseURL string) (*figma.Client, string, string, error) {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}

	resolvedFileKey, resolvedNodeID, err := figma.ResolveTarget(positional, flagFileKey, flagNodeID)
	if err != nil {
		return nil, "", "", err
	}

	auth := config.ResolveAuth(global.Token, global.OAuthToken, global.EnvFile)
	if err := auth.Validate(); err != nil {
		return nil, "", "", err
	}

	client, err := figma.NewClient(auth, global.Proxy)
	if err != nil {
		return nil, "", "", err
	}
	if baseURL != "" {
		client.SetBaseURL(baseURL)
	}

	return client, resolvedFileKey, resolvedNodeID, nil
}

// fetchSimplifiedDesign fetches either a specific node subtree or the full file and runs the simplification pipeline.
func fetchSimplifiedDesign(ctx context.Context, client *figma.Client, fileKey, nodeID string, depth int) (*extractors.SimplifiedDesign, error) {
	var (
		rawData map[string]any
		err     error
	)
	if nodeID != "" {
		rawData, _, err = client.GetRawNode(ctx, fileKey, nodeID, depth)
	} else {
		rawData, _, err = client.GetRawFile(ctx, fileKey, depth)
	}
	if err != nil {
		return nil, err
	}

	return extractors.SimplifyRawFigmaObject(rawData, extractors.Options{
		MaxDepth: depth,
	})
}

// NewRootCmd constructs the root `fm` Cobra command with `-v`/`--version` and `-h`/`--help` support.
func NewRootCmd() *cobra.Command {
	var flags GlobalFlags
	var showVersion bool

	info := version.Get()

	rootCmd := &cobra.Command{
		Use:   "fm",
		Short: "fm — Cross-platform Figma Design & Asset Extraction CLI for AI Agents",
		Long: `fm is a zero-dependency, cross-platform CLI tool that fetches Figma design trees,
simplifies layout/Flexbox/CSS Grid/typography/gradients/components into token-efficient
formats (tree DSL, YAML, JSON), and downloads/crops SVG, PNG, and GIF assets.

Authentication is resolved in order:
  1. CLI flags (--token / --oauth-token)
  2. Project .env file in current directory (./.env, or --env <path>)
  3. User global ~/.figma/.env file
  4. OS Environment variables: FIGMA_API_KEY (PAT) or FIGMA_OAUTH_TOKEN (OAuth Bearer)`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       info.String(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				fmt.Fprintln(cmd.OutOrStdout(), info.String())
				return nil
			}
			return cmd.Help()
		},
	}

	rootCmd.SetVersionTemplate("{{.Version}}\n")
	rootCmd.Flags().BoolVarP(&showVersion, "version", "v", false, "Print version information and exit")

	rootCmd.PersistentFlags().StringVar(&flags.Token, "token", "", "Figma Personal Access Token (overrides FIGMA_API_KEY)")
	rootCmd.PersistentFlags().StringVar(&flags.OAuthToken, "oauth-token", "", "Figma OAuth Bearer token (overrides FIGMA_OAUTH_TOKEN)")
	rootCmd.PersistentFlags().StringVar(&flags.EnvFile, "env", "", "Path to .env file (default: ./.env)")
	rootCmd.PersistentFlags().StringVar(&flags.Proxy, "proxy", "", "Explicit HTTP/HTTPS proxy URL, or 'none' to bypass system proxy")

	rootCmd.AddCommand(
		newFetchCmd(&flags),
		newDownloadCmd(&flags),
		newTokensCmd(&flags),
		newLinkCmd(&flags),
		newInstallCmd(&flags),
		newSetTokenCmd(),
		newUpdateCmd(&flags),
		newVersionCmd(),
	)

	return rootCmd
}
