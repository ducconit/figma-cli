package figma

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ducconit/figma-cli/internal/config"
)

const (
	defaultBaseURL            = "https://api.figma.com/v1"
	renderBatchSize           = 50
	maxRenderBatchConcurrency = 4
)

// SVGOptions configures SVG rendering query parameters on /v1/images/:key.
type SVGOptions struct {
	OutlineText    bool
	IncludeID      bool
	SimplifyStroke bool
}

// DefaultSVGOptions returns Figma-Context-MCP default SVG export flags.
func DefaultSVGOptions() SVGOptions {
	return SVGOptions{
		OutlineText:    true,
		IncludeID:      false,
		SimplifyStroke: true,
	}
}

// RenderOptions holds options for rendering PNG or SVG nodes.
type RenderOptions struct {
	PNGScale   float64
	SVGOptions SVGOptions
}

// Client is a Figma REST API v1 client with proxy and dual-auth support.
type Client struct {
	httpClient *http.Client
	baseURL    string
	proxyMode  string
	auth       config.AuthConfig
}

// NewClient constructs a Figma API client with optional explicit proxy or environment proxy support.
func NewClient(auth config.AuthConfig, explicitProxy string) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxyMode := "none"

	explicitProxy = strings.TrimSpace(explicitProxy)
	if explicitProxy == "" {
		explicitProxy = strings.TrimSpace(os.Getenv("FIGMA_PROXY"))
	}

	switch {
	case strings.EqualFold(explicitProxy, "none"):
		transport.Proxy = nil
		proxyMode = "none"
	case explicitProxy != "":
		proxyURL, err := url.Parse(explicitProxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", explicitProxy, err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
		proxyMode = "explicit"
	case hasProxyEnv():
		transport.Proxy = http.ProxyFromEnvironment
		proxyMode = "env"
	default:
		transport.Proxy = nil
	}

	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second

	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   90 * time.Second,
		},
		baseURL:   defaultBaseURL,
		proxyMode: proxyMode,
		auth:      auth,
	}, nil
}

// SetBaseURL overrides the API base URL (used in tests).
func (c *Client) SetBaseURL(u string) {
	c.baseURL = strings.TrimRight(u, "/")
}

func hasProxyEnv() bool {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

func (c *Client) applyAuthHeaders(req *http.Request) error {
	if err := c.auth.Validate(); err != nil {
		return err
	}
	if c.auth.UseOAuth && c.auth.OAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.auth.OAuthToken)
		return nil
	}
	req.Header.Set("X-Figma-Token", c.auth.APIKey)
	return nil
}

// RequestJSON performs an authenticated GET request to the Figma API and returns the decoded JSON map and raw byte size.
func (c *Client) RequestJSON(ctx context.Context, endpoint string) (map[string]any, int, error) {
	fullURL := c.baseURL + endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create request for %s: %w", endpoint, err)
	}
	if err := c.applyAuthHeaders(req); err != nil {
		return nil, 0, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("network error requesting %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyStr := ""
		if readErr == nil {
			bodyStr = SanitizeAndRedactBody(string(bodyBytes), c.auth.Secrets())
		}
		httpErr := &HTTPError{
			StatusCode: resp.StatusCode,
			StatusText: http.StatusText(resp.StatusCode),
			Headers:    ExtractLowerHeaders(resp.Header),
			Body:       bodyStr,
		}
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			return nil, 0, fmt.Errorf("%s: %w", BuildRateLimitMessage(httpErr), httpErr)
		case http.StatusForbidden:
			return nil, 0, fmt.Errorf("%s: %w", BuildForbiddenMessage(endpoint, httpErr, c.proxyMode), httpErr)
		default:
			return nil, 0, fmt.Errorf("failed request to Figma API endpoint '%s': %w", endpoint, httpErr)
		}
	}
	if readErr != nil {
		return nil, 0, fmt.Errorf("read response body from %s: %w", endpoint, readErr)
	}

	var decoded map[string]any
	if err := json.Unmarshal(bodyBytes, &decoded); err != nil {
		return nil, len(bodyBytes), fmt.Errorf("decode JSON from %s: %w", endpoint, err)
	}
	return decoded, len(bodyBytes), nil
}

// GetRawFile fetches the entire Figma file tree from /v1/files/:fileKey.
func (c *Client) GetRawFile(ctx context.Context, fileKey string, depth int) (map[string]any, int, error) {
	endpoint := fmt.Sprintf("/files/%s", url.PathEscape(fileKey))
	if depth > 0 {
		endpoint += fmt.Sprintf("?depth=%d", depth)
	}
	return c.RequestJSON(ctx, endpoint)
}

// GetRawNode fetches specific node subtree(s) from /v1/files/:fileKey/nodes.
func (c *Client) GetRawNode(ctx context.Context, fileKey, nodeID string, depth int) (map[string]any, int, error) {
	q := url.Values{}
	q.Set("ids", NormalizeNodeID(nodeID))
	if depth > 0 {
		q.Set("depth", fmt.Sprintf("%d", depth))
	}
	endpoint := fmt.Sprintf("/files/%s/nodes?%s", url.PathEscape(fileKey), q.Encode())
	return c.RequestJSON(ctx, endpoint)
}

