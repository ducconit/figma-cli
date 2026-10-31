package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ducconit/figma-cli/internal/motion"
	"github.com/spf13/cobra"
)

func newMotionCmd(global *GlobalFlags) *cobra.Command {
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
		Use:   "motion [figma-url-or-file-key]",
		Short: "Extract Figma prototype transitions, animations, and easing curves to CSS, Framer Motion, or JSON",
		Long: `Extract prototype interactions, smart animate transitions, durations, and custom cubic-bezier
easing curves from a Figma Design file or node subtree into CSS (@keyframes + transition utilities),
Framer Motion / motion.dev transition definitions, or structured JSON.

Examples:
  # Extract CSS keyframes and transition classes from a Figma node URL
  fm motion "https://www.figma.com/design/abc123XYZ/My-App?node-id=12-34"

  # Export Framer Motion / motion.dev transition definitions
  fm motion "https://www.figma.com/design/abc123XYZ/My-App?node-id=12-34" --format framer -o ./src/motion.ts

  # Export structured JSON motion inventory
  fm motion abc123XYZ --node-id 12:34 --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, resolvedFileKey, resolvedNodeID, err := initFigmaClient(global, args, fileKey, nodeID, baseURL)
			if err != nil {
				return err
			}

			outFormat := format
			if asJSON && outFormat == "" {
				outFormat = "json"
			}
			fmtType, err := motion.ParseFormat(outFormat)
			if err != nil {
				return err
			}

			var rawData map[string]any
			if resolvedNodeID != "" {
				rawData, _, err = client.GetRawNode(cmd.Context(), resolvedFileKey, resolvedNodeID, depth)
			} else {
				rawData, _, err = client.GetRawFile(cmd.Context(), resolvedFileKey, depth)
			}
			if err != nil {
				return err
			}

			report, err := motion.ExtractMotion(rawData)
			if err != nil {
				return err
			}

			exported, err := motion.Export(report, fmtType)
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
				if err := os.WriteFile(absOut, []byte(exported), 0o644); err != nil {
					return fmt.Errorf("write output file %s: %w", absOut, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Saved motion specs (%s) to `%s` (%d transitions)\n",
					fmtType, absOut, len(report.Transitions))
				return nil
			}

			fmt.Fprint(cmd.OutOrStdout(), exported)
			return nil
		},
	}

	cmd.Flags().StringVarP(&fileKey, "file-key", "k", "", "Figma file key (overrides URL fileKey)")
	cmd.Flags().StringVarP(&nodeID, "node-id", "n", "", "Figma node ID, e.g. '1234:5678' (overrides URL node-id)")
	cmd.Flags().IntVarP(&depth, "depth", "d", 0, "Optional maximum depth to traverse the node tree")
	cmd.Flags().StringVarP(&format, "format", "f", "", "Output format: css (default), framer, or json")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Shorthand for --format=json")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Write output to file path instead of stdout")
	cmd.Flags().StringVar(&baseURL, "api-base-url", "", "Override Figma API base URL (for testing)")
	_ = cmd.Flags().MarkHidden("api-base-url")

	return cmd
}
