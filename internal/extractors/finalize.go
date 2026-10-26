package extractors

import (
	"regexp"
	"runtime"
	"sync"

	"github.com/ducconit/figma-cli/internal/transformers"
)

var inlineTextStyleKeyRegex = regexp.MustCompile(`^ts\d+$`)

// FinalizeDesign performs the 3-step global optimization pass from Figma-Context-MCP:
// 1. Inline single-use generated styles back onto nodes (and drop orphaned styles with count==0).
// 2. Deduplicate repeated node bodies (count >= 2) into EL-xxxxxxxx templates in the elements map.
// 3. Inline generated styles that are used exclusively by a single element template to eliminate double indirection.
func FinalizeDesign(
	nodes []*SimplifiedNode,
	globalVars *GlobalVars,
	namedStyleKeys map[string]bool,
) ([]*SimplifiedNode, *GlobalVars, map[string]ElementBody) {
	if globalVars == nil {
		globalVars = &GlobalVars{Styles: make(map[string]any)}
	}
	if namedStyleKeys == nil {
		namedStyleKeys = make(map[string]bool)
	}

	// Step 1: Count style references and inline single-use generated styles
	counts := countStyleRefs(nodes)
	inlineKeys := make(map[string]bool)
	dropKeys := make(map[string]bool)

	for key := range globalVars.Styles {
		if inlineTextStyleKeyRegex.MatchString(key) {
			continue
		}
		cnt := counts[key]
		if namedStyleKeys[key] {
			if cnt == 0 {
				dropKeys[key] = true
			}
			continue
		}
		if cnt == 0 {
			dropKeys[key] = true
		} else if cnt == 1 {
			inlineKeys[key] = true
		}
	}

	applySingleUseInlining(nodes, globalVars.Styles, inlineKeys)
	for k := range inlineKeys {
		delete(globalVars.Styles, k)
	}
	for k := range dropKeys {
		delete(globalVars.Styles, k)
	}

	// Re-count remaining style references before template deduplication
	remainingCounts := countStyleRefs(nodes)

	// Step 2: Deduplicate repeated node bodies into EL-xxxxxxxx templates
	elements, instanceCounts := deduplicateElements(nodes)

	// Step 3: Inline styles used exclusively by a single element template
	inlineExclusiveStyles(elements, instanceCounts, remainingCounts, globalVars.Styles, namedStyleKeys)

	return nodes, globalVars, elements
}

func (n *SimplifiedNode) styleFieldPtrs() [5]*any {
	return [5]*any{&n.Layout, &n.Fills, &n.Strokes, &n.Effects, &n.TextStyle}
}

func countStyleRefs(nodes []*SimplifiedNode) map[string]int {
	counts := make(map[string]int)
	var visit func(list []*SimplifiedNode)
	visit = func(list []*SimplifiedNode) {
		for _, n := range list {
			for _, fieldPtr := range n.styleFieldPtrs() {
				if s, ok := (*fieldPtr).(string); ok && s != "" {
					counts[s]++
				}
			}
			if len(n.Children) > 0 {
				visit(n.Children)
			}
		}
	}
	visit(nodes)
	return counts
}

func applySingleUseInlining(nodes []*SimplifiedNode, styles map[string]any, inlineKeys map[string]bool) {
	for _, n := range nodes {
		for _, fieldPtr := range n.styleFieldPtrs() {
			if s, ok := (*fieldPtr).(string); ok && inlineKeys[s] {
				*fieldPtr = styles[s]
			}
		}
		if len(n.Children) > 0 {
			applySingleUseInlining(n.Children, styles, inlineKeys)
		}
	}
}

type nodeBodyEntry struct {
	node      *SimplifiedNode
	body      ElementBody
	canonical string
}

