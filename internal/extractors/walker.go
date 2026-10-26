package extractors

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ducconit/figma-cli/internal/transformers"
)

const svgCollapseAutoLayoutThreshold = 10

var (
	collapsibleContainerTypes = map[string]bool{
		"FRAME":             true,
		"GROUP":             true,
		"INSTANCE":          true,
		"BOOLEAN_OPERATION": true,
	}
	svgEligibleTypes = map[string]bool{
		"IMAGE-SVG":         true,
		"BOOLEAN_OPERATION": true,
		"STAR":              true,
		"LINE":              true,
		"ELLIPSE":           true,
		"REGULAR_POLYGON":   true,
		"RECTANGLE":         true,
	}
)

type traversalState struct {
	mu             sync.Mutex
	globalVars     *GlobalVars
	extraStyles    map[string]map[string]any
	styleCache     map[string]string
	inlineCache    map[string]string
	namedStyleKeys map[string]bool
	compPropDefs   map[string]map[string]any
	tsCounter      atomic.Int64
	workerSem      chan struct{}
}

type traversalContext struct {
	*traversalState
	parent             map[string]any
	currentDepth       int
	insideComponentDef bool
}

// SimplifyRawFigmaObject parses either a GetFileResponse or GetFileNodesResponse map,
// walks the node tree (parallelizing top-level subtrees across worker goroutines),
// collapses vector containers into IMAGE-SVG, and runs FinalizeDesign + component simplification concurrently.
func SimplifyRawFigmaObject(raw map[string]any, opts Options) (*SimplifiedDesign, error) {
	if raw == nil {
		return nil, fmt.Errorf("empty Figma API response")
	}

	name, rawNodes, extraStyles, rawComponents, rawComponentSets, err := parseAPIResponse(raw)
	if err != nil {
		return nil, err
	}

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers < 2 {
		numWorkers = 2
	}

	state := &traversalState{
		globalVars:     &GlobalVars{Styles: make(map[string]any)},
		extraStyles:    extraStyles,
		styleCache:     make(map[string]string),
		inlineCache:    make(map[string]string),
		namedStyleKeys: make(map[string]bool),
		compPropDefs:   make(map[string]map[string]any),
		workerSem:      make(chan struct{}, numWorkers),
	}
	ctx := &traversalContext{
		traversalState: state,
	}

	extractedNodes := walkNodes(rawNodes, ctx, opts)

	type finalizeResult struct {
		nodes      []*SimplifiedNode
		globalVars *GlobalVars
		elements   map[string]ElementBody
	}
	finCh := make(chan finalizeResult, 1)
	compCh := make(chan map[string]SimplifiedComponentDefinition, 1)
	compSetCh := make(chan map[string]SimplifiedComponentSetDefinition, 1)

	go func() {
		fn, fg, el := FinalizeDesign(extractedNodes, state.globalVars, state.namedStyleKeys)
		finCh <- finalizeResult{nodes: fn, globalVars: fg, elements: el}
	}()
	go func() {
		compCh <- simplifyComponents(rawComponents, state.compPropDefs)
	}()
	go func() {
		compSetCh <- simplifyComponentSets(rawComponentSets, state.compPropDefs)
	}()

	fin := <-finCh
	components := <-compCh
	componentSets := <-compSetCh

	return &SimplifiedDesign{
		Name:          name,
		Nodes:         fin.nodes,
		Components:    components,
		ComponentSets: componentSets,
		GlobalVars:    *fin.globalVars,
		Elements:      fin.elements,
	}, nil
}

