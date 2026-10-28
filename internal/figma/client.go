package figma

import (
	"bytes"
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

// doRequest handles authenticated HTTP requests with secret redaction and error formatting.
func (c *Client) doRequest(ctx context.Context, method, endpoint string, body any) ([]byte, error) {
	fullURL := c.baseURL + endpoint
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body for %s: %w", endpoint, err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", endpoint, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := c.applyAuthHeaders(req); err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error requesting %s: %w", endpoint, err)
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
			return nil, fmt.Errorf("%s: %w", BuildRateLimitMessage(httpErr), httpErr)
		case http.StatusForbidden:
			return nil, fmt.Errorf("%s: %w", BuildForbiddenMessage(endpoint, httpErr, c.proxyMode), httpErr)
		default:
			return nil, fmt.Errorf("failed request to Figma API endpoint '%s': %w", endpoint, httpErr)
		}
	}
	if readErr != nil {
		return nil, fmt.Errorf("read response body from %s: %w", endpoint, readErr)
	}
	return bodyBytes, nil
}

// RequestJSON performs an authenticated GET request to the Figma API and returns the decoded JSON map and raw byte size.
func (c *Client) RequestJSON(ctx context.Context, endpoint string) (map[string]any, int, error) {
	bodyBytes, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
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

// VariableMode represents a mode in a Figma variable collection (e.g. Light / Dark).
type VariableMode struct {
	ModeID string `json:"modeId"`
	Name   string `json:"name"`
}

// VariableCollection represents a group of variables with shared modes.
type VariableCollection struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name"`
	Key                  string         `json:"key"`
	Modes                []VariableMode `json:"modes"`
	DefaultModeID        string         `json:"defaultModeId"`
	Remote               bool           `json:"remote"`
	HiddenFromPublishing bool           `json:"hiddenFromPublishing"`
	VariableIDs          []string       `json:"variableIds"`
}

// VariableCodeSyntax contains platform-specific code syntax representations.
type VariableCodeSyntax struct {
	Web     string `json:"WEB,omitempty"`
	Android string `json:"ANDROID,omitempty"`
	IOS     string `json:"iOS,omitempty"`
}

// Variable represents a Figma design token / variable definition.
type Variable struct {
	ID                   string              `json:"id"`
	Name                 string              `json:"name"`
	Key                  string              `json:"key"`
	VariableCollectionID string              `json:"variableCollectionId"`
	ResolvedType         string              `json:"resolvedType"` // "COLOR", "FLOAT", "STRING", "BOOLEAN"
	ValuesByMode         map[string]any      `json:"valuesByMode"`
	Remote               bool                `json:"remote"`
	Description          string              `json:"description"`
	HiddenFromPublishing bool                `json:"hiddenFromPublishing"`
	Scopes               []string            `json:"scopes"`
	CodeSyntax           *VariableCodeSyntax `json:"codeSyntax,omitempty"`
}

// VariablesMeta represents the meta container returned by Figma Variables API.
type VariablesMeta struct {
	Variables           map[string]Variable           `json:"variables"`
	VariableCollections map[string]VariableCollection `json:"variableCollections"`
}

// VariablesResponse is the top-level payload returned by GET /v1/files/:file_key/variables/local.
type VariablesResponse struct {
	Status int           `json:"status"`
	Error  bool          `json:"error"`
	Meta   VariablesMeta `json:"meta"`
}

// GetLocalVariables fetches all local variables and variable collections for a file.
func (c *Client) GetLocalVariables(ctx context.Context, fileKey string) (*VariablesMeta, error) {
	endpoint := fmt.Sprintf("/files/%s/variables/local", url.PathEscape(fileKey))
	bodyBytes, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var resp VariablesResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("decode variables from %s: %w", endpoint, err)
	}
	if resp.Error {
		return nil, fmt.Errorf("figma variables API returned error (status %d)", resp.Status)
	}
	return &resp.Meta, nil
}

// DevResource represents a code or external documentation link attached to a Figma node.
type DevResource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	FileKey string `json:"file_key"`
	NodeID  string `json:"node_id"`
}

// DevResourceInput represents payload parameters for creating a Dev Resource.
type DevResourceInput struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	FileKey string `json:"file_key"`
	NodeID  string `json:"node_id"`
}

type devResourcesPayload struct {
	DevResources []DevResourceInput `json:"dev_resources"`
}

type devResourcesResp struct {
	Links        []DevResource `json:"links"`
	DevResources []DevResource `json:"dev_resources"`
	Errors       []any         `json:"errors"`
}

// GetDevResources retrieves dev resources attached to the file or specific nodes.
func (c *Client) GetDevResources(ctx context.Context, fileKey string, nodeIDs []string) ([]DevResource, error) {
	q := url.Values{}
	if len(nodeIDs) > 0 {
		normalized := make([]string, 0, len(nodeIDs))
		for _, id := range nodeIDs {
			n := NormalizeNodeID(id)
			if n != "" {
				normalized = append(normalized, n)
			}
		}
		if len(normalized) > 0 {
			q.Set("node_ids", strings.Join(normalized, ","))
		}
	}

	endpoint := fmt.Sprintf("/files/%s/dev_resources", url.PathEscape(fileKey))
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	bodyBytes, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var resp devResourcesResp
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("decode dev resources: %w", err)
	}
	if len(resp.Links) > 0 {
		return resp.Links, nil
	}
	return resp.DevResources, nil
}

// CreateDevResources creates one or more dev resources attached to Figma nodes.
func (c *Client) CreateDevResources(ctx context.Context, fileKey string, resources []DevResourceInput) ([]DevResource, error) {
	endpoint := fmt.Sprintf("/files/%s/dev_resources", url.PathEscape(fileKey))
	normalizedResources := make([]DevResourceInput, len(resources))
	for i, r := range resources {
		fk := r.FileKey
		if fk == "" {
			fk = fileKey
		}
		normalizedResources[i] = DevResourceInput{
			Name:    r.Name,
			URL:     r.URL,
			FileKey: fk,
			NodeID:  NormalizeNodeID(r.NodeID),
		}
	}

	payload := devResourcesPayload{
		DevResources: normalizedResources,
	}

	bodyBytes, err := c.doRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return nil, err
	}

	var resp devResourcesResp
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("decode create dev resources response: %w", err)
	}
	if len(resp.Links) > 0 {
		return resp.Links, nil
	}
	return resp.DevResources, nil
}

// DeleteDevResource deletes a specific dev resource by ID.
func (c *Client) DeleteDevResource(ctx context.Context, fileKey, devResourceID string) error {
	endpoint := fmt.Sprintf("/files/%s/dev_resources/%s", url.PathEscape(fileKey), url.PathEscape(devResourceID))
	_, err := c.doRequest(ctx, http.MethodDelete, endpoint, nil)
	return err
}
