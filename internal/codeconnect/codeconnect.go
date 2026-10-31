package codeconnect

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ducconit/figma-cli/internal/extractors"
)

// LocalComponent represents a UI component file discovered in the local repository.
type LocalComponent struct {
	Name      string   `json:"name"`
	FilePath  string   `json:"filePath"`
	RelPath   string   `json:"relPath"`
	Framework string   `json:"framework"` // react, vue, swiftui
	Props     []string `json:"props,omitempty"`
}

// ComponentMatch pairs a Figma Component or ComponentSet with its best local codebase match.
type ComponentMatch struct {
	FigmaID        string          `json:"figmaId"`
	FigmaName      string          `json:"figmaName"`
	ComponentKey   string          `json:"componentKey,omitempty"`
	IsComponentSet bool            `json:"isComponentSet"`
	Local          *LocalComponent `json:"local,omitempty"`
	Confidence     string          `json:"confidence"` // exact, leaf, partial, unmapped
	Properties     []string        `json:"properties,omitempty"`
}

var (
	nonAlphaNumRegex = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	propLineRegex    = regexp.MustCompile(`(?m)^\s*([a-zA-Z_][a-zA-Z0-9_]*)\??\s*:`)
)

func normalizeCompKey(s string) string {
	return strings.ToLower(nonAlphaNumRegex.ReplaceAllString(strings.TrimSpace(s), ""))
}

func safeFileSlug(s string) string {
	clean := strings.Trim(nonAlphaNumRegex.ReplaceAllString(strings.TrimSpace(s), "-"), "-")
	if clean == "" {
		return "Component"
	}
	return clean
}

func cleanComment(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "*/", "* /")
	return strings.TrimSpace(s)
}

// ScanLocalComponents recursively walks rootDir and discovers React (.tsx/.jsx), Vue (.vue), and SwiftUI (.swift) components.
func ScanLocalComponents(rootDir string) ([]LocalComponent, error) {
	absRoot, err := filepath.Abs(filepath.Clean(rootDir))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("stat directory %s: %w", absRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", absRoot)
	}

	var components []LocalComponent
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist" || name == "build" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".test.tsx") || strings.HasSuffix(lower, ".spec.tsx") ||
			strings.HasSuffix(lower, ".stories.tsx") || strings.HasSuffix(lower, ".figma.ts") ||
			strings.HasSuffix(lower, ".figma.tsx") {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(name))
		var framework string
		switch ext {
		case ".tsx", ".jsx":
			framework = "react"
		case ".vue":
			framework = "vue"
		case ".swift":
			framework = "swiftui"
		default:
			return nil
		}

		baseName := strings.TrimSuffix(name, filepath.Ext(name))
		if strings.EqualFold(baseName, "index") {
			baseName = filepath.Base(filepath.Dir(path))
		}

		rel, relErr := filepath.Rel(absRoot, path)
		if relErr != nil {
			rel = name
		}

		var props []string
		if content, readErr := os.ReadFile(path); readErr == nil && len(content) <= 64*1024 {
			matches := propLineRegex.FindAllStringSubmatch(string(content), 25)
			seen := make(map[string]bool)
			for _, m := range matches {
				if len(m) >= 2 && !seen[m[1]] {
					seen[m[1]] = true
					props = append(props, m[1])
				}
			}
			sort.Strings(props)
		}

		components = append(components, LocalComponent{
			Name:      baseName,
			FilePath:  path,
			RelPath:   filepath.ToSlash(rel),
			Framework: framework,
			Props:     props,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(components, func(i, j int) bool {
		return components[i].RelPath < components[j].RelPath
	})
	return components, nil
}

// MatchComponents pairs Figma ComponentSets and standalone Components from design against local components.
func MatchComponents(design *extractors.SimplifiedDesign, local []LocalComponent, fileKey string) []ComponentMatch {
	if design == nil {
		return nil
	}

	type figmaCandidate struct {
		id         string
		name       string
		key        string
		isSet      bool
		propDefs   map[string]any
	}

	var candidates []figmaCandidate
	for id, cs := range design.ComponentSets {
		candidates = append(candidates, figmaCandidate{
			id:       id,
			name:     cs.Name,
			key:      cs.Key,
			isSet:    true,
			propDefs: cs.PropertyDefinitions,
		})
	}
	for id, c := range design.Components {
		if c.ComponentSetID != "" {
			// Skip individual variant children belonging to a ComponentSet
			if _, exists := design.ComponentSets[c.ComponentSetID]; exists {
				continue
			}
		}
		candidates = append(candidates, figmaCandidate{
			id:       id,
			name:     c.Name,
			key:      c.Key,
			isSet:    false,
			propDefs: c.PropertyDefinitions,
		})
	}

	// Fallback: if Components and ComponentSets maps are empty, inspect extracted nodes of type COMPONENT / COMPONENT_SET / INSTANCE
	if len(candidates) == 0 {
		seenID := make(map[string]bool)
		var walk func(nodes []*extractors.SimplifiedNode)
		walk = func(nodes []*extractors.SimplifiedNode) {
			for _, n := range nodes {
				if n == nil {
					continue
				}
				if (n.Type == "COMPONENT" || n.Type == "COMPONENT_SET" || n.ComponentID != "") && !seenID[n.ID] {
					seenID[n.ID] = true
					candidates = append(candidates, figmaCandidate{
						id:    n.ID,
						name:  n.Name,
						isSet: n.Type == "COMPONENT_SET",
					})
				}
				walk(n.Children)
			}
		}
		walk(design.Nodes)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].id < candidates[j].id
	})

	matches := make([]ComponentMatch, 0, len(candidates))
	for _, cand := range candidates {
		var propNames []string
		for k := range cand.propDefs {
			cleanProp := strings.Split(k, "#")[0]
			propNames = append(propNames, cleanProp)
		}
		sort.Strings(propNames)

		bestLocal, confidence := findBestLocalMatch(cand.name, local)
		matches = append(matches, ComponentMatch{
			FigmaID:        cand.id,
			FigmaName:      cand.name,
			ComponentKey:   cand.key,
			IsComponentSet: cand.isSet,
			Local:          bestLocal,
			Confidence:     confidence,
			Properties:     propNames,
		})
	}
	return matches
}