func parseAPIResponse(data map[string]any) (
	name string,
	rawNodes []map[string]any,
	extraStyles map[string]map[string]any,
	components map[string]map[string]any,
	componentSets map[string]map[string]any,
	err error,
) {
	name, _ = data["name"].(string)
	extraStyles = make(map[string]map[string]any)
	components = make(map[string]map[string]any)
	componentSets = make(map[string]map[string]any)

	if nodesMap, hasNodes := data["nodes"].(map[string]any); hasNodes {
		if len(nodesMap) == 0 {
			return "", nil, nil, nil, nil, fmt.Errorf("Figma API returned an empty nodes map")
		}
		nodeIDs := make([]string, 0, len(nodesMap))
		for nodeID := range nodesMap {
			nodeIDs = append(nodeIDs, nodeID)
		}
		slices.Sort(nodeIDs)

		for _, nodeID := range nodeIDs {
			entry := nodesMap[nodeID]
			if entry == nil {
				return "", nil, nil, nil, nil, fmt.Errorf(
					"node %s not found in Figma file (ensure URL is not /proto/, /figjam/, or /slides/, and check branch fileKey)",
					nodeID,
				)
			}
			nodeData, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			mergeNestedMaps(components, nodeData["components"])
			mergeNestedMaps(componentSets, nodeData["componentSets"])
			mergeNestedMaps(extraStyles, nodeData["styles"])
			if doc, ok := nodeData["document"].(map[string]any); ok {
				rawNodes = append(rawNodes, doc)
			}
		}
		return name, rawNodes, extraStyles, components, componentSets, nil
	}

	mergeNestedMaps(components, data["components"])
	mergeNestedMaps(componentSets, data["componentSets"])
	mergeNestedMaps(extraStyles, data["styles"])

	if doc, ok := data["document"].(map[string]any); ok {
		rawNodes = getSliceOfMaps(doc["children"])
	}
	return name, rawNodes, extraStyles, components, componentSets, nil
}

func walkNodes(nodes []map[string]any, ctx *traversalContext, opts Options) []*SimplifiedNode {
	if len(nodes) == 0 {
		return nil
	}
	// Parallelize sibling subtree extraction at top levels via buffered channel
	if len(nodes) >= 2 && ctx.currentDepth <= 1 && ctx.workerSem != nil {
		type indexedNode struct {
			index int
			node  *SimplifiedNode
		}
		nodeCh := make(chan indexedNode, len(nodes))
		var wg sync.WaitGroup
		for i, n := range nodes {
			select {
			case ctx.workerSem <- struct{}{}:
				wg.Add(1)
				go func(idx int, rawNode map[string]any, branchCtx traversalContext) {
					defer wg.Done()
					defer func() { <-ctx.workerSem }()
					nodeCh <- indexedNode{index: idx, node: processNode(rawNode, &branchCtx, opts)}
				}(i, n, *ctx)
			default:
				nodeCh <- indexedNode{index: i, node: processNode(n, ctx, opts)}
			}
		}
		wg.Wait()
		close(nodeCh)

		ordered := make([]*SimplifiedNode, len(nodes))
		for item := range nodeCh {
			ordered[item.index] = item.node
		}

		out := make([]*SimplifiedNode, 0, len(nodes))
		for _, sn := range ordered {
			if sn != nil {
				out = append(out, sn)
			}
		}
		return out
	}

	out := make([]*SimplifiedNode, 0, len(nodes))
	for _, n := range nodes {
		if sn := processNode(n, ctx, opts); sn != nil {
			out = append(out, sn)
		}
	}
	return out
}

func processNode(node map[string]any, ctx *traversalContext, opts Options) *SimplifiedNode {
	if !shouldProcessNode(node, ctx, opts) {
		return nil
	}

	nodeID, _ := node["id"].(string)
	nodeName, _ := node["name"].(string)
	rawType, _ := node["type"].(string)
	nodeType := rawType
	if rawType == "VECTOR" {
		nodeType = "IMAGE-SVG"
	}

	result := &SimplifiedNode{
		ID:   nodeID,
		Name: nodeName,
		Type: nodeType,
	}

	// Run the 4 built-in extractors: layout, text, visuals, component
	extractLayout(node, result, ctx)
	extractText(node, result, ctx)
	extractVisuals(node, result, ctx)
	extractComponent(node, result, ctx)

	// Traverse children if within maxDepth
	if opts.MaxDepth <= 0 || ctx.currentDepth < opts.MaxDepth {
		rawChildren := transformers.ComputeGridChildOrder(node)
		if len(rawChildren) > 0 {
			nextInsideComp := ctx.insideComponentDef
			switch rawType {
			case "COMPONENT", "COMPONENT_SET":
				nextInsideComp = true
			case "INSTANCE":
				nextInsideComp = false
			}

			childCtx := *ctx
			childCtx.parent = node
			childCtx.currentDepth = ctx.currentDepth + 1
			childCtx.insideComponentDef = nextInsideComp

			children := walkNodes(rawChildren, &childCtx, opts)

			// Bottom-up SVG container collapse hook
			children = CollapseSvgContainers(node, result, children)
			if len(children) > 0 {
				result.Children = children
			}
		}
	}

	return result
}

