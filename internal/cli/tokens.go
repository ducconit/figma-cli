package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ducconit/figma-cli/internal/stitch"
	"github.com/ducconit/figma-cli/internal/tokens"
	"github.com/spf13/cobra"
)

func newTokensCmd(global *GlobalFlags) *cobra.Command {
	var (
		fileKey    string
		format     string
		outputPath string
		baseURL    string
	)

	cmd := &cobra.Command{
		Use:   "tokens [figma-url-or-file-key]",
		Short: "Extract Figma Variables (Design Tokens) to CSS, Tailwind v3/v4, SwiftUI, DTCG, or JSON",
		Long: `Extract design tokens (colors, spacing, typography sizes, border radiuses, dark/light themes)
from Figma Variables via the Variables REST API into ready-to-use CSS Variables (:root / .dark),
Tailwind v3 configuration snippets (theme.extend), Tailwind v4 (@theme), SwiftUI DesignTokens,
W3C Design Tokens Community Group (DTCG) format, or structured JSON.

Examples:
  # Export CSS variables to stdout
  fm tokens "https://www.figma.com/design/abc123XYZ/Design-System"

  # Export Tailwind v3 or v4 configuration
  fm tokens --file-key abc123XYZ --format tailwind -o tailwind.tokens.js
  fm tokens --file-key abc123XYZ --format tailwind-v4 -o theme.css

  # Export SwiftUI or W3C DTCG tokens
  fm tokens abc123XYZ --format swiftui -o DesignTokens.swift
  fm tokens abc123XYZ --format dtcg -o tokens.dtcg.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, resolvedFileKey, _, err := initFigmaClient(global, args, fileKey, "", baseURL)
			if err != nil {
				return err
			}

			if format == "stitch" || format == "design-md" {
				spec, err := stitch.ExtractDesignSystem(cmd.Context(), client, resolvedFileKey, "", stitch.ExtractionOptions{})
				if err != nil {
					return fmt.Errorf("extract stitch design system: %w", err)
				}
				exported, err := stitch.SerializeStitch(spec)
				if err != nil {
					return fmt.Errorf("serialize stitch design system: %w", err)
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
					if err := os.WriteFile(absOut, []byte(exported), 0o644); err != nil {
						return fmt.Errorf("write output file %s: %w", absOut, err)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Saved Google Stitch DESIGN.md to `%s`\n", absOut)
					return nil
				}
				fmt.Fprint(cmd.OutOrStdout(), exported)
				return nil
			}

			fmtType, err := tokens.ParseFormat(format)
			if err != nil {
				return err
			}

			meta, err := client.GetLocalVariables(cmd.Context(), resolvedFileKey)
			if err != nil {
				return fmt.Errorf("fetch variables for file %s: %w", resolvedFileKey, err)
			}

			exported, err := tokens.Export(meta, fmtType)
			if err != nil {
				return fmt.Errorf("export tokens: %w", err)
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
				fmt.Fprintf(cmd.OutOrStdout(), "Saved design tokens (%s) to `%s` (%d variables across %d collections)\n",
					fmtType, absOut, len(meta.Variables), len(meta.VariableCollections))
				return nil
			}

			fmt.Fprint(cmd.OutOrStdout(), exported)
			return nil
		},
	}

	cmd.Flags().StringVarP(&fileKey, "file-key", "k", "", "Figma file key (overrides URL fileKey)")
	cmd.Flags().StringVarP(&format, "format", "f", "css", "Output format: css (default), tailwind, tailwind-v4, swiftui, dtcg, stitch, or json")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Write output to file path instead of stdout")
	cmd.Flags().StringVar(&baseURL, "api-base-url", "", "Override Figma API base URL (for testing)")
	_ = cmd.Flags().MarkHidden("api-base-url")

	return cmd
}
