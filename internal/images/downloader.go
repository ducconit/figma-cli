package images

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ducconit/figma-cli/internal/extractors"
	"github.com/ducconit/figma-cli/internal/figma"
)

const maxConcurrentDownloads = 6

// DownloadNodeRequest represents a single requested image/icon asset to download.
type DownloadNodeRequest struct {
	NodeID                  string      `json:"nodeId" yaml:"nodeId"`
	ImageRef                string      `json:"imageRef,omitempty" yaml:"imageRef,omitempty"`
	GifRef                  string      `json:"gifRef,omitempty" yaml:"gifRef,omitempty"`
	FileName                string      `json:"fileName" yaml:"fileName"`
	FilenameSuffix          string      `json:"filenameSuffix,omitempty" yaml:"filenameSuffix,omitempty"`
	CropTransform           [][]float64 `json:"cropTransform,omitempty" yaml:"cropTransform,omitempty"`
	NeedsCropping           bool        `json:"needsCropping,omitempty" yaml:"needsCropping,omitempty"`
	RequiresImageDimensions bool        `json:"requiresImageDimensions,omitempty" yaml:"requiresImageDimensions,omitempty"`
}

// DownloadParams configures a batch download operation.
type DownloadParams struct {
	FileKey    string
	TargetDir  string
	Nodes      []DownloadNodeRequest
	PNGScale   float64
	SVGOptions figma.SVGOptions
}

