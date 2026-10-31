package cli

import (
	"github.com/ducconit/figma-cli/internal/config"
	"github.com/ducconit/figma-cli/internal/mcp"
	"github.com/spf13/cobra"
)

func newMCPCmd(global *GlobalFlags) *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the built-in local Model Context Protocol (MCP) server over Stdio",
		Long: `Start a zero-dependency JSON-RPC 2.0 Model Context Protocol (MCP) server over stdin/stdout.
Exposes 6 Figma Design tools for AI Coding Agents (Antigravity, Claude Code, Cursor, Windsurf, Codex):
  - get_design_context : Fetch & simplify Figma Design trees into token-optimized Tree DSL / YAML / JSON
  - get_screenshot     : Render & download high-DPI PNG/JPG/SVG node screenshots for visual verification
  - get_tokens         : Extract Figma Variables into CSS, Tailwind v3/v4, SwiftUI, DTCG, or JSON
  - get_motion_context : Extract prototype transitions & easing curves into CSS, Framer Motion, or JSON
  - download_assets    : Auto-discover, download, and crop SVG, PNG, and GIF assets
  - code_connect       : Scan local components, match Figma components, and generate .figma.ts files

Example MCP client configuration:
  {
    "mcpServers": {
      "figma-cli": {
        "command": "fm",
        "args": ["mcp"]
      }
    }
  }`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			auth := config.ResolveAuth(global.Token, global.OAuthToken, global.EnvFile)
			srv := mcp.NewServer(mcp.Options{
				Auth:    auth,
				Proxy:   global.Proxy,
				BaseURL: baseURL,
			}, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			return srv.Serve(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&baseURL, "api-base-url", "", "Override Figma API base URL (for testing)")
	_ = cmd.Flags().MarkHidden("api-base-url")

	return cmd
}
