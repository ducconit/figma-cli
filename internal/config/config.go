package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// Source represents where a configuration value came from.
type Source string

const (
	SourceCLI        Source = "cli"
	SourceProjectEnv Source = "project-dotenv"
	SourceHomeEnv    Source = "home-figma-dotenv"
	SourceEnv        Source = "env"
	SourceDefault    Source = "default"
)

// AuthConfig holds Figma API authentication credentials.
type AuthConfig struct {
	APIKey     string
	OAuthToken string
	Source     Source
	EnvFile    string
	UseOAuth   bool
}

// ErrMissingCredentials is returned when neither PAT nor OAuth token is configured.
var ErrMissingCredentials = errors.New(
	"Figma authentication required: set FIGMA_API_KEY (or FIGMA_OAUTH_TOKEN) in project ./.env, ~/.figma/.env, or OS environment, or pass --token / --oauth-token",
)

// DefaultHomeEnvPath returns ~/.figma/.env for the given home directory (or os.UserHomeDir() if empty).
func DefaultHomeEnvPath(homeDir string) (string, error) {
	if strings.TrimSpace(homeDir) == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		homeDir = h
	}
	return filepath.Join(homeDir, ".figma", ".env"), nil
}

// UpsertEnvFile writes or updates FIGMA_API_KEY and/or FIGMA_OAUTH_TOKEN in envPath
// while preserving any unrelated environment variables. Creates parent directory with 0700 and file with 0600.
func UpsertEnvFile(envPath, apiKey, oauthToken string) error {
	apiKey = strings.TrimSpace(apiKey)
	oauthToken = strings.TrimSpace(oauthToken)
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		return fmt.Errorf("create directory for %s: %w", envPath, err)
	}

	existingBytes, _ := os.ReadFile(envPath)
	lines := strings.Split(string(existingBytes), "\n")
	filtered := make([]string, 0, len(lines)+2)
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		if apiKey != "" && (strings.HasPrefix(trimmed, "FIGMA_API_KEY=") || strings.HasPrefix(trimmed, "export FIGMA_API_KEY=")) {
			continue
		}
		if oauthToken != "" && (strings.HasPrefix(trimmed, "FIGMA_OAUTH_TOKEN=") || strings.HasPrefix(trimmed, "export FIGMA_OAUTH_TOKEN=")) {
			continue
		}
		filtered = append(filtered, ln)
	}
	if apiKey != "" {
		filtered = append(filtered, fmt.Sprintf("FIGMA_API_KEY=%s", apiKey))
	}
	if oauthToken != "" {
		filtered = append(filtered, fmt.Sprintf("FIGMA_OAUTH_TOKEN=%s", oauthToken))
	}
	content := strings.Join(filtered, "\n") + "\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", envPath, err)
	}
	return nil
}

// SaveHomeToken writes or updates FIGMA_API_KEY (or FIGMA_OAUTH_TOKEN when isOAuth is true)
// exclusively in ~/.figma/.env with 0600 permissions.
func SaveHomeToken(token string, isOAuth bool, homeDir string) (string, error) {
	tok := strings.TrimSpace(token)
	if tok == "" {
		return "", errors.New("token must not be empty")
	}
	envPath, err := DefaultHomeEnvPath(homeDir)
	if err != nil {
		return "", fmt.Errorf("resolve ~/.figma/.env path: %w", err)
	}
	apiKey, oauthToken := tok, ""
	if isOAuth {
		apiKey, oauthToken = "", tok
	}
	if err := UpsertEnvFile(envPath, apiKey, oauthToken); err != nil {
		return "", err
	}
	return envPath, nil
}

// readDotEnvMap parses a .env file into a key-value map without mutating the OS environment.
func readDotEnvMap(path string) (map[string]string, string, bool) {
	if strings.TrimSpace(path) == "" {
		return nil, "", false
	}
	target := path
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		return nil, target, false
	}
	m, err := godotenv.Read(target)
	if err != nil {
		return nil, target, false
	}
	return m, target, true
}

