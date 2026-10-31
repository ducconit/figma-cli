package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ducconit/figma-cli/internal/stitch"
	"github.com/spf13/cobra"
)

func newStitchCmd(global *GlobalFlags) *cobra.Command {
	var (
		fileKey     string
		nodeID      string
		depth       int
		forceCanvas bool
		validate    bool
		asJSON      bool
		outputPath  string
		baseURL     string
	)

	cmd := &cobra.Command{
		Use:     "stitch [figma-url-or-file-key]",
		Aliases: []string{"design-system", "design-md"},
		Short:   "Extract and export a Google Stitch DESIGN.md specification from Figma",
		Long: `Extract design system tokens (colors, typography, spacing, border radiuses, and components)
from Figma Variables and Canvas nodes, and serialize them into a 100% compliant Google Stitch DESIGN.md document.

The extractor uses a Dual-Source engine:
  1. Figma Variables API (when available in Pro/Enterprise files)
  2. Canvas Heuristic Extractor (for UI Kits & Community files with frames for Colors, Typography, and Components)

Examples:
  # Export DESIGN.md from a Figma Design System page URL
  fm stitch "https://www.figma.com/design/abc123XYZ/DreamsPOS?node-id=1:133" -o DESIGN.md

  # Export directly to stdout
  fm stitch "https://www.figma.com/design/abc123XYZ/Design-System"

  # Force canvas heuristic extraction even if variables exist
  fm stitch abc123XYZ --force-canvas -o DESIGN.md

  # Export as structured JSON
  fm stitch abc123XYZ --json -o design-system.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, resolvedFileKey, resolvedNodeID, err := initFigmaClient(global, args, fileKey, nodeID, baseURL)
			if err != nil {
				return err
			}

			if depth <= 0 {
				depth = 5
			}

			spec, err := stitch.ExtractDesignSystem(cmd.Context(), client, resolvedFileKey, resolvedNodeID, stitch.ExtractionOptions{
				Depth:       depth,
				ForceCanvas: forceCanvas,
				Validate:    validate,
			})
			if err != nil {
				return fmt.Errorf("extract design system: %w", err)
			}

			if validate {
				warnings := stitch.ValidateTokens(spec)
				for _, w := range warnings {
					fmt.Fprintf(cmd.ErrOrStderr(), "Token validation note: %s\n", w)
				}
			}

			var output string
			if asJSON {
				output, err = spec.ToJSON()
			} else {
				output, err = stitch.SerializeStitch(spec)
			}
			if err != nil {
				return fmt.Errorf("serialize design system: %w", err)
			}

			if outputPath != "" {
				cleanOut := filepath.Clean(outputPath)
				absOut, err := filepath.Abs(cleanOut)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
					return fmt.Errorf("create output directory: %w", err)
				}
				if err := os.WriteFile(absOut, []byte(output), 0o644); err != nil {
					return fmt.Errorf("write output file %s: %w", absOut, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Successfully generated Google Stitch DESIGN.md (`%s`) with %d colors, %d typography tokens, %d spacing scales, %d rounded radiuses, and %d components.\n",
					absOut, len(spec.Colors), len(spec.Typography), len(spec.Spacing), len(spec.Rounded), len(spec.Components))
				return nil
			}

			fmt.Fprint(cmd.OutOrStdout(), output)
			return nil
		},
	}

	cmd.Flags().StringVarP(&fileKey, "file-key", "k", "", "Figma file key (overrides URL fileKey)")
	cmd.Flags().StringVarP(&nodeID, "node-id", "n", "", "Target Design System node ID (e.g. 1:133)")
	cmd.Flags().IntVarP(&depth, "depth", "d", 5, "Traversal depth for Canvas node extraction (default: 5)")
	cmd.Flags().BoolVar(&forceCanvas, "force-canvas", false, "Force Canvas heuristic extraction and bypass Variables API")
	cmd.Flags().BoolVar(&validate, "validate", false, "Validate token references and print warnings for resolved fallbacks")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output machine-readable JSON specification instead of Markdown")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Write output to file path (e.g. -o DESIGN.md) instead of stdout")
	cmd.Flags().StringVar(&baseURL, "api-base-url", "", "Override Figma API base URL (for testing)")
	_ = cmd.Flags().MarkHidden("api-base-url")

	return cmd
}
