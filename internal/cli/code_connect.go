package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ducconit/figma-cli/internal/codeconnect"
	"github.com/ducconit/figma-cli/internal/figma"
	"github.com/spf13/cobra"
)

func newCodeConnectCmd(global *GlobalFlags) *cobra.Command {
	var (
		fileKey    string
		nodeID     string
		srcDir     string
		outDir     string
		repoURL    string
		doGenerate bool
		doLink     bool
		asJSON     bool
		baseURL    string
	)

	cmd := &cobra.Command{
		Use:   "code-connect [scan|generate|link] [figma-url-or-file-key]",
		Short: "Scan codebase components, match to Figma components, and generate .figma.ts Code Connect files",
		Long: `Bridge Figma Design components with local codebase components (React .tsx/.jsx, Vue .vue, SwiftUI .swift).
Supports scanning and fuzzy-matching components, generating parserless .figma.ts Code Connect template files,
and attaching Dev Mode resource links back onto Figma component nodes.

Examples:
  # Scan local ./src directory and report matches against a Figma file
  fm code-connect scan "https://www.figma.com/design/abc123XYZ/Design-System" --dir ./src

  # Generate .figma.ts Code Connect templates for matched components
  fm code-connect generate "https://www.figma.com/design/abc123XYZ/Design-System" --dir ./src --out-dir ./figma

  # Attach matched component URLs to Figma nodes via Dev Resources API
  fm code-connect link "https://www.figma.com/design/abc123XYZ/Design-System" --dir ./src --repo-url "https://github.com/org/repo/blob/main"`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := "scan"
			var targetArgs []string
			if len(args) > 0 {
				first := strings.ToLower(strings.TrimSpace(args[0]))
				switch first {
				case "scan", "generate", "link":
					mode = first
					targetArgs = args[1:]
				default:
					targetArgs = args
				}
			}
			if doGenerate {
				mode = "generate"
			}
			if doLink {
				mode = "link"
			}

			client, resolvedFileKey, resolvedNodeID, err := initFigmaClient(global, targetArgs, fileKey, nodeID, baseURL)
			if err != nil {
				return err
			}

			if strings.TrimSpace(srcDir) == "" {
				srcDir = "."
			}

			localComps, err := codeconnect.ScanLocalComponents(srcDir)
			if err != nil {
				return fmt.Errorf("scan local components in %s: %w", srcDir, err)
			}

			ctx := cmd.Context()
			simplified, err := fetchSimplifiedDesign(ctx, client, resolvedFileKey, resolvedNodeID, 0)
			if err != nil {
				return err
			}

			matches := codeconnect.MatchComponents(simplified, localComps, resolvedFileKey)

			var writtenFiles []string
			if mode == "generate" {
				targetOut := outDir
				if strings.TrimSpace(targetOut) == "" {
					targetOut = srcDir
				}
				writtenFiles, err = codeconnect.WriteTemplates(matches, resolvedFileKey, targetOut)
				if err != nil {
					return fmt.Errorf("write Code Connect templates: %w", err)
				}
			}

			var createdLinks []figma.DevResource
			if mode == "link" {
				var inputs []figma.DevResourceInput
				baseRepo := strings.TrimRight(strings.TrimSpace(repoURL), "/")
				if baseRepo == "" {
					baseRepo = "https://github.com/local/repo/blob/main"
				}
				for _, m := range matches {
					if m.Local == nil {
						continue
					}
					inputs = append(inputs, figma.DevResourceInput{
						Name:    fmt.Sprintf("Code Connect: %s (%s)", m.Local.Name, m.Local.RelPath),
						URL:     fmt.Sprintf("%s/%s", baseRepo, strings.TrimLeft(m.Local.RelPath, "/")),
						FileKey: resolvedFileKey,
						NodeID:  m.FigmaID,
					})
				}
				if len(inputs) > 0 {
					createdLinks, err = client.CreateDevResources(ctx, resolvedFileKey, inputs)
					if err != nil {
						return fmt.Errorf("link dev resources: %w", err)
					}
				}
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"fileKey":        resolvedFileKey,
					"mode":           mode,
					"matches":        matches,
					"generatedFiles": writtenFiles,
					"linkedNodes":    createdLinks,
				})
			}

			mappedCount := 0
			for _, m := range matches {
				if m.Local != nil {
					mappedCount++
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Code Connect (%s) for file `%s`: %d/%d Figma components matched (%d local components scanned)\n",
				mode, resolvedFileKey, mappedCount, len(matches), len(localComps))
			for _, m := range matches {
				if m.Local != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "  ✓ [%s] %s -> %s (%s, %s)\n",
						m.FigmaID, m.FigmaName, m.Local.Name, m.Local.RelPath, m.Confidence)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  • [%s] %s -> (unmapped)\n",
						m.FigmaID, m.FigmaName)
				}
			}
			if len(writtenFiles) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Generated %d `.figma.ts` Code Connect file(s):\n", len(writtenFiles))
				for _, wf := range writtenFiles {
					fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", wf)
				}
			}
			if len(createdLinks) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Linked %d Dev Resource(s) on Figma nodes.\n", len(createdLinks))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&fileKey, "file-key", "k", "", "Figma file key (overrides URL fileKey)")
	cmd.Flags().StringVarP(&nodeID, "node-id", "n", "", "Figma node ID, e.g. '1234:5678' (overrides URL node-id)")
	cmd.Flags().StringVar(&srcDir, "dir", ".", "Local project directory to scan for components (.tsx, .jsx, .vue, .swift)")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "Output directory for generated .figma.ts files (default: --dir)")
	cmd.Flags().StringVar(&repoURL, "repo-url", "", "Base repository URL when attaching Dev Resource links in 'link' mode")
	cmd.Flags().BoolVarP(&doGenerate, "generate", "g", false, "Generate parserless .figma.ts Code Connect files for matched components")
	cmd.Flags().BoolVarP(&doLink, "link", "l", false, "Attach matched component links to Figma nodes via Dev Resources API")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output Code Connect report in JSON format")
	cmd.Flags().StringVar(&baseURL, "api-base-url", "", "Override Figma API base URL (for testing)")
	_ = cmd.Flags().MarkHidden("api-base-url")

	return cmd
}