// GetImageFillURLs fetches download URLs for all image/gif fills in a file from /v1/files/:fileKey/images.
func (c *Client) GetImageFillURLs(ctx context.Context, fileKey string) (map[string]string, error) {
	endpoint := fmt.Sprintf("/files/%s/images", url.PathEscape(fileKey))
	data, _, err := c.RequestJSON(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	meta, ok := data["meta"].(map[string]any)
	if !ok {
		return out, nil
	}
	images, ok := meta["images"].(map[string]any)
	if !ok {
		return out, nil
	}
	for k, v := range images {
		if s, ok := v.(string); ok && s != "" {
			out[k] = s
		}
	}
	return out, nil
}

// GetNodeRenderURLs fetches rendered image URLs for specific node IDs from /v1/images/:fileKey.
// Deduplicates node IDs and fetches large node lists concurrently in bounded batches.
func (c *Client) GetNodeRenderURLs(ctx context.Context, fileKey string, nodeIDs []string, format string, opts RenderOptions) (map[string]string, error) {
	if len(nodeIDs) == 0 {
		return map[string]string{}, nil
	}
	seen := make(map[string]struct{}, len(nodeIDs))
	uniqueIDs := make([]string, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		norm := NormalizeNodeID(id)
		if norm == "" {
			continue
		}
		if _, exists := seen[norm]; !exists {
			seen[norm] = struct{}{}
			uniqueIDs = append(uniqueIDs, norm)
		}
	}
	if len(uniqueIDs) == 0 {
		return map[string]string{}, nil
	}

	if len(uniqueIDs) <= renderBatchSize {
		return c.fetchRenderURLBatch(ctx, fileKey, uniqueIDs, format, opts)
	}

	var batches [][]string
	for start := 0; start < len(uniqueIDs); start += renderBatchSize {
		end := start + renderBatchSize
		if end > len(uniqueIDs) {
			end = len(uniqueIDs)
		}
		batches = append(batches, uniqueIDs[start:end])
	}

	batchCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	sem := make(chan struct{}, maxRenderBatchConcurrency)
	resCh := make(chan map[string]string, len(batches))
	var wg sync.WaitGroup
	var firstErr atomic.Pointer[error]

	for _, batch := range batches {
		wg.Add(1)
		go func(ids []string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-batchCtx.Done():
				return
			}

			part, err := c.fetchRenderURLBatch(batchCtx, fileKey, ids, format, opts)
			if err != nil {
				e := err
				if firstErr.CompareAndSwap(nil, &e) {
					cancel(e)
				}
				return
			}
			resCh <- part
		}(batch)
	}

	wg.Wait()
	close(resCh)

	if ep := firstErr.Load(); ep != nil {
		return nil, *ep
	}

	out := make(map[string]string, len(uniqueIDs))
	for part := range resCh {
		for k, v := range part {
			out[k] = v
		}
	}
	return out, nil
}

func (c *Client) fetchRenderURLBatch(ctx context.Context, fileKey string, normalizedIDs []string, format string, opts RenderOptions) (map[string]string, error) {
	q := url.Values{}
	q.Set("ids", strings.Join(normalizedIDs, ","))
	q.Set("format", format)

	if format == "svg" {
		q.Set("svg_outline_text", fmt.Sprintf("%t", opts.SVGOptions.OutlineText))
		q.Set("svg_include_id", fmt.Sprintf("%t", opts.SVGOptions.IncludeID))
		q.Set("svg_simplify_stroke", fmt.Sprintf("%t", opts.SVGOptions.SimplifyStroke))
	} else {
		scale := opts.PNGScale
		if scale <= 0 {
			scale = 2
		}
		q.Set("scale", fmt.Sprintf("%g", scale))
	}

	endpoint := fmt.Sprintf("/images/%s?%s", url.PathEscape(fileKey), q.Encode())
	data, _, err := c.RequestJSON(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, len(normalizedIDs))
	images, ok := data["images"].(map[string]any)
	if !ok {
		return out, nil
	}
	for k, v := range images {
		if s, ok := v.(string); ok && s != "" {
			out[k] = s
		}
	}
	return out, nil
}

// DownloadToFile streams an image URL directly to destPath, cleaning up partial files on failure.
func (c *Client) DownloadToFile(ctx context.Context, imageURL, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", destPath, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return fmt.Errorf("build image download request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download image from %s: %w", imageURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("failed to download image (status %d)", resp.StatusCode)
	}

	tmpPath := destPath + ".part"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create file %s: %w", destPath, err)
	}

	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		return errors.Join(copyErr, closeErr)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("finalize file %s: %w", destPath, err)
	}
	return nil
}