// findProjectEnvCandidates discovers project .env files in priority order when running in an installed project:
//  1. If explicit --env <path> is given, only that path is checked as the project .env.
//  2. If projectDir (cwd) is inside <project>/.agents/skills/... or <project>/.agent/skills/...,
//     resolves <project>/.env (the root of the project open in the AI Agent).
//  3. Checks <projectDir>/.env and walks upward toward the project root (stopping at .git/.agents/.agent or before homeDir/tmp).
func findProjectEnvCandidates(envFile, projectDir, homeDir string) []string {
	if strings.TrimSpace(envFile) != "" {
		return []string{strings.TrimSpace(envFile)}
	}
	if strings.TrimSpace(projectDir) == "" {
		return nil
	}

	absProj, err := filepath.Abs(projectDir)
	if err != nil {
		absProj = projectDir
	}
	absHome, _ := filepath.Abs(homeDir)
	absTmp := filepath.Clean(os.TempDir())

	seen := make(map[string]bool)
	var out []string
	addCandidate := func(dir string) {
		if dir == "" {
			return
		}
		envPath := filepath.Join(dir, ".env")
		if !seen[envPath] {
			seen[envPath] = true
			out = append(out, envPath)
		}
	}

	// 1. Direct projectDir/.env (most common when AI Agent runs fm from the open project root)
	addCandidate(absProj)

	// 2. If cwd is inside <project>/.agents/skills/... or <project>/.agent/skills/..., extract <project>
	if hostProj := extractProjectRootFromSkillPath(absProj); hostProj != "" {
		addCandidate(hostProj)
	}

	// 3. Walk upward from absProj up to .git / .agents / .agent boundary (never crossing homeDir or /tmp)
	curr := absProj
	for i := 0; i < 6; i++ {
		parent := filepath.Dir(curr)
		if parent == curr || parent == "/" || parent == "." {
			break
		}
		if absHome != "" && curr == absHome {
			break
		}
		if curr == absTmp {
			break
		}
		addCandidate(curr)
		if hasProjectMarker(curr) {
			break
		}
		curr = parent
	}

	return out
}

func extractProjectRootFromSkillPath(dir string) string {
	slash := filepath.ToSlash(dir)
	for _, marker := range []string{"/.agents/skills", "/.agent/skills"} {
		if idx := strings.Index(slash, marker); idx > 0 {
			return filepath.FromSlash(slash[:idx])
		}
	}
	return ""
}

