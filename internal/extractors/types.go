package extractors

// GlobalVars holds shared style definitions referenced across nodes.
type GlobalVars struct {
	Styles map[string]any `json:"styles" yaml:"styles"`
}

// ElementBody represents the deduplicated body of a repeated node (excluding id, name, children, template).
type ElementBody map[string]any

// SimplifiedComponentDefinition holds component metadata at the file/node level.
type SimplifiedComponentDefinition struct {
	PropertyDefinitions map[string]any `json:"propertyDefinitions,omitempty" yaml:"propertyDefinitions,omitempty"`
	ID                  string         `json:"id" yaml:"id"`
	Key                 string         `json:"key" yaml:"key"`
	Name                string         `json:"name" yaml:"name"`
	ComponentSetID      string         `json:"componentSetId,omitempty" yaml:"componentSetId,omitempty"`
	Description         string         `json:"description,omitempty" yaml:"description,omitempty"`
}

// SimplifiedComponentSetDefinition holds variant group metadata.
type SimplifiedComponentSetDefinition struct {
	PropertyDefinitions map[string]any `json:"propertyDefinitions,omitempty" yaml:"propertyDefinitions,omitempty"`
	ID                  string         `json:"id" yaml:"id"`
	Key                 string         `json:"key" yaml:"key"`
	Name                string         `json:"name" yaml:"name"`
	Description         string         `json:"description,omitempty" yaml:"description,omitempty"`
}

// SimplifiedNode represents a single node in the simplified Figma tree.
type SimplifiedNode struct {
	Layout                      any               `json:"layout,omitempty" yaml:"layout,omitempty"`
	Fills                       any               `json:"fills,omitempty" yaml:"fills,omitempty"`
	Strokes                     any               `json:"strokes,omitempty" yaml:"strokes,omitempty"`
	Effects                     any               `json:"effects,omitempty" yaml:"effects,omitempty"`
	TextStyle                   any               `json:"textStyle,omitempty" yaml:"textStyle,omitempty"`
	ComponentProperties         map[string]any    `json:"componentProperties,omitempty" yaml:"componentProperties,omitempty"`
	ComponentPropertyReferences map[string]string `json:"componentPropertyReferences,omitempty" yaml:"componentPropertyReferences,omitempty"`
	Styles                      map[string]string `json:"styles,omitempty" yaml:"styles,omitempty"`
	Opacity                     *float64          `json:"opacity,omitempty" yaml:"opacity,omitempty"`
	BoldWeight                  *int              `json:"boldWeight,omitempty" yaml:"boldWeight,omitempty"`
	ID                          string            `json:"id" yaml:"id"`
	Name                        string            `json:"name,omitempty" yaml:"name,omitempty"`
	Type                        string            `json:"type,omitempty" yaml:"type,omitempty"`
	Template                    string            `json:"template,omitempty" yaml:"template,omitempty"`
	Text                        string            `json:"text,omitempty" yaml:"text,omitempty"`
	StrokeWeight                string            `json:"strokeWeight,omitempty" yaml:"strokeWeight,omitempty"`
	StrokeWeights               string            `json:"strokeWeights,omitempty" yaml:"strokeWeights,omitempty"`
	StrokeAlign                 string            `json:"strokeAlign,omitempty" yaml:"strokeAlign,omitempty"`
	BorderRadius                string            `json:"borderRadius,omitempty" yaml:"borderRadius,omitempty"`
	ComponentID                 string            `json:"componentId,omitempty" yaml:"componentId,omitempty"`
	StrokeDashes                []float64         `json:"strokeDashes,omitempty" yaml:"strokeDashes,omitempty"`
	Children                    []*SimplifiedNode `json:"children,omitempty" yaml:"children,omitempty"`
}

// SimplifiedDesign is the top-level output of the extraction and finalization pipeline.
type SimplifiedDesign struct {
	Components    map[string]SimplifiedComponentDefinition    `json:"components,omitempty" yaml:"components,omitempty"`
	ComponentSets map[string]SimplifiedComponentSetDefinition `json:"componentSets,omitempty" yaml:"componentSets,omitempty"`
	Elements      map[string]ElementBody                      `json:"elements,omitempty" yaml:"elements,omitempty"`
	GlobalVars    GlobalVars                                  `json:"globalVars" yaml:"globalVars"`
	Name          string                                      `json:"name" yaml:"name"`
	Nodes         []*SimplifiedNode                           `json:"nodes" yaml:"nodes"`
}

// Options configures tree traversal and extraction.
type Options struct {
	NodeFilter func(node map[string]any) bool
	MaxDepth   int
}
