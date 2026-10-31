package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ducconit/figma-cli/internal/codeconnect"
	"github.com/ducconit/figma-cli/internal/config"
	"github.com/ducconit/figma-cli/internal/extractors"
	"github.com/ducconit/figma-cli/internal/figma"
	"github.com/ducconit/figma-cli/internal/images"
	"github.com/ducconit/figma-cli/internal/motion"
	"github.com/ducconit/figma-cli/internal/serialize"
	"github.com/ducconit/figma-cli/internal/tokens"
	"github.com/ducconit/figma-cli/internal/version"
)

// Options holds configuration for the local Stdio MCP server.
type Options struct {
	Auth    config.AuthConfig
	Proxy   string
	BaseURL string
}

// Server implements a JSON-RPC 2.0 Model Context Protocol (MCP) server over Stdio.
type Server struct {
	opts   Options
	in     io.Reader
	out    io.Writer
	errOut io.Writer
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type callToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// NewServer constructs a new Stdio MCP server instance.
func NewServer(opts Options, in io.Reader, out io.Writer, errOut io.Writer) *Server {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	return &Server{
		opts:   opts,
		in:     in,
		out:    out,
		errOut: errOut,
	}
}

// Serve processes newline-delimited JSON-RPC 2.0 messages until EOF or context cancellation.
func (s *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(s.in)
	// Allow up to 4MB JSON-RPC request payloads
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.writeError(nil, -32700, "Parse error: invalid JSON-RPC payload")
			continue
		}

		// Notifications do not have an ID and do not expect a response
		if req.ID == nil && strings.HasPrefix(req.Method, "notifications/") {
			continue
		}

		s.handleRequest(ctx, req)
	}

	return scanner.Err()
}

func (s *Server) writeResponse(id any, result any) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(s.out, "%s\n", string(b))
}

func (s *Server) writeError(id any, code int, msg string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonRPCError{
			Code:    code,
			Message: figma.SanitizeAndRedactBody(msg, s.opts.Auth.Secrets()),
		},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(s.out, "%s\n", string(b))
}

func (s *Server) writeToolResult(id any, text string, isError bool) {
	safeText := figma.SanitizeAndRedactBody(text, s.opts.Auth.Secrets())
	s.writeResponse(id, map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": safeText,
			},
		},
		"isError": isError,
	})
}

func (s *Server) handleRequest(ctx context.Context, req jsonRPCRequest) {
	switch req.Method {
	case "initialize":
		info := version.Get()
		s.writeResponse(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "figma-cli",
				"version": info.Version,
			},
		})

	case "ping":
		s.writeResponse(req.ID, map[string]any{})

	case "tools/list":
		s.writeResponse(req.ID, map[string]any{
			"tools": s.toolDefinitions(),
		})

	case "tools/call":
		var params callToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(req.ID, -32602, "Invalid params for tools/call")
			return
		}
		out, err := s.executeTool(ctx, params.Name, params.Arguments)
		if err != nil {
			s.writeToolResult(req.ID, fmt.Sprintf("Error executing %s: %v", params.Name, err), true)
			return
		}
		s.writeToolResult(req.ID, out, false)

	default:
		s.writeError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func (s *Server) buildClient() (*figma.Client, error) {
	if err := s.opts.Auth.Validate(); err != nil {
		return nil, err
	}
	client, err := figma.NewClient(s.opts.Auth, s.opts.Proxy)
	if err != nil {
		return nil, err
	}
	if s.opts.BaseURL != "" {
		client.SetBaseURL(s.opts.BaseURL)
	}
	return client, nil
}

func strArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func floatArg(args map[string]any, key string, def float64) float64 {
	if args == nil {
		return def
	}
	switch v := args[key].(type) {
	case float64:
		if v > 0 {
			return v
		}
	case int:
		if v > 0 {
			return float64(v)
		}
	}
	return def
}

