package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ducconit/figma-cli/internal/figma"
	"github.com/spf13/cobra"
)

// ScreenshotResult represents metadata for a rendered node screenshot.
type ScreenshotResult struct {
	NodeID   string  `json:"nodeId"`
	Format   string  `json:"format"`
	Scale    float64 `json:"scale"`
	FilePath string  `json:"filePath"`
}

func discoverFirstFrameIDs(ctx context.Context, client *figma.Client, fileKey string) ([]string, error) {
	raw, _, err := client.GetRawFile(ctx, fileKey, 2)
	if err != nil {
		return nil, err
	}
	doc, ok := raw["document"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing document in file %s", fileKey)
	}
	pages, _ := doc["children"].([]any)
	for _, rawPage := range pages {
		page, ok := rawPage.(map[string]any)
		if !ok {
			continue
		}
		children, _ := page["children"].([]any)
		for _, rawChild := range children {
			child, ok := rawChild.(map[string]any)
			if !ok {
				continue
			}
			if vis, hasVis := child["visible"].(bool); hasVis && !vis {
				continue
			}
			id, _ := child["id"].(string)
			if id != "" {
				return []string{id}, nil
			}
		}
	}
	return nil, fmt.Errorf("no visible top-level frames found in file %s (pass --node-id explicitly)", fileKey)
}

func newScreenshotCmd(global *GlobalFlags) *cobra.Command {
	var (
		fileKey    string
		nodeID     string
		scale      float64
		format     string
		outputPath string
		asJSON     bool
		baseURL    string
	)

	cmd := &cobra.Command{
		Use:   "screenshot [figma-url-or-file-key]",
		Short: "Render and download a high-DPI visual screenshot of a Figma frame or node",
		Long: `Render a pixel-accurate PNG, JPG, or SVG visual snapshot of a Figma frame, component,
or screen using the Figma Images REST API (/v1/images/:key) and save it locally for visual
comparison during Design-to-Code implementation.

Examples:
  # Render a 2x PNG screenshot of a Figma node URL to OS temp directory
  fm screenshot "https://www.figma.com/design/abc123XYZ/My-App?node-id=12-34"

  # Save screenshot to a specific file path and output metadata as JSON
  fm screenshot "https://www.figma.com/design/abc123XYZ/My-App?node-id=12-34" -o ./tmp/target.png --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, resolvedFileKey, resolvedNodeID, err := initFigmaClient(global, args, fileKey, nodeID, baseURL)
			if err != nil {
				return err
			}

			normFormat := strings.ToLower(strings.TrimSpace(format))
			if normFormat == "" {
				normFormat = "png"
			}
			if normFormat == "jpeg" {
				normFormat = "jpg"
			}
			switch normFormat {
			case "png", "jpg", "svg":
			default:
				return fmt.Errorf("unsupported screenshot format %q (allowed: png, jpg, svg)", format)
			}

			if scale <= 0 {
				scale = 2.0
			}

			ctx := cmd.Context()
			var targetNodeIDs []string
			if resolvedNodeID != "" {
				for _, part := range strings.Split(resolvedNodeID, ",") {
					p := strings.TrimSpace(part)
					if p != "" {
						targetNodeIDs = append(targetNodeIDs, p)
					}
				}
			} else {
				discovered, err := discoverFirstFrameIDs(ctx, client, resolvedFileKey)
				if err != nil {
					return err
				}
				targetNodeIDs = discovered
			}

			renderURLs, err := client.GetNodeRenderURLs(ctx, resolvedFileKey, targetNodeIDs, normFormat, figma.RenderOptions{
				PNGScale:   scale,
				SVGOptions: figma.DefaultSVGOptions(),
			})
			if err != nil {
				return fmt.Errorf("fetch screenshot render URLs: %w", err)
			}

			var results []ScreenshotResult
			for _, nid := range targetNodeIDs {
				normID := figma.NormalizeNodeID(nid)
				rURL := renderURLs[normID]
				if rURL == "" {
					return fmt.Errorf("Figma API returned an empty render URL for node %s in file %s", normID, resolvedFileKey)
				}

				destPath, err := resolveScreenshotDestPath(outputPath, resolvedFileKey, normID, normFormat, len(targetNodeIDs) > 1)
				if err != nil {
					return err
				}

				if err := client.DownloadToFile(ctx, rURL, destPath); err != nil {
					return fmt.Errorf("download screenshot for node %s: %w", normID, err)
				}

				results = append(results, ScreenshotResult{
					NodeID:   normID,
					Format:   normFormat,
					Scale:    scale,
					FilePath: destPath,
				})
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if len(results) == 1 {
					return enc.Encode(results[0])
				}
				return enc.Encode(map[string]any{
					"fileKey":     resolvedFileKey,
					"screenshots": results,
				})
			}

			for _, res := range results {
				fmt.Fprintf(cmd.OutOrStdout(), "Saved screenshot for node `%s` (%s @ %gx) to `%s`\n",
					res.NodeID, res.Format, res.Scale, res.FilePath)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&fileKey, "file-key", "k", "", "Figma file key (overrides URL fileKey)")
	cmd.Flags().StringVarP(&nodeID, "node-id", "n", "", "Figma node ID, e.g. '1234:5678' (overrides URL node-id)")
	cmd.Flags().Float64VarP(&scale, "scale", "s", 2.0, "Render scale factor (1, 2, 3, 4; default: 2)")
	cmd.Flags().StringVarP(&format, "format", "f", "png", "Screenshot image format: png (default), jpg, or svg")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Output file path or directory (default: OS temp directory)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output screenshot metadata in JSON format")
	cmd.Flags().StringVar(&baseURL, "api-base-url", "", "Override Figma API base URL (for testing)")
	_ = cmd.Flags().MarkHidden("api-base-url")

	return cmd
}

func resolveScreenshotDestPath(outputPath, fileKey, nodeID, format string, multiNode bool) (string, error) {
	safeID := figma.SanitizeNodeIDForFile(nodeID)
	defaultName := fmt.Sprintf("screenshot-%s-%s.%s", fileKey, safeID, format)

	cleanOut := strings.TrimSpace(outputPath)
	if cleanOut == "" {
		return filepath.Join(os.TempDir(), defaultName), nil
	}

	absOut, err := filepath.Abs(filepath.Clean(cleanOut))
	if err != nil {
		return "", err
	}

	if info, statErr := os.Stat(absOut); statErr == nil && info.IsDir() {
		return filepath.Join(absOut, defaultName), nil
	}
	if multiNode {
		return filepath.Join(absOut, defaultName), nil
	}
	return absOut, nil
}