func shouldProcessNode(node map[string]any, ctx *traversalContext, opts Options) bool {
	if node == nil {
		return false
	}
	vis := true
	if v, ok := node["visible"].(bool); ok {
		vis = v
	}
	if !vis {
		// Rescue hidden nodes inside COMPONENT/COMPONENT_SET when controlled by a boolean "visible" property
		hasVisibleRef := false
		if refs, ok := node["componentPropertyReferences"].(map[string]any); ok {
			if _, exists := refs["visible"]; exists {
				hasVisibleRef = true
			}
		}
		if !(hasVisibleRef && ctx.insideComponentDef) {
			return false
		}
	}
	if opts.NodeFilter != nil && !opts.NodeFilter(node) {
		return false
	}
	return true
}

// CollapseSvgContainers collapses FRAME, GROUP, INSTANCE, or BOOLEAN_OPERATION containers
// whose children are 100% SVG-eligible primitives (without bitmap IMAGE fills) into a single IMAGE-SVG leaf.
func CollapseSvgContainers(rawNode map[string]any, result *SimplifiedNode, children []*SimplifiedNode) []*SimplifiedNode {
	if len(children) == 0 {
		return children
	}
	rawType, _ := rawNode["type"].(string)
	if !collapsibleContainerTypes[rawType] {
		return children
	}
	for _, ch := range children {
		if !svgEligibleTypes[ch.Type] {
			return children
		}
	}
	if hasImageFillOnSelfOrDirectChildren(rawNode) {
		return children
	}
	layoutMode := transformers.LayoutModeToSchema(rawNode)
	if layoutMode != "none" && len(children) < svgCollapseAutoLayoutThreshold {
		return children
	}

	result.Type = "IMAGE-SVG"
	return nil
}

func hasImageFillOnSelfOrDirectChildren(node map[string]any) bool {
	if nodeHasImageFill(node) {
		return true
	}
	for _, ch := range getSliceOfMaps(node["children"]) {
		if nodeHasImageFill(ch) {
			return true
		}
	}
	return false
}

func nodeHasImageFill(n map[string]any) bool {
	for _, f := range getSliceOfMaps(n["fills"]) {
		if vis, ok := f["visible"].(bool); ok && !vis {
			continue
		}
		if t, _ := f["type"].(string); t == "IMAGE" {
			return true
		}
	}
	return false
}

func extractLayout(node map[string]any, result *SimplifiedNode, ctx *traversalContext) {
	layout := transformers.BuildSimplifiedLayout(node, ctx.parent)
	if m := layout.ToMap(); m != nil {
		result.Layout = findOrCreateVar(ctx, m, "layout")
	}
}

func extractText(node map[string]any, result *SimplifiedNode, ctx *traversalContext) {
	rawType, _ := node["type"].(string)
	if rawType == "TEXT" {
		txt, boldW := transformers.BuildFormattedText(node, func(delta map[string]any) string {
			return registerInlineTextStyle(ctx, delta)
		})
		if txt != "" {
			result.Text = txt
		}
		if boldW > 0 {
			result.BoldWeight = &boldW
		}
	}
	if ts := transformers.ExtractTextStyle(node); ts != nil {
		result.TextStyle = registerStyle(node, ctx, ts, []string{"text", "typography"}, "style")
	}
}