func intArg(args map[string]any, key string, def int) int {
	if args == nil {
		return def
	}
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func boolArg(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	b, _ := args[key].(bool)
	return b
}

func (s *Server) executeTool(ctx context.Context, name string, args map[string]any) (string, error) {
	client, err := s.buildClient()
	if err != nil {
		return "", err
	}

	rawURL := strArg(args, "url")
	flagFileKey := strArg(args, "fileKey")
	flagNodeID := strArg(args, "nodeId")

	switch name {
	case "get_design_context":
		fileKey, nodeID, err := figma.ResolveTarget(rawURL, flagFileKey, flagNodeID)
		if err != nil {
			return "", err
		}
		depth := intArg(args, "depth", 0)
		format := strArg(args, "format")
		if format == "" {
			format = "tree"
		}

		var rawData map[string]any
		if nodeID != "" {
			rawData, _, err = client.GetRawNode(ctx, fileKey, nodeID, depth)
		} else {
			rawData, _, err = client.GetRawFile(ctx, fileKey, depth)
		}
		if err != nil {
			return "", err
		}
		simplified, err := extractors.SimplifyRawFigmaObject(rawData, extractors.Options{MaxDepth: depth})
		if err != nil {
			return "", err
		}
		return serialize.Serialize(simplified, format)

	case "get_screenshot":
		fileKey, nodeID, err := figma.ResolveTarget(rawURL, flagFileKey, flagNodeID)
		if err != nil {
			return "", err
		}
		if nodeID == "" {
			return "", fmt.Errorf("nodeId (or ?node-id= in url) is required for get_screenshot")
		}
		format := strings.ToLower(strArg(args, "format"))
		if format == "" {
			format = "png"
		}
		if format == "jpeg" {
			format = "jpg"
		}
		switch format {
		case "png", "jpg", "svg":
		default:
			return "", fmt.Errorf("unsupported screenshot format %q (allowed: png, jpg, svg)", format)
		}
		scale := floatArg(args, "scale", 2.0)

		renderURLs, err := client.GetNodeRenderURLs(ctx, fileKey, []string{nodeID}, format, figma.RenderOptions{
			PNGScale:   scale,
			SVGOptions: figma.DefaultSVGOptions(),
		})
		if err != nil {
			return "", err
		}
		normID := figma.NormalizeNodeID(nodeID)
		rURL := renderURLs[normID]
		if rURL == "" {
			return "", fmt.Errorf("empty render URL for node %s", normID)
		}

		outPath := strArg(args, "outputPath")
		safeID := figma.SanitizeNodeIDForFile(normID)
		defaultName := fmt.Sprintf("screenshot-%s-%s.%s", fileKey, safeID, format)
		if outPath == "" {
			outPath = filepath.Join(os.TempDir(), defaultName)
		} else {
			absOut, err := filepath.Abs(filepath.Clean(outPath))
			if err != nil {
				return "", err
			}
			if info, statErr := os.Stat(absOut); statErr == nil && info.IsDir() {
				outPath = filepath.Join(absOut, defaultName)
			} else {
				outPath = absOut
			}
		}

		if err := client.DownloadToFile(ctx, rURL, outPath); err != nil {
			return "", err
		}
		b, _ := json.MarshalIndent(map[string]any{
			"fileKey":  fileKey,
			"nodeId":   normID,
			"format":   format,
			"scale":    scale,
			"filePath": outPath,
		}, "", "  ")
		return string(b), nil

	case "get_tokens":
		fileKey, _, err := figma.ResolveTarget(rawURL, flagFileKey, "")
		if err != nil {
			return "", err
		}
		fmtType, err := tokens.ParseFormat(strArg(args, "format"))
		if err != nil {
			return "", err
		}
		meta, err := client.GetLocalVariables(ctx, fileKey)
		if err != nil {
			return "", err
		}
		exported, err := tokens.Export(meta, fmtType)
		if err != nil {
			return "", err
		}
		if outPath := strArg(args, "outputPath"); outPath != "" {
			absOut, err := filepath.Abs(filepath.Clean(outPath))
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(absOut, []byte(exported), 0o644); err != nil {
				return "", err
			}
		}
		return exported, nil

	case "get_motion_context":
		fileKey, nodeID, err := figma.ResolveTarget(rawURL, flagFileKey, flagNodeID)
		if err != nil {
			return "", err
		}
		fmtType, err := motion.ParseFormat(strArg(args, "format"))
		if err != nil {
			return "", err
		}
		var rawData map[string]any
		if nodeID != "" {
			rawData, _, err = client.GetRawNode(ctx, fileKey, nodeID, 0)
		} else {
			rawData, _, err = client.GetRawFile(ctx, fileKey, 0)
		}
		if err != nil {
			return "", err
		}
		report, err := motion.ExtractMotion(rawData)
		if err != nil {
			return "", err
		}
		return motion.Export(report, fmtType)

	case "download_assets":
		fileKey, nodeID, err := figma.ResolveTarget(rawURL, flagFileKey, flagNodeID)
		if err != nil {
			return "", err
		}
		targetDir, err := images.ResolveTargetDir(strArg(args, "dir"))
		if err != nil {
			return "", err
		}
		scale := floatArg(args, "scale", 2.0)

		var rawData map[string]any
		if nodeID != "" {
			rawData, _, err = client.GetRawNode(ctx, fileKey, nodeID, 0)
		} else {
			rawData, _, err = client.GetRawFile(ctx, fileKey, 0)
		}
		if err != nil {
			return "", err
		}
		simplified, err := extractors.SimplifyRawFigmaObject(rawData, extractors.Options{})
		if err != nil {
			return "", err
		}
		reqs := images.DiscoverDownloadableNodes(simplified)
		if len(reqs) == 0 {
			return fmt.Sprintf("No downloadable assets found in %s", fileKey), nil
		}
		results, err := images.DownloadFigmaImages(ctx, client, images.DownloadParams{
			FileKey:    fileKey,
			TargetDir:  targetDir,
			Nodes:      reqs,
			PNGScale:   scale,
			SVGOptions: figma.DefaultSVGOptions(),
		})
		if err != nil {
			return "", err
		}
		lines := make([]string, len(results))
		for i, r := range results {
			lines[i] = r.FormatLine()
		}
		return fmt.Sprintf("Downloaded %d assets to %s:\n%s", len(results), targetDir, strings.Join(lines, "\n")), nil

	case "code_connect":
		fileKey, nodeID, err := figma.ResolveTarget(rawURL, flagFileKey, flagNodeID)
		if err != nil {
			return "", err
		}
		dir := strArg(args, "dir")
		if dir == "" {
			dir = "."
		}
		locals, err := codeconnect.ScanLocalComponents(dir)
		if err != nil {
			return "", err
		}
		var rawData map[string]any
		if nodeID != "" {
			rawData, _, err = client.GetRawNode(ctx, fileKey, nodeID, 0)
		} else {
			rawData, _, err = client.GetRawFile(ctx, fileKey, 0)
		}
		if err != nil {
			return "", err
		}
		simplified, err := extractors.SimplifyRawFigmaObject(rawData, extractors.Options{})
		if err != nil {
			return "", err
		}
		matches := codeconnect.MatchComponents(simplified, locals, fileKey)
		var written []string
		if boolArg(args, "generate") {
			outDir := strArg(args, "outDir")
			if outDir == "" {
				outDir = dir
			}
			written, err = codeconnect.WriteTemplates(matches, fileKey, outDir)
			if err != nil {
				return "", err
			}
		}
		b, _ := json.MarshalIndent(map[string]any{
			"fileKey":        fileKey,
			"matches":        matches,
			"generatedFiles": written,
		}, "", "  ")
		return string(b), nil

	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (s *Server) toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "get_design_context",
			"description": "Fetch and simplify a Figma Design file or node subtree into token-optimized Tree DSL, YAML, or JSON (layout, Flexbox/Grid, typography, colors, gradients, components).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":     map[string]any{"type": "string", "description": "Full Figma Design URL (supports /design/, /file/, and /branch/ URLs)"},
					"fileKey": map[string]any{"type": "string", "description": "Figma fileKey (optional if url is provided)"},
					"nodeId":  map[string]any{"type": "string", "description": "Figma node ID, e.g. '12:34' or '12-34'"},
					"depth":   map[string]any{"type": "integer", "description": "Optional max traversal depth"},
					"format":  map[string]any{"type": "string", "description": "Output format: tree (default), yaml, or json"},
				},
			},
		},
		{
			"name":        "get_screenshot",
			"description": "Render and download a high-DPI PNG, JPG, or SVG visual screenshot of a Figma node to local disk for visual fidelity comparison.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":        map[string]any{"type": "string", "description": "Figma URL containing node-id"},
					"fileKey":    map[string]any{"type": "string", "description": "Figma fileKey"},
					"nodeId":     map[string]any{"type": "string", "description": "Target Figma node ID"},
					"scale":      map[string]any{"type": "number", "description": "Render scale factor (default: 2)"},
					"format":     map[string]any{"type": "string", "description": "png (default), jpg, or svg"},
					"outputPath": map[string]any{"type": "string", "description": "Optional destination file path"},
				},
			},
		},
		{
			"name":        "get_tokens",
			"description": "Extract Figma Variables (Design Tokens) into CSS variables (:root/.dark), Tailwind v3, Tailwind v4 (@theme), SwiftUI, W3C DTCG, or JSON.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":        map[string]any{"type": "string", "description": "Figma file URL"},
					"fileKey":    map[string]any{"type": "string", "description": "Figma fileKey"},
					"format":     map[string]any{"type": "string", "description": "css (default), tailwind, tailwind-v4, swiftui, dtcg, or json"},
					"outputPath": map[string]any{"type": "string", "description": "Optional file path to write exported tokens"},
				},
			},
		},
		{
			"name":        "get_motion_context",
			"description": "Extract Figma prototype transitions, animations, durations, and cubic-bezier easing curves into CSS @keyframes, Framer Motion (motion.dev), or JSON.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":     map[string]any{"type": "string", "description": "Figma file or node URL"},
					"fileKey": map[string]any{"type": "string", "description": "Figma fileKey"},
					"nodeId":  map[string]any{"type": "string", "description": "Optional Figma node ID"},
					"format":  map[string]any{"type": "string", "description": "css (default), framer, or json"},
				},
			},
		},
		{
			"name":        "download_assets",
			"description": "Auto-discover, download, and crop all SVG icons, PNG images, and GIFs from a Figma file or node subtree.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":     map[string]any{"type": "string", "description": "Figma file or node URL"},
					"fileKey": map[string]any{"type": "string", "description": "Figma fileKey"},
					"nodeId":  map[string]any{"type": "string", "description": "Optional Figma node ID"},
					"dir":     map[string]any{"type": "string", "description": "Target directory to save assets"},
					"scale":   map[string]any{"type": "number", "description": "PNG scale factor (default: 2)"},
				},
			},
		},
		{
			"name":        "code_connect",
			"description": "Scan local codebase components (.tsx, .jsx, .vue, .swift), match them to Figma components, and optionally generate parserless .figma.ts Code Connect files.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":      map[string]any{"type": "string", "description": "Figma file or node URL"},
					"fileKey":  map[string]any{"type": "string", "description": "Figma fileKey"},
					"nodeId":   map[string]any{"type": "string", "description": "Optional Figma node ID"},
					"dir":      map[string]any{"type": "string", "description": "Local source directory to scan (default: '.')"},
					"generate": map[string]any{"type": "boolean", "description": "Generate .figma.ts files for matched components"},
					"outDir":   map[string]any{"type": "string", "description": "Optional output directory for generated .figma.ts files"},
				},
			},
		},
	}
}
