package cli

import (
	"fmt"
	"strings"

	"github.com/ducconit/figma-cli/internal/figma"
	"github.com/ducconit/figma-cli/internal/images"
	"github.com/spf13/cobra"
)

func newDownloadCmd(global *GlobalFlags) *cobra.Command {
	var (
		fileKey           string
		nodeID            string
		dir               string
		items             []string
		manifest          string
		pngScale          float64
		svgOutlineText    bool
		svgIncludeID      bool
		svgSimplifyStroke bool
		baseURL           string
	)

	cmd := &cobra.Command{
		Use:   "download [figma-url-or-file-key]",
		Short: "Download SVG, PNG, and GIF resources from Figma (defaults to OS temp directory)",
		Long: `Download SVG icons, rendered PNG nodes, static image fills (imageRef), and animated GIFs (gifRef)
from a Figma file. By default, assets are saved to the operating system's temporary directory
(os.TempDir()). Pass --dir to specify a custom target directory.

Features:
  - Deduplicates identical imageRef downloads automatically
  - Applies 2x3 affine matrix cropping (cropTransform) to PNG/JPEG images in pure Go
  - Extracts SVG dimensions from width/height or viewBox
  - Generates --original-width and --original-height CSS variables for TILE fills
  - Auto-discovers all images and IMAGE-SVG nodes when no --item or --manifest is specified

Examples:
  # Auto-discover and download all images/SVGs from a Figma node to OS temp directory
  fm download "https://www.figma.com/design/abc123XYZ/My-App?node-id=12-34"

  # Download to a custom project directory (--dir) using --item flags
  fm download abc123XYZ --dir ./public/images \
    --item "nodeId=10:22,file=ic-bell.svg" \
    --item "nodeId=10:25,imageRef=8f9a1b2c,file=hero.png"

  # Download with cropTransform / TILE metadata using --manifest JSON
  fm download abc123XYZ --dir ./public/images --manifest '[{"nodeId":"10:25","imageRef":"8f9a1b2c","fileName":"hero.png","needsCropping":true,"cropTransform":[[0.5,0,0.25],[0,0.5,0.1]]}]'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, resolvedFileKey, resolvedNodeID, err := initFigmaClient(global, args, fileKey, nodeID, baseURL)
			if err != nil {
				return err
			}

			targetDir, err := images.ResolveTargetDir(dir)
			if err != nil {
				return err
			}

			var nodeRequests []images.DownloadNodeRequest
			for _, it := range items {
				req, err := images.ParseQuickItem(it)
				if err != nil {
					return err
				}
				nodeRequests = append(nodeRequests, req)
			}

			if strings.TrimSpace(manifest) != "" {
				manifestReqs, err := images.ParseManifest(manifest)
				if err != nil {
					return err
				}
				nodeRequests = append(nodeRequests, manifestReqs...)
			}

			ctx := cmd.Context()

			// If no explicit --item or --manifest was given, auto-discover downloadable assets from the file/node
			if len(nodeRequests) == 0 {
				simplified, err := fetchSimplifiedDesign(ctx, client, resolvedFileKey, resolvedNodeID, 0)
				if err != nil {
					return err
				}
				nodeRequests = images.DiscoverDownloadableNodes(simplified)
				if len(nodeRequests) == 0 && resolvedNodeID != "" {
					// Fallback: render the requested nodeId itself as PNG
					safeID := figma.SanitizeNodeIDForFile(resolvedNodeID)
					nodeRequests = append(nodeRequests, images.DownloadNodeRequest{
						NodeID:   resolvedNodeID,
						FileName: fmt.Sprintf("node-%s.png", safeID),
					})
				}
			}

			if len(nodeRequests) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No downloadable images or SVG nodes found in %s.\n", resolvedFileKey)
				return nil
			}

			results, err := images.DownloadFigmaImages(ctx, client, images.DownloadParams{
				FileKey:   resolvedFileKey,
				TargetDir: targetDir,
				Nodes:     nodeRequests,
				PNGScale:  pngScale,
				SVGOptions: figma.SVGOptions{
					OutlineText:    svgOutlineText,
					IncludeID:      svgIncludeID,
					SimplifyStroke: svgSimplifyStroke,
				},
			})
			if err != nil {
				return err
			}

			lines := make([]string, len(results))
			for i, res := range results {
				lines[i] = res.FormatLine()
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Downloaded %d images to `%s`:\n%s\n", len(results), targetDir, strings.Join(lines, "\n"))
			return nil
		},
	}

	cmd.Flags().StringVarP(&fileKey, "file-key", "k", "", "Figma file key (overrides URL fileKey)")
	cmd.Flags().StringVarP(&nodeID, "node-id", "n", "", "Figma node ID, e.g. '1234:5678' (overrides URL node-id)")
	cmd.Flags().StringVar(&dir, "dir", "", "Target directory to save downloaded assets (default: OS temp directory)")
	cmd.Flags().StringArrayVarP(&items, "item", "i", nil, "Asset item to download, e.g. 'nodeId=12:34,file=icon.svg' or '12:34=icon.svg' (repeatable)")
	cmd.Flags().StringVarP(&manifest, "manifest", "m", "", "JSON array (or path to JSON file) of download node specifications")
	cmd.Flags().Float64Var(&pngScale, "scale", 2.0, "Export scale factor for rendered PNG nodes (default: 2)")
	cmd.Flags().BoolVar(&svgOutlineText, "svg-outline-text", true, "Convert text to outlines in rendered SVGs")
	cmd.Flags().BoolVar(&svgIncludeID, "svg-include-id", false, "Include layer ID attributes in rendered SVGs")
	cmd.Flags().BoolVar(&svgSimplifyStroke, "svg-simplify-stroke", true, "Simplify strokes in rendered SVGs")
	cmd.Flags().StringVar(&baseURL, "api-base-url", "", "Override Figma API base URL (for testing)")
	_ = cmd.Flags().MarkHidden("api-base-url")

	return cmd
}