func hasProjectMarker(dir string) bool {
	for _, marker := range []string{".git", ".agents", ".agent"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// LoadEnvFile loads environment variables from the specified path (or project ./.env then ~/.figma/.env).
func LoadEnvFile(envPath string) (string, bool) {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	candidates := findProjectEnvCandidates(envPath, cwd, home)
	if homeEnv, err := DefaultHomeEnvPath(home); err == nil {
		candidates = append(candidates, homeEnv)
	}

	var loadedPath string
	var anyLoaded bool
	for i := len(candidates) - 1; i >= 0; i-- {
		target := candidates[i]
		if abs, err := filepath.Abs(target); err == nil {
			target = abs
		}
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			if err := godotenv.Overload(target); err == nil {
				loadedPath = target
				anyLoaded = true
			}
		}
	}
	return loadedPath, anyLoaded
}

// ResolveAuth resolves Figma credentials on the installed machine using the strict priority chain:
//  1. CLI flags (--token / --oauth-token)
//  2. Project .env file of the currently open project (./.env, or explicit --env <path>)
//  3. User global ~/.figma/.env file on the machine
//  4. OS Environment Variables of the device (FIGMA_OAUTH_TOKEN / FIGMA_API_KEY / FIGMA_TOKEN)
//
// Within the same source tier, when both OAuth and API key are present, OAuth takes precedence.
func ResolveAuth(flagAPIKey, flagOAuthToken, envFile string) AuthConfig {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	return ResolveAuthWithDirs(flagAPIKey, flagOAuthToken, envFile, cwd, home)
}

// ResolveAuthWithDirs resolves Figma credentials with explicit projectDir and homeDir roots.
func ResolveAuthWithDirs(flagAPIKey, flagOAuthToken, envFile, projectDir, homeDir string) AuthConfig {
	var projMap map[string]string
	var projPath string
	var projOK bool

	for _, candidate := range findProjectEnvCandidates(envFile, projectDir, homeDir) {
		if m, p, ok := readDotEnvMap(candidate); ok {
			projMap, projPath, projOK = m, p, true
			break
		}
	}

	var homeEnvCandidate string
	if strings.TrimSpace(homeDir) != "" {
		homeEnvCandidate = filepath.Join(homeDir, ".figma", ".env")
	}
	homeMap, homePath, homeOK := readDotEnvMap(homeEnvCandidate)

	// Apply non-auth variables (such as FIGMA_PROXY / HTTPS_PROXY) in priority order:
	// project ./.env > ~/.figma/.env > OS env
	applySupplementalEnv(projMap, homeMap)

	// 1. CLI Flags (Highest Priority)
	cliAPI := strings.TrimSpace(flagAPIKey)
	cliOAuth := strings.TrimSpace(flagOAuthToken)
	if cliOAuth != "" || cliAPI != "" {
		return AuthConfig{
			APIKey:     cliAPI,
			OAuthToken: cliOAuth,
			UseOAuth:   cliOAuth != "",
			Source:     SourceCLI,
		}
	}

	// 2. Open Project's .env (./.env or --env)
	if projOK {
		apiKey := firstNonEmpty(projMap["FIGMA_API_KEY"], projMap["FIGMA_TOKEN"])
		oauthToken := strings.TrimSpace(projMap["FIGMA_OAUTH_TOKEN"])
		if oauthToken != "" || apiKey != "" {
			return AuthConfig{
				APIKey:     apiKey,
				OAuthToken: oauthToken,
				UseOAuth:   oauthToken != "",
				Source:     SourceEnv,
				EnvFile:    projPath,
			}
		}
	}

	// 3. Machine User's ~/.figma/.env
	if homeOK {
		apiKey := firstNonEmpty(homeMap["FIGMA_API_KEY"], homeMap["FIGMA_TOKEN"])
		oauthToken := strings.TrimSpace(homeMap["FIGMA_OAUTH_TOKEN"])
		if oauthToken != "" || apiKey != "" {
			return AuthConfig{
				APIKey:     apiKey,
				OAuthToken: oauthToken,
				UseOAuth:   oauthToken != "",
				Source:     SourceEnv,
				EnvFile:    homePath,
			}
		}
	}

	// 4. Device / OS Environment Variables (Lowest Priority)
	osAPI := firstNonEmpty(os.Getenv("FIGMA_API_KEY"), os.Getenv("FIGMA_TOKEN"))
	osOAuth := strings.TrimSpace(os.Getenv("FIGMA_OAUTH_TOKEN"))
	if osOAuth != "" || osAPI != "" {
		return AuthConfig{
			APIKey:     osAPI,
			OAuthToken: osOAuth,
			UseOAuth:   osOAuth != "",
			Source:     SourceEnv,
		}
	}

	return AuthConfig{
		Source: SourceDefault,
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func applySupplementalEnv(projMap, homeMap map[string]string) {
	isAuthKey := func(k string) bool {
		return k == "FIGMA_API_KEY" || k == "FIGMA_TOKEN" || k == "FIGMA_OAUTH_TOKEN"
	}
	for k, v := range homeMap {
		if !isAuthKey(k) && strings.TrimSpace(v) != "" {
			_ = os.Setenv(k, v)
		}
	}
	for k, v := range projMap {
		if !isAuthKey(k) && strings.TrimSpace(v) != "" {
			_ = os.Setenv(k, v)
		}
	}
}

// Validate checks that either APIKey or OAuthToken is present.
func (a AuthConfig) Validate() error {
	if a.UseOAuth && a.OAuthToken != "" {
		return nil
	}
	if a.APIKey != "" {
		return nil
	}
	return ErrMissingCredentials
}

// Secrets returns all non-empty tokens for log/error redaction.
func (a AuthConfig) Secrets() []string {
	out := make([]string, 0, 2)
	if a.APIKey != "" {
		out = append(out, a.APIKey)
	}
	if a.OAuthToken != "" {
		out = append(out, a.OAuthToken)
	}
	return out
}

// MaskToken masks all but the last 4 characters of a sensitive token.
func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 4 {
		return "****"
	}
	return fmt.Sprintf("****%s", token[len(token)-4:])
}