// DownloadResult describes a completed file download and its post-processing metadata.
type DownloadResult struct {
	FileName     string     `json:"fileName" yaml:"fileName"`
	FullPath     string     `json:"fullPath" yaml:"fullPath"`
	CSSVariables string     `json:"cssVariables,omitempty" yaml:"cssVariables,omitempty"`
	Aliases      []string   `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Dimensions   Dimensions `json:"dimensions" yaml:"dimensions"`
	WasCropped   bool       `json:"wasCropped,omitempty" yaml:"wasCropped,omitempty"`
}

// FormatLine formats a DownloadResult matching Figma-Context-MCP output style.
func (r DownloadResult) FormatLine() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("- %s: %dx%d", r.FileName, r.Dimensions.Width, r.Dimensions.Height))
	if r.CSSVariables != "" {
		sb.WriteString(" | " + r.CSSVariables)
	}
	if r.WasCropped {
		sb.WriteString(" (cropped)")
	}
	if len(r.Aliases) > 0 {
		sb.WriteString(fmt.Sprintf(" (also requested as: %s)", strings.Join(r.Aliases, ", ")))
	}
	return sb.String()
}

// ParseQuickItem parses a shorthand "--item" flag string like:
//   "12:34=icon.svg" or "nodeId=12:34,file=hero.png,imageRef=abc123"
func ParseQuickItem(spec string) (DownloadNodeRequest, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return DownloadNodeRequest{}, fmt.Errorf("empty --item specification")
	}

	// Key-value comma-separated format: nodeId=12:34,file=icon.svg,imageRef=...
	if strings.Contains(spec, ",") || strings.HasPrefix(spec, "nodeId=") || strings.HasPrefix(spec, "imageRef=") {
		var req DownloadNodeRequest
		parts := strings.Split(spec, ",")
		for _, p := range parts {
			kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
			if len(kv) != 2 {
				return DownloadNodeRequest{}, fmt.Errorf("invalid key=value pair %q in --item %q", p, spec)
			}
			k, v := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
			switch k {
			case "nodeId", "node", "id":
				req.NodeID = figma.NormalizeNodeID(v)
			case "fileName", "file", "name":
				req.FileName = v
			case "imageRef", "ref":
				req.ImageRef = v
			case "gifRef", "gif":
				req.GifRef = v
			case "suffix", "filenameSuffix":
				req.FilenameSuffix = v
			default:
				return DownloadNodeRequest{}, fmt.Errorf("unknown key %q in --item %q", k, spec)
			}
		}
		if req.FileName == "" {
			return DownloadNodeRequest{}, fmt.Errorf("--item %q is missing file=<fileName>", spec)
		}
		if req.NodeID == "" && req.ImageRef == "" && req.GifRef == "" {
			return DownloadNodeRequest{}, fmt.Errorf("--item %q requires nodeId, imageRef, or gifRef", spec)
		}
		return req, nil
	}

	// Shorthand "<nodeId>=<fileName>"
	if idx := strings.IndexByte(spec, '='); idx > 0 {
		nodeID := figma.NormalizeNodeID(spec[:idx])
		fileName := strings.TrimSpace(spec[idx+1:])
		return DownloadNodeRequest{
			NodeID:   nodeID,
			FileName: fileName,
		}, nil
	}

	// Bare nodeID -> default to <sanitized-nodeId>.png
	nodeID := figma.NormalizeNodeID(spec)
	safeName := figma.SanitizeNodeIDForFile(nodeID) + ".png"
	return DownloadNodeRequest{
		NodeID:   nodeID,
		FileName: safeName,
	}, nil
}

// ParseManifest parses a JSON array of DownloadNodeRequest from either an inline JSON string or a JSON file path.
func ParseManifest(manifestArg string) ([]DownloadNodeRequest, error) {
	manifestArg = strings.TrimSpace(manifestArg)
	if manifestArg == "" {
		return nil, nil
	}
	var rawBytes []byte
	if strings.HasPrefix(manifestArg, "[") || strings.HasPrefix(manifestArg, "{") {
		rawBytes = []byte(manifestArg)
	} else {
		b, err := os.ReadFile(manifestArg)
		if err != nil {
			return nil, fmt.Errorf("read manifest file %q: %w", manifestArg, err)
		}
		rawBytes = b
	}

	if strings.HasPrefix(strings.TrimSpace(string(rawBytes)), "{") {
		var single DownloadNodeRequest
		if err := json.Unmarshal(rawBytes, &single); err != nil {
			return nil, fmt.Errorf("parse manifest JSON object: %w", err)
		}
		return []DownloadNodeRequest{single}, nil
	}

	var list []DownloadNodeRequest
	if err := json.Unmarshal(rawBytes, &list); err != nil {
		return nil, fmt.Errorf("parse manifest JSON array: %w", err)
	}
	return list, nil
}

// DiscoverTestableImageNodes walks a SimplifiedDesign and extracts all downloadable image/SVG nodes automatically
// when a user runs `fm download <url>` without explicit --item or --manifest flags.
func DiscoverDownloadableNodes(design *extractors.SimplifiedDesign) []DownloadNodeRequest {
	var out []DownloadNodeRequest
	seen := make(map[string]bool)

	resolveFills := func(fillsVal any) []any {
		switch v := fillsVal.(type) {
		case string:
			if arr, ok := design.GlobalVars.Styles[v].([]any); ok {
				return arr
			}
		case []any:
			return v
		}
		return nil
	}

	var walk func(nodes []*extractors.SimplifiedNode)
	walk = func(nodes []*extractors.SimplifiedNode) {
		for _, n := range nodes {
			effType := n.Type
			effFills := n.Fills
			if n.Template != "" && design.Elements != nil {
				if body, ok := design.Elements[n.Template]; ok {
					if effType == "" {
						effType, _ = body["type"].(string)
					}
					if effFills == nil {
						effFills = body["fills"]
					}
				}
			}

			safeID := figma.SanitizeNodeIDForFile(n.ID)
			if effType == "IMAGE-SVG" && n.ID != "" {
				key := "svg:" + n.ID
				if !seen[key] {
					seen[key] = true
					out = append(out, DownloadNodeRequest{
						NodeID:   n.ID,
						FileName: fmt.Sprintf("svg-%s.svg", safeID),
					})
				}
			}

			for idx, item := range resolveFills(effFills) {
				fm, ok := item.(map[string]any)
				if !ok {
					continue
				}
				fType, _ := fm["type"].(string)
				if fType == "IMAGE" {
					imgRef, _ := fm["imageRef"].(string)
					gifRef, _ := fm["gifRef"].(string)
					ext := "png"
					if gifRef != "" {
						ext = "gif"
					}
					baseFile := fmt.Sprintf("img-%s-%d.%s", safeID, idx, ext)
					if imgRef != "" {
						shortRef := imgRef
						if len(shortRef) > 8 {
							shortRef = shortRef[:8]
						}
						baseFile = fmt.Sprintf("img-%s.%s", shortRef, ext)
					}
					req := DownloadNodeRequest{
						NodeID:   n.ID,
						ImageRef: imgRef,
						GifRef:   gifRef,
						FileName: baseFile,
					}
					if dlArgs, ok := fm["imageDownloadArguments"].(map[string]any); ok {
						req.NeedsCropping, _ = dlArgs["needsCropping"].(bool)
						req.RequiresImageDimensions, _ = dlArgs["requiresImageDimensions"].(bool)
						req.FilenameSuffix, _ = dlArgs["filenameSuffix"].(string)
						if ct, ok := dlArgs["cropTransform"].([][]float64); ok {
							req.CropTransform = ct
						}
					}
					key := fmt.Sprintf("img:%s:%s:%s:%s", n.ID, imgRef, gifRef, req.FilenameSuffix)
					if !seen[key] {
						seen[key] = true
						out = append(out, req)
					}
				} else if fType == "PATTERN" {
					if ps, ok := fm["patternSource"].(map[string]any); ok {
						if srcNodeID, _ := ps["nodeId"].(string); srcNodeID != "" {
							safeSrc := figma.SanitizeNodeIDForFile(srcNodeID)
							key := "pat:" + srcNodeID
							if !seen[key] {
								seen[key] = true
								out = append(out, DownloadNodeRequest{
									NodeID:   srcNodeID,
									FileName: fmt.Sprintf("pattern-%s.png", safeSrc),
								})
							}
						}
					}
				}
			}

			if len(n.Children) > 0 {
				walk(n.Children)
			}
		}
	}
	walk(design.Nodes)
	return out
}

// DownloadFigmaImages resolves image/render URLs, deduplicates identical static imageRefs,
// downloads files to TargetDir, and runs pure-Go cropping + dimension extraction.
func DownloadFigmaImages(ctx context.Context, client *figma.Client, params DownloadParams) ([]DownloadResult, error) {
	targetDir, err := ResolveTargetDir(params.TargetDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("create target directory %s: %w", targetDir, err)
	}

	var downloadItems []DownloadNodeRequest
	seenDownloads := make(map[string]int)
	downloadToAliases := make(map[int][]string)

	for _, rawReq := range params.Nodes {
		req := rawReq
		req.NodeID = figma.NormalizeNodeID(req.NodeID)
		if err := ValidateFilenameSuffix(req.FilenameSuffix); err != nil {
			return nil, err
		}
		req.FileName = ApplyFilenameSuffix(req.FileName, req.FilenameSuffix)
		if err := ValidateFileName(req.FileName); err != nil {
			return nil, err
		}

		if req.GifRef != "" {
			idx := len(downloadItems)
			downloadItems = append(downloadItems, req)
			downloadToAliases[idx] = []string{req.FileName}
			continue
		}

		if req.ImageRef != "" {
			uniqueKey := fmt.Sprintf("%s-%s", req.ImageRef, nonEmptyOr(req.FilenameSuffix, "none"))
			if req.FilenameSuffix == "" {
				if existingIdx, seen := seenDownloads[uniqueKey]; seen {
					downloadToAliases[existingIdx] = append(downloadToAliases[existingIdx], req.FileName)
					if req.RequiresImageDimensions {
						downloadItems[existingIdx].RequiresImageDimensions = true
					}
					continue
				}
			}
			idx := len(downloadItems)
			downloadItems = append(downloadItems, req)
			seenDownloads[uniqueKey] = idx
			downloadToAliases[idx] = []string{req.FileName}
			continue
		}

		// Rendered node via nodeId
		if req.NodeID == "" {
			return nil, fmt.Errorf("download item %q requires nodeId, imageRef, or gifRef", req.FileName)
		}
		idx := len(downloadItems)
		downloadItems = append(downloadItems, req)
		downloadToAliases[idx] = []string{req.FileName}
	}

	if len(downloadItems) == 0 {
		return nil, nil
	}

	type downloadSpec struct {
		index    int
		reqItem  DownloadNodeRequest
		destPath string
		aliases  []string
	}
	type downloadTask struct {
		spec   downloadSpec
		urlStr string
	}
	type indexedResult struct {
		index int
		res   DownloadResult
	}

	var fillSpecs, svgSpecs, pngSpecs []downloadSpec
	var svgNodeIDs, pngNodeIDs []string

	for idx, item := range downloadItems {
		dst, err := ResolveSafeFilePath(targetDir, item.FileName)
		if err != nil {
			return nil, err
		}
		var otherNames []string
		for _, alias := range downloadToAliases[idx] {
			if alias != item.FileName {
				otherNames = append(otherNames, alias)
			}
		}
		spec := downloadSpec{
			index:    idx,
			reqItem:  item,
			destPath: dst,
			aliases:  otherNames,
		}

		if item.GifRef != "" || item.ImageRef != "" {
			fillSpecs = append(fillSpecs, spec)
		} else if strings.HasSuffix(strings.ToLower(item.FileName), ".svg") {
			svgSpecs = append(svgSpecs, spec)
			svgNodeIDs = append(svgNodeIDs, item.NodeID)
		} else {
			pngSpecs = append(pngSpecs, spec)
			pngNodeIDs = append(pngNodeIDs, item.NodeID)
		}
	}

	dlCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	var firstErr atomic.Pointer[error]
	recordErr := func(err error) {
		if err == nil {
			return
		}
		e := err
		if firstErr.CompareAndSwap(nil, &e) {
			cancel(e)
		}
	}

	taskCh := make(chan downloadTask, len(downloadItems))
	resultCh := make(chan indexedResult, len(downloadItems))
	numWorkers := maxConcurrentDownloads
	if len(downloadItems) < numWorkers {
		numWorkers = len(downloadItems)
	}

	// Stage 2: Worker pool consuming self-contained download tasks from taskCh and emitting to resultCh
	var workerWG sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for task := range taskCh {
				if dlCtx.Err() != nil {
					return
				}
				sp := task.spec
				if err := client.DownloadToFile(dlCtx, task.urlStr, sp.destPath); err != nil {
					recordErr(err)
					return
				}

				dims, wasCropped, err := ProcessDownloadedImage(sp.destPath, sp.reqItem.NeedsCropping, sp.reqItem.CropTransform)
				if err != nil {
					recordErr(err)
					return
				}

				cssVars := ""
				if sp.reqItem.RequiresImageDimensions && dims.Width > 0 && dims.Height > 0 {
					cssVars = GenerateImageCSSVariables(dims)
				}

				resultCh <- indexedResult{
					index: sp.index,
					res: DownloadResult{
						FileName:     sp.reqItem.FileName,
						FullPath:     sp.destPath,
						Dimensions:   dims,
						WasCropped:   wasCropped,
						CSSVariables: cssVars,
						Aliases:      sp.aliases,
					},
				}
			}
		}()
	}

	// Stage 1: Concurrent API URL producers (fills, SVGs, PNGs) streaming into taskCh
	renderOpts := figma.RenderOptions{
		PNGScale:   params.PNGScale,
		SVGOptions: params.SVGOptions,
	}

	var producerWG sync.WaitGroup
	enqueueResolved := func(specs []downloadSpec, urlMap map[string]string, isFill bool) {
		for _, sp := range specs {
			var dlURL, refKey string
			if isFill {
				refKey = sp.reqItem.GifRef
				if refKey == "" {
					refKey = sp.reqItem.ImageRef
				}
				dlURL = urlMap[refKey]
			} else {
				dlURL = urlMap[sp.reqItem.NodeID]
			}
			if dlURL == "" {
				recordErr(fmt.Errorf("Figma API did not return a download URL for %s (nodeId=%s, imageRef=%s)", sp.reqItem.FileName, sp.reqItem.NodeID, refKey))
				return
			}
			select {
			case taskCh <- downloadTask{spec: sp, urlStr: dlURL}:
			case <-dlCtx.Done():
				return
			}
		}
	}

	if len(fillSpecs) > 0 {
		producerWG.Add(1)
		go func(specs []downloadSpec) {
			defer producerWG.Done()
			fillURLs, err := client.GetImageFillURLs(dlCtx, params.FileKey)
			if err != nil {
				recordErr(err)
				return
			}
			enqueueResolved(specs, fillURLs, true)
		}(fillSpecs)
	}

	if len(svgSpecs) > 0 {
		producerWG.Add(1)
		go func(specs []downloadSpec, ids []string) {
			defer producerWG.Done()
			svgURLs, err := client.GetNodeRenderURLs(dlCtx, params.FileKey, ids, "svg", renderOpts)
			if err != nil {
				recordErr(err)
				return
			}
			enqueueResolved(specs, svgURLs, false)
		}(svgSpecs, svgNodeIDs)
	}

	if len(pngSpecs) > 0 {
		producerWG.Add(1)
		go func(specs []downloadSpec, ids []string) {
			defer producerWG.Done()
			pngURLs, err := client.GetNodeRenderURLs(dlCtx, params.FileKey, ids, "png", renderOpts)
			if err != nil {
				recordErr(err)
				return
			}
			enqueueResolved(specs, pngURLs, false)
		}(pngSpecs, pngNodeIDs)
	}

	producerWG.Wait()
	close(taskCh)
	workerWG.Wait()
	close(resultCh)

	if ep := firstErr.Load(); ep != nil {
		return nil, *ep
	}

	results := make([]DownloadResult, len(downloadItems))
	for item := range resultCh {
		results[item.index] = item.res
	}

	return results, nil
}

func nonEmptyOr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}
