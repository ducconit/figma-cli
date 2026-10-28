package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ducconit/figma-cli/internal/figma"
	"github.com/spf13/cobra"
)

func newLinkCmd(global *GlobalFlags) *cobra.Command {
	var (
		fileKey    string
		nodeID     string
		linkURL    string
		linkName   string
		listOnly   bool
		deleteID   string
		asJSON     bool
		baseURL    string
	)

	cmd := &cobra.Command{
		Use:   "link [figma-url-or-file-key]",
		Short: "Manage Dev Mode resources and links (GitHub PRs, Storybook, Jira) on Figma nodes",
		Long: `Attach, inspect, or remove developer resources (GitHub pull requests, Storybook stories,
Jira tickets, documentation URLs) directly on Figma design nodes using Figma Dev Resources REST API.

Examples:
  # Attach a GitHub PR or Storybook link to a Figma node from a URL
  fm link "https://www.figma.com/design/abc123XYZ/App?node-id=12-34" \
    --url "https://github.com/org/repo/pull/42" \
    --name "PR #42: Header Redesign"

  # List all dev resources on a specific node or entire file
  fm link "https://www.figma.com/design/abc123XYZ/App?node-id=12-34" --list
  fm link --file-key abc123XYZ --list --json

  # Delete a dev resource by ID
  fm link --file-key abc123XYZ --delete 98765`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, resolvedFileKey, resolvedNodeID, err := initFigmaClient(global, args, fileKey, nodeID, baseURL)
			if err != nil {
				return err
			}

			// 1. Delete dev resource
			if deleteID != "" {
				if err := client.DeleteDevResource(cmd.Context(), resolvedFileKey, deleteID); err != nil {
					return fmt.Errorf("delete dev resource %s: %w", deleteID, err)
				}
				if asJSON {
					_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
						"deleted":  true,
						"id":       deleteID,
						"file_key": resolvedFileKey,
					})
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted dev resource %s from file %s\n", deleteID, resolvedFileKey)
				return nil
			}

			// 2. List dev resources
			if listOnly {
				var nodeIDs []string
				if resolvedNodeID != "" {
					nodeIDs = []string{resolvedNodeID}
				}
				links, err := client.GetDevResources(cmd.Context(), resolvedFileKey, nodeIDs)
				if err != nil {
					return fmt.Errorf("list dev resources for file %s: %w", resolvedFileKey, err)
				}

				if asJSON {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]any{
						"file_key":      resolvedFileKey,
						"dev_resources": links,
					})
				}

				if len(links) == 0 {
					if resolvedNodeID != "" {
						fmt.Fprintf(cmd.OutOrStdout(), "No dev resources found for node %s in file %s\n", resolvedNodeID, resolvedFileKey)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "No dev resources found for file %s\n", resolvedFileKey)
					}
					return nil
				}

				fmt.Fprintf(cmd.OutOrStdout(), "Dev resources (%d) in file %s:\n", len(links), resolvedFileKey)
				for _, link := range links {
					nodeSuffix := ""
					if link.NodeID != "" {
						nodeSuffix = fmt.Sprintf(" (node: %s)", link.NodeID)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "  • [%s] %s%s (id: %s)\n", link.Name, link.URL, nodeSuffix, link.ID)
				}
				return nil
			}

			// 3. Create dev resource
			if linkURL != "" {
				if resolvedNodeID == "" {
					return errors.New("node ID is required to attach a dev resource link (provide in URL query e.g. ?node-id=12-34 or with --node-id)")
				}
				name := linkName
				if name == "" {
					name = linkURL
				}

				inputs := []figma.DevResourceInput{
					{
						Name:    name,
						URL:     linkURL,
						FileKey: resolvedFileKey,
						NodeID:  resolvedNodeID,
					},
				}

				created, err := client.CreateDevResources(cmd.Context(), resolvedFileKey, inputs)
				if err != nil {
					return fmt.Errorf("create dev resource on node %s: %w", resolvedNodeID, err)
				}

				if asJSON {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]any{
						"created": created,
					})
				}

				for _, res := range created {
					fmt.Fprintf(cmd.OutOrStdout(), "Attached dev resource %q (id: %s) to node %s: %s\n",
						res.Name, res.ID, res.NodeID, res.URL)
				}
				return nil
			}

			return errors.New("specify --url to attach a resource link, --list to view existing links, or --delete <id> to remove a link")
		},
	}

	cmd.Flags().StringVarP(&fileKey, "file-key", "k", "", "Figma file key (overrides URL fileKey)")
	cmd.Flags().StringVarP(&nodeID, "node-id", "n", "", "Figma node ID, e.g. '1234:5678' or '1234-5678' (overrides URL node-id)")
	cmd.Flags().StringVarP(&linkURL, "url", "u", "", "URL of dev resource (GitHub PR, Storybook, Jira, etc.)")
	cmd.Flags().StringVarP(&linkName, "name", "m", "", "Human-readable label for the resource link")
	cmd.Flags().BoolVarP(&listOnly, "list", "l", false, "List existing dev resources on file or node")
	cmd.Flags().StringVarP(&deleteID, "delete", "d", "", "ID of dev resource to delete")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output results in JSON format")
	cmd.Flags().StringVar(&baseURL, "api-base-url", "", "Override Figma API base URL (for testing)")
	_ = cmd.Flags().MarkHidden("api-base-url")

	return cmd
}
