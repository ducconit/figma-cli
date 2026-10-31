package figma

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	figmaPathPattern  = regexp.MustCompile(`^/(file|design)/([a-zA-Z0-9]+)(?:/branch/([a-zA-Z0-9]+))?`)
	fileKeyPattern    = regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	nodeIDPattern     = regexp.MustCompile(`^I?\d+[:|-]\d+(?:;\d+[:|-]\d+)*$`)
	nodeIDFileCleaner = strings.NewReplacer(":", "_", ";", "_")
)

// SanitizeNodeIDForFile converts a Figma node ID (e.g. "12:34;56:78") into a safe filename token ("12_34_56_78").
func SanitizeNodeIDForFile(nodeID string) string {
	return nodeIDFileCleaner.Replace(nodeID)
}

// URLParts holds extracted fileKey and normalized nodeId from a Figma URL.
type URLParts struct {
	FileKey string
	NodeID  string
}

// NormalizeNodeID converts browser hyphenated node IDs (1234-5678) into Figma API colon format (1234:5678).
// Supports comma-separated lists of node IDs (e.g. "12-34,56-78" -> "12:34,56:78").
func NormalizeNodeID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 1 {
		return strings.ReplaceAll(raw, "-", ":")
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, strings.ReplaceAll(p, "-", ":"))
		}
	}
	return strings.Join(out, ",")
}

// ValidateFileKey checks that fileKey is non-empty alphanumeric.
func ValidateFileKey(fileKey string) error {
	if !fileKeyPattern.MatchString(fileKey) {
		return fmt.Errorf("invalid fileKey %q: must be alphanumeric", fileKey)
	}
	return nil
}

// ValidateNodeID checks that nodeID (or comma-separated nodeIDs) matches Figma node ID format when provided.
func ValidateNodeID(nodeID string) error {
	if nodeID == "" {
		return nil
	}
	for _, part := range strings.Split(nodeID, ",") {
		part = strings.TrimSpace(part)
		if !nodeIDPattern.MatchString(part) {
			return fmt.Errorf("invalid nodeId %q: expected format like '1234:5678' or 'I5666:180910;1:10515'", nodeID)
		}
	}
	return nil
}

// ParseFigmaURL extracts fileKey and optional node-id from a Figma URL.
// Supports https://www.figma.com/file/<fileKey>/... and https://www.figma.com/design/<fileKey>/...
func ParseFigmaURL(input string) (URLParts, error) {
	input = strings.TrimSpace(input)
	u, err := url.Parse(input)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return URLParts{}, fmt.Errorf("not a valid URL: %s", input)
	}

	host := strings.ToLower(u.Hostname())
	if host != "figma.com" && !strings.HasSuffix(host, ".figma.com") {
		return URLParts{}, fmt.Errorf("not a Figma URL: %s", input)
	}

	matches := figmaPathPattern.FindStringSubmatch(u.Path)
	if len(matches) < 3 {
		return URLParts{}, fmt.Errorf("could not extract file key from Figma URL (only /file/ and /design/ URLs are supported): %s", input)
	}

	fileKey := matches[2]
	if len(matches) >= 4 && matches[3] != "" {
		fileKey = matches[3]
	}
	rawNodeID := u.Query().Get("node-id")
	return URLParts{
		FileKey: fileKey,
		NodeID:  NormalizeNodeID(rawNodeID),
	}, nil
}

// ResolveTarget parses a positional argument that can be either a full Figma URL or a direct fileKey,
// and combines it with explicit flagFileKey and flagNodeID overrides.
func ResolveTarget(positional, flagFileKey, flagNodeID string) (fileKey, nodeID string, err error) {
	fileKey = strings.TrimSpace(flagFileKey)
	nodeID = NormalizeNodeID(flagNodeID)

	pos := strings.TrimSpace(positional)
	if pos != "" {
		if strings.Contains(pos, "://") || strings.Contains(pos, "figma.com/") {
			parsed, parseErr := ParseFigmaURL(pos)
			if parseErr != nil {
				if fileKey == "" {
					return "", "", parseErr
				}
			} else {
				if fileKey == "" {
					fileKey = parsed.FileKey
				}
				if nodeID == "" {
					nodeID = parsed.NodeID
				}
			}
		} else if fileKey == "" {
			fileKey = pos
		}
	}

	if fileKey == "" {
		return "", "", fmt.Errorf("either a Figma URL, positional fileKey, or --file-key is required")
	}
	if err := ValidateFileKey(fileKey); err != nil {
		return "", "", err
	}
	if err := ValidateNodeID(nodeID); err != nil {
		return "", "", err
	}
	return fileKey, nodeID, nil
}