func findBestLocalMatch(figmaName string, local []LocalComponent) (*LocalComponent, string) {
	fullNorm := normalizeCompKey(figmaName)
	parts := strings.Split(figmaName, "/")
	leafName := strings.TrimSpace(parts[len(parts)-1])
	leafNorm := normalizeCompKey(leafName)

	// 1. Exact full name match
	for i := range local {
		if normalizeCompKey(local[i].Name) == fullNorm && fullNorm != "" {
			cp := local[i]
			return &cp, "exact"
		}
	}
	// 2. Leaf segment match (e.g. "Buttons / Primary Button" -> "PrimaryButton")
	for i := range local {
		if normalizeCompKey(local[i].Name) == leafNorm && leafNorm != "" {
			cp := local[i]
			return &cp, "leaf"
		}
	}
	// 3. Substring / suffix match
	for i := range local {
		locNorm := normalizeCompKey(local[i].Name)
		if locNorm != "" && leafNorm != "" && (strings.Contains(locNorm, leafNorm) || strings.Contains(leafNorm, locNorm)) {
			cp := local[i]
			return &cp, "partial"
		}
	}
	return nil, "unmapped"
}

// GenerateFigmaTS generates a parserless .figma.ts Code Connect template for a ComponentMatch.
func GenerateFigmaTS(match ComponentMatch, fileKey string) string {
	nodeIDHyphen := strings.ReplaceAll(match.FigmaID, ":", "-")
	figmaURL := fmt.Sprintf("https://www.figma.com/design/%s?node-id=%s", fileKey, nodeIDHyphen)

	compName := safeFileSlug(match.FigmaName)
	importPath := "./" + compName
	if match.Local != nil {
		compName = safeFileSlug(match.Local.Name)
		importPath = "./" + strings.TrimSuffix(match.Local.RelPath, filepath.Ext(match.Local.RelPath))
	}
	// Convert hyphenated slug to PascalCase identifier for JSX tag
	parts := strings.Split(compName, "-")
	var pascal strings.Builder
	for _, p := range parts {
		if p != "" {
			pascal.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	tagName := pascal.String()
	if tagName == "" {
		tagName = "Component"
	}

	var sb strings.Builder
	sb.WriteString("// Generated by fm code-connect\n")
	sb.WriteString(fmt.Sprintf("// Figma Node: %s (%s)\n", cleanComment(match.FigmaName), figmaURL))
	sb.WriteString("import figma from \"@figma/code-connect\";\n\n")

	if len(match.Properties) > 0 {
		for _, prop := range match.Properties {
			varIdent := strings.ToLower(nonAlphaNumRegex.ReplaceAllString(prop, ""))
			if varIdent == "" {
				varIdent = "prop"
			}
			sb.WriteString(fmt.Sprintf("const %s = figma.properties.string(%q);\n", varIdent, cleanComment(prop)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("// Source: %s\n", importPath))
	sb.WriteString("export default figma.code`<")
	sb.WriteString(tagName)
	for _, prop := range match.Properties {
		varIdent := strings.ToLower(nonAlphaNumRegex.ReplaceAllString(prop, ""))
		if varIdent == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf(" %s={${%s}}", varIdent, varIdent))
	}
	sb.WriteString(" />`;\n")
	return sb.String()
}

// WriteTemplates writes parserless .figma.ts files for matched components into outDir,
// strictly enforcing directory boundary checks against path traversal.
func WriteTemplates(matches []ComponentMatch, fileKey, outDir string) ([]string, error) {
	absOutDir, err := filepath.Abs(filepath.Clean(outDir))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absOutDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory %s: %w", absOutDir, err)
	}

	var written []string
	usedNames := make(map[string]int)

	for _, m := range matches {
		if m.Local == nil {
			continue
		}
		rawBase := m.Local.Name
		if rawBase == "" {
			rawBase = m.FigmaName
		}
		baseSlug := safeFileSlug(rawBase)
		usedNames[baseSlug]++
		slug := baseSlug
		if count := usedNames[baseSlug]; count > 1 {
			slug = fmt.Sprintf("%s-%d", baseSlug, count)
		}

		fileName := slug + ".figma.ts"
		destPath := filepath.Join(absOutDir, fileName)
		absDest, err := filepath.Abs(filepath.Clean(destPath))
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(absDest, absOutDir+string(filepath.Separator)) {
			return nil, fmt.Errorf("security violation: target path %s escapes output directory %s", absDest, absOutDir)
		}

		content := GenerateFigmaTS(m, fileKey)
		if err := os.WriteFile(absDest, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", absDest, err)
		}
		written = append(written, absDest)
	}
	return written, nil
}