func extractVisuals(node map[string]any, result *SimplifiedNode, ctx *traversalContext) {
	rawChildren := getSliceOfMaps(node["children"])
	hasChildren := len(rawChildren) > 0

	// 1. Fills
	var visibleFills []map[string]any
	for _, f := range getSliceOfMaps(node["fills"]) {
		if vis, ok := f["visible"].(bool); !ok || vis {
			visibleFills = append(visibleFills, f)
		}
	}
	if len(visibleFills) > 0 {
		var fillsValue []any
		if flatColor, ok := transformers.FlattenSolidFills(visibleFills); ok {
			fillsValue = []any{flatColor}
		} else {
			for _, f := range visibleFills {
				if pf, ok := transformers.ParsePaint(f, hasChildren); ok {
					fillsValue = append(fillsValue, pf.ToSerializable())
				}
			}
			slices.Reverse(fillsValue)
		}
		if len(fillsValue) > 0 {
			result.Fills = registerStyle(node, ctx, fillsValue, []string{"fill", "fills"}, "fill")
		}
	}

	// 2. Strokes
	strokes := transformers.BuildSimplifiedStrokes(node, hasChildren)
	if len(strokes.Colors) > 0 {
		result.Strokes = registerStyle(node, ctx, strokes.Colors, []string{"stroke", "strokes"}, "fill")
		result.StrokeWeight = strokes.StrokeWeight
		result.StrokeWeights = strokes.StrokeWeights
		result.StrokeDashes = strokes.StrokeDashes
		result.StrokeAlign = strokes.StrokeAlign
	}

	// 3. Effects
	effects := transformers.BuildSimplifiedEffects(node)
	if !effects.IsEmpty() {
		result.Effects = registerStyle(node, ctx, effects.ToMap(), []string{"effect", "effects"}, "effect")
	}

	// 4. Opacity
	if op, ok := node["opacity"].(float64); ok && op < 1.0 {
		rounded := transformers.PixelRound(op)
		result.Opacity = &rounded
	}

	// 5. Border Radius
	if cr, ok := node["cornerRadius"].(float64); ok && cr > 0 {
		result.BorderRadius = fmt.Sprintf("%gpx", transformers.PixelRound(cr))
	} else if radii, ok := node["rectangleCornerRadii"].([]any); ok && len(radii) == 4 {
		var vals [4]float64
		hasNonZero := false
		for i := 0; i < 4; i++ {
			if v, ok := radii[i].(float64); ok {
				vals[i] = transformers.PixelRound(v)
				if vals[i] > 0 {
					hasNonZero = true
				}
			}
		}
		if hasNonZero {
			result.BorderRadius = fmt.Sprintf("%gpx %gpx %gpx %gpx", vals[0], vals[1], vals[2], vals[3])
		}
	}
}

func extractComponent(node map[string]any, result *SimplifiedNode, ctx *traversalContext) {
	rawType, _ := node["type"].(string)
	if rawType == "INSTANCE" {
		if cid, ok := node["componentId"].(string); ok && cid != "" {
			result.ComponentID = cid
		}
		if props := transformers.SimplifyComponentProperties(node); len(props) > 0 {
			result.ComponentProperties = props
		}
	}
	if refs := transformers.SimplifyPropertyReferences(node); len(refs) > 0 {
		result.ComponentPropertyReferences = refs
	}
	if rawType == "COMPONENT" || rawType == "COMPONENT_SET" {
		if defs := transformers.SimplifyPropertyDefinitions(node); len(defs) > 0 {
			nodeID, _ := node["id"].(string)
			if nodeID != "" {
				ctx.mu.Lock()
				ctx.compPropDefs[nodeID] = defs
				ctx.mu.Unlock()
			}
		}
	}
}

func registerStyle(node map[string]any, ctx *traversalContext, value any, styleKeys []string, prefix string) string {
	if styleName, styleID, ok := getNamedStyleMatch(node, ctx.extraStyles, styleKeys); ok {
		valCanonical := transformers.StableStringify(value)
		ctx.mu.Lock()
		resolvedKey := resolveNamedStyleKeyLocked(ctx, styleName, styleID, valCanonical)
		ctx.globalVars.Styles[resolvedKey] = value
		ctx.namedStyleKeys[resolvedKey] = true
		ctx.mu.Unlock()
		return resolvedKey
	}
	return findOrCreateVar(ctx, value, prefix)
}

