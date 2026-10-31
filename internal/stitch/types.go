package stitch

// TypographyToken defines a font style token according to the Google Stitch DESIGN.md spec.
type TypographyToken struct {
	FontFamily    string `json:"fontFamily" yaml:"fontFamily"`
	FontSize      string `json:"fontSize" yaml:"fontSize"`
	FontWeight    int    `json:"fontWeight" yaml:"fontWeight"`
	LineHeight    string `json:"lineHeight" yaml:"lineHeight"`
	LetterSpacing string `json:"letterSpacing,omitempty" yaml:"letterSpacing,omitempty"`
}

// DesignSystemSpec holds all machine-readable design tokens for the DESIGN.md frontmatter
// as well as documentation tables in the markdown body.
type DesignSystemSpec struct {
	Version     string                     `json:"version" yaml:"version"`
	Name        string                     `json:"name" yaml:"name"`
	Description string                     `json:"description,omitempty" yaml:"description,omitempty"`
	Omitted     []string                   `json:"omitted,omitempty" yaml:"omitted,omitempty"`
	Colors      map[string]string          `json:"colors" yaml:"colors"`
	Typography  map[string]TypographyToken `json:"typography" yaml:"typography"`
	Spacing     map[string]string          `json:"spacing" yaml:"spacing"`
	Rounded     map[string]string          `json:"rounded" yaml:"rounded"`
	Elevation   map[string]string          `json:"elevation,omitempty" yaml:"elevation,omitempty"`
	Components  map[string]map[string]string `json:"components,omitempty" yaml:"components,omitempty"`
}

// ExtractionOptions configures design system discovery and token resolution.
type ExtractionOptions struct {
	Depth       int
	ForceCanvas bool
	Validate    bool
}