func deduplicateElements(nodes []*SimplifiedNode) (map[string]ElementBody, map[string]int) {
	var allNodes []*SimplifiedNode
	var flatten func(list []*SimplifiedNode)
	flatten = func(list []*SimplifiedNode) {
		for _, n := range list {
			allNodes = append(allNodes, n)
			if len(n.Children) > 0 {
				flatten(n.Children)
			}
		}
	}
	flatten(nodes)

	if len(allNodes) == 0 {
		return nil, nil
	}

	entries := make([]nodeBodyEntry, len(allNodes))

	// Parallelize CPU-heavy node body extraction + canonical serialization via jobCh & entryCh
	if len(allNodes) >= 8 {
		type indexedBodyEntry struct {
			index int
			entry nodeBodyEntry
		}
		type nodeJob struct {
			index int
			node  *SimplifiedNode
		}
		numWorkers := runtime.GOMAXPROCS(0)
		if numWorkers > len(allNodes) {
			numWorkers = len(allNodes)
		}
		jobCh := make(chan nodeJob, len(allNodes))
		entryCh := make(chan indexedBodyEntry, len(allNodes))

		for i, n := range allNodes {
			jobCh <- nodeJob{index: i, node: n}
		}
		close(jobCh)

		var wg sync.WaitGroup
		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for job := range jobCh {
					body := extractNodeBody(job.node)
					if len(body) > 1 {
						entryCh <- indexedBodyEntry{
							index: job.index,
							entry: nodeBodyEntry{
								node:      job.node,
								body:      body,
								canonical: transformers.StableStringify(map[string]any(body)),
							},
						}
					}
				}
			}()
		}
		wg.Wait()
		close(entryCh)

		for item := range entryCh {
			entries[item.index] = item.entry
		}
	} else {
		for i, n := range allNodes {
			body := extractNodeBody(n)
			if len(body) > 1 {
				entries[i] = nodeBodyEntry{
					node:      n,
					body:      body,
					canonical: transformers.StableStringify(map[string]any(body)),
				}
			}
		}
	}

	bodyFreq := make(map[string]int, len(entries))
	bodySample := make(map[string]ElementBody, len(entries))
	var canonicalOrder []string
	for _, entry := range entries {
		if entry.canonical == "" {
			continue
		}
		if bodyFreq[entry.canonical] == 0 {
			canonicalOrder = append(canonicalOrder, entry.canonical)
			bodySample[entry.canonical] = entry.body
		}
		bodyFreq[entry.canonical]++
	}

	elements := make(map[string]ElementBody)
	canonicalToID := make(map[string]string)
	instanceCounts := make(map[string]int)

	for _, canonical := range canonicalOrder {
		freq := bodyFreq[canonical]
		if freq < 2 {
			continue
		}
		elID := generateShortHashID(canonical, "EL-", func(candidate string) bool {
			_, exists := elements[candidate]
			return exists
		})
		elements[elID] = bodySample[canonical]
		canonicalToID[canonical] = elID
		instanceCounts[elID] = freq
	}

	if len(elements) == 0 {
		return nil, nil
	}

	for _, entry := range entries {
		if entry.canonical == "" {
			continue
		}
		if elID, ok := canonicalToID[entry.canonical]; ok {
			clearNodeBodyForTemplate(entry.node, elID)
		}
	}

	return elements, instanceCounts
}

func inlineExclusiveStyles(
	elements map[string]ElementBody,
	instanceCounts map[string]int,
	styleRefCounts map[string]int,
	styles map[string]any,
	namedStyleKeys map[string]bool,
) {
	if len(elements) == 0 {
		return
	}
	styleFields := []string{"layout", "fills", "strokes", "effects", "textStyle"}
	for elID, body := range elements {
		instCnt := instanceCounts[elID]
		for _, field := range styleFields {
			ref, ok := body[field].(string)
			if !ok || ref == "" {
				continue
			}
			if namedStyleKeys[ref] || inlineTextStyleKeyRegex.MatchString(ref) {
				continue
			}
			if styleRefCounts[ref] == instCnt {
				if val, exists := styles[ref]; exists {
					body[field] = val
					delete(styles, ref)
				}
			}
		}
	}
}

func extractNodeBody(n *SimplifiedNode) ElementBody {
	body := make(ElementBody)
	if n.Type != "" {
		body["type"] = n.Type
	}
	if n.Layout != nil {
		body["layout"] = n.Layout
	}
	if n.Fills != nil {
		body["fills"] = n.Fills
	}
	if n.Strokes != nil {
		body["strokes"] = n.Strokes
	}
	if n.StrokeWeight != "" {
		body["strokeWeight"] = n.StrokeWeight
	}
	if n.StrokeWeights != "" {
		body["strokeWeights"] = n.StrokeWeights
	}
	if len(n.StrokeDashes) > 0 {
		body["strokeDashes"] = n.StrokeDashes
	}
	if n.StrokeAlign != "" {
		body["strokeAlign"] = n.StrokeAlign
	}
	if n.Effects != nil {
		body["effects"] = n.Effects
	}
	if n.Opacity != nil {
		body["opacity"] = *n.Opacity
	}
	if n.BorderRadius != "" {
		body["borderRadius"] = n.BorderRadius
	}
	if len(n.Styles) > 0 {
		body["styles"] = n.Styles
	}
	if n.ComponentID != "" {
		body["componentId"] = n.ComponentID
	}
	if len(n.ComponentProperties) > 0 {
		body["componentProperties"] = n.ComponentProperties
	}
	if len(n.ComponentPropertyReferences) > 0 {
		body["componentPropertyReferences"] = n.ComponentPropertyReferences
	}
	if n.TextStyle != nil {
		body["textStyle"] = n.TextStyle
	}
	if n.BoldWeight != nil {
		body["boldWeight"] = *n.BoldWeight
	}
	if n.Text != "" {
		body["text"] = n.Text
	}
	return body
}

func clearNodeBodyForTemplate(n *SimplifiedNode, templateID string) {
	n.Template = templateID
	n.Type = ""
	n.Layout = nil
	n.Fills = nil
	n.Strokes = nil
	n.StrokeWeight = ""
	n.StrokeWeights = ""
	n.StrokeDashes = nil
	n.StrokeAlign = ""
	n.Effects = nil
	n.Opacity = nil
	n.BorderRadius = ""
	n.Styles = nil
	n.ComponentID = ""
	n.ComponentProperties = nil
	n.ComponentPropertyReferences = nil
	n.TextStyle = nil
	n.BoldWeight = nil
	n.Text = ""
}
