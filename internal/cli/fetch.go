package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ducconit/figma-cli/internal/serialize"
	"github.com/spf13/cobra"
)

func newFetchCmd(global *GlobalFlags) *cobra.Command {
	var (
		fileKey    string
		nodeID     string
		depth      int
		format     string
		asJSON     bool
		outputPath string
		baseURL    string
	)

	cmd := &cobra.Command{
		Use:   "fetch [figma-url-or-file-key]",
		Short: "Fetch and simplify Figma design data (layout, text, visuals, components)",
		Long: `Fetch a Figma file or specific node subtree from the Figma REST API, simplify all layout
(Flexbox/CSS Grid), typography, rich text, colors, gradients, effects, and components,
collapse pure vector groups into IMAGE-SVG nodes, and serialize into a compact format.

Examples:
  # Fetch from a Figma URL in default compact Tree DSL format
  fm fetch "https://www.figma.com/design/abc123XYZ/My-App?node-id=12-34"

  # Fetch using --file-key and --node-id and output as YAML or JSON
  fm fetch --file-key abc123XYZ --node-id 12:34 --format yaml
  fm fetch --file-key abc123XYZ --node-id 12:34 --json

  # Save output directly to a file
  fm fetch "https://www.figma.com/design/abc123XYZ/My-App?node-id=12-34" -o /tmp/design.tree`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, resolvedFileKey, resolvedNodeID, err := initFigmaClient(global, args, fileKey, nodeID, baseURL)
			if err != nil {
				return err
			}

			outFormat := format
			if outFormat == "" {
				if asJSON {
					outFormat = "json"
				} else {
					outFormat = "tree"
				}
			}

			simplified, err := fetchSimplifiedDesign(cmd.Context(), client, resolvedFileKey, resolvedNodeID, depth)
			if err != nil {
				return err
			}

			formatted, err := serialize.Serialize(simplified, outFormat)
			if err != nil {
				return err
			}

			if outputPath != "" {
				absOut, err := filepath.Abs(outputPath)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
					return fmt.Errorf("create output directory: %w", err)
				}
				if err := os.WriteFile(absOut, []byte(formatted+"\n"), 0o644); err != nil {
					return fmt.Errorf("write output file %s: %w", absOut, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Saved simplified design (%s) to `%s` (%d root nodes, %d shared styles, %d element templates)\n",
					outFormat, absOut, len(simplified.Nodes), len(simplified.GlobalVars.Styles), len(simplified.Elements))
				return nil
			}

			fmt.Fprintln(cmd.OutOrStdout(), formatted)
			return nil
		},
	}

	cmd.Flags().StringVarP(&fileKey, "file-key", "k", "", "Figma file key (overrides URL fileKey)")
	cmd.Flags().StringVarP(&nodeID, "node-id", "n", "", "Figma node ID, e.g. '1234:5678' or '1234-5678' (overrides URL node-id)")
	cmd.Flags().IntVarP(&depth, "depth", "d", 0, "Optional maximum depth to traverse the node tree")
	cmd.Flags().StringVarP(&format, "format", "f", "", "Output format: tree (default), yaml, or json")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Shorthand for --format=json")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Write output to file path instead of stdout")
	cmd.Flags().StringVar(&baseURL, "api-base-url", "", "Override Figma API base URL (for testing)")
	_ = cmd.Flags().MarkHidden("api-base-url")

	return cmd
}