func getNamedStyleMatch(node map[string]any, extraStyles map[string]map[string]any, keys []string) (name, id string, ok bool) {
	stylesMap, has := node["styles"].(map[string]any)
	if !has || len(extraStyles) == 0 {
		return "", "", false
	}
	for _, k := range keys {
		if sid, ok := stylesMap[k].(string); ok && sid != "" {
			if meta, exists := extraStyles[sid]; exists {
				if sName, ok := meta["name"].(string); ok && strings.TrimSpace(sName) != "" {
					return strings.TrimSpace(sName), sid, true
				}
			}
		}
	}
	return "", "", false
}

func resolveNamedStyleKeyLocked(ctx *traversalContext, name, styleID, valCanonical string) string {
	existing, exists := ctx.globalVars.Styles[name]
	if !exists {
		return name
	}
	if transformers.StableStringify(existing) == valCanonical {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, styleID)
}

func findOrCreateVar(ctx *traversalContext, value any, prefix string) string {
	canonical := transformers.StableStringify(value)
	sum := sha1.Sum([]byte(canonical))
	fullHash := hex.EncodeToString(sum[:])

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	if existingID, ok := ctx.styleCache[canonical]; ok {
		return existingID
	}

	length := 8
	varID := prefix + "_" + fullHash[:length]
	for {
		if _, occupied := ctx.globalVars.Styles[varID]; !occupied || length+4 > len(fullHash) {
			break
		}
		length += 4
		varID = prefix + "_" + fullHash[:length]
	}

	ctx.globalVars.Styles[varID] = value
	ctx.styleCache[canonical] = varID
	return varID
}

// generateShortHashID computes a deterministic 8-char SHA-1 hex ID with prefix,
// expanding by 4 hex chars on hash collision.
func generateShortHashID(canonical, prefix string, isOccupied func(string) bool) string {
	sum := sha1.Sum([]byte(canonical))
	fullHash := hex.EncodeToString(sum[:])
	length := 8
	id := prefix + fullHash[:length]
	for isOccupied(id) && length+4 <= len(fullHash) {
		length += 4
		id = prefix + fullHash[:length]
	}
	return id
}

func registerInlineTextStyle(ctx *traversalContext, delta map[string]any) string {
	canonical := transformers.StableStringify(delta)
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if existingID, ok := ctx.inlineCache[canonical]; ok {
		return existingID
	}
	next := ctx.tsCounter.Add(1)
	varID := fmt.Sprintf("ts%d", next)
	ctx.globalVars.Styles[varID] = delta
	ctx.inlineCache[canonical] = varID
	return varID
}

func simplifyComponents(raw map[string]map[string]any, propDefs map[string]map[string]any) map[string]SimplifiedComponentDefinition {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]SimplifiedComponentDefinition, len(raw))
	for id, m := range raw {
		def := SimplifiedComponentDefinition{
			ID:                  id,
			Key:                 strVal(m, "key"),
			Name:                strVal(m, "name"),
			ComponentSetID:      strVal(m, "componentSetId"),
			Description:         strVal(m, "description"),
			PropertyDefinitions: propDefs[id],
		}
		out[id] = def
	}
	return out
}

func simplifyComponentSets(raw map[string]map[string]any, propDefs map[string]map[string]any) map[string]SimplifiedComponentSetDefinition {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]SimplifiedComponentSetDefinition, len(raw))
	for id, m := range raw {
		def := SimplifiedComponentSetDefinition{
			ID:                  id,
			Key:                 strVal(m, "key"),
			Name:                strVal(m, "name"),
			Description:         strVal(m, "description"),
			PropertyDefinitions: propDefs[id],
		}
		out[id] = def
	}
	return out
}

func mergeNestedMaps(dst map[string]map[string]any, src any) {
	m, ok := src.(map[string]any)
	if !ok {
		return
	}
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			dst[k] = sub
		}
	}
}

func getSliceOfMaps(v any) []map[string]any {
	raw, ok := v.([]any)
	if !ok {
		if typed, ok := v.([]map[string]any); ok {
			return typed
		}
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func strVal(m map[string]any, k string) string {
	if s, ok := m[k].(string); ok {
		return s
	}
	return ""
}
