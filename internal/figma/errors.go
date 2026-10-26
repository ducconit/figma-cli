package figma

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

const maxResponseBodyChars = 500

var whitespaceRegex = regexp.MustCompile(`\s+`)

// HTTPError represents a non-2xx HTTP response from the Figma API.
type HTTPError struct {
	Headers    map[string]string
	StatusText string
	Body       string
	StatusCode int
}

func (e *HTTPError) Error() string {
	suffix := ""
	if e.Body != "" {
		suffix = fmt.Sprintf(" — %s", e.Body)
	}
	return fmt.Sprintf("Fetch failed with status %d: %s%s", e.StatusCode, e.StatusText, suffix)
}

// SanitizeAndRedactBody collapses whitespace, redacts secrets, and truncates long bodies.
func SanitizeAndRedactBody(raw string, secrets []string) string {
	collapsed := strings.TrimSpace(whitespaceRegex.ReplaceAllString(raw, " "))
	if collapsed == "" {
		return ""
	}
	result := collapsed
	for _, secret := range secrets {
		if secret != "" {
			result = strings.ReplaceAll(result, secret, "[REDACTED]")
		}
	}
	runes := []rune(result)
	if len(runes) > maxResponseBodyChars {
		return string(runes[:maxResponseBodyChars]) + "… [truncated]"
	}
	return result
}

// ExtractLowerHeaders converts http.Header into a lowercase key map.
func ExtractLowerHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[strings.ToLower(k)] = strings.Join(v, ", ")
		}
	}
	return out
}

// BuildRateLimitMessage builds an actionable message for HTTP 429 responses using Figma headers.
func BuildRateLimitMessage(err *HTTPError) string {
	var parts []string
	parts = append(parts, "Figma API rate limit exceeded (HTTP 429).")

	if retryAfter := err.Headers["retry-after"]; retryAfter != "" {
		parts = append(parts, fmt.Sprintf("Retry after %s seconds.", retryAfter))
	}
	if strings.EqualFold(err.Headers["x-figma-rate-limit-type"], "low") {
		parts = append(parts, "Your Figma seat type (Viewer or Collaborator) has a lower API rate limit.")
	}
	planTier := strings.ToLower(err.Headers["x-figma-plan-tier"])
	if planTier == "starter" || planTier == "student" {
		parts = append(parts, fmt.Sprintf("Your %s plan has limited API access.", planTier))
	}
	if upgradeLink := err.Headers["x-figma-upgrade-link"]; upgradeLink != "" {
		parts = append(parts, fmt.Sprintf("Upgrade: %s", upgradeLink))
	}
	parts = append(parts, "See https://developers.figma.com/docs/rest-api/rate-limits/")
	return strings.Join(parts, " ")
}

// BuildForbiddenMessage builds a detailed troubleshooting message for HTTP 403 responses.
func BuildForbiddenMessage(endpoint string, err *HTTPError, proxyMode string) string {
	var sections []string

	header := fmt.Sprintf("Request to Figma API endpoint '%s' returned 403 Forbidden.", endpoint)
	if err.Body != "" {
		header += fmt.Sprintf("\nResponse body: %s", err.Body)
	}
	sections = append(sections, header)

	causes := strings.Join([]string{
		"Common causes:",
		"1. Missing required token scopes (ensure 'File content: Read' and 'Dev resources: Read' are enabled).",
		"2. Access token was revoked, mistyped, or (for OAuth tokens) expired.",
		"3. The token's account does not have permission to view this Figma file.",
		"4. File sharing settings restrict viewers from copying, sharing, or exporting.",
		"5. An HTTP intermediary (corporate proxy, firewall, VPN) blocked the request before reaching Figma.",
	}, "\n")
	sections = append(sections, causes)

	switch proxyMode {
	case "explicit":
		sections = append(sections, "Note: Outbound requests are routed through an explicit proxy (--proxy / FIGMA_PROXY). Try --proxy=none if the proxy is blocking api.figma.com.")
	case "env":
		sections = append(sections, "Note: Outbound requests are using HTTP_PROXY/HTTPS_PROXY from your environment. Try setting NO_PROXY=api.figma.com or passing --proxy=none.")
	}

	return strings.Join(sections, "\n\n")
}
