package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAuthPriorityChain(t *testing.T) {
	// Strictly verify priority order:
	// 1. CLI flags (--token / --oauth-token)
	// 2. Project .env file (./.env)
	// 3. Global ~/.figma/.env file
	// 4. OS environment variables (FIGMA_API_KEY / FIGMA_OAUTH_TOKEN)
	fakeProjectDir := t.TempDir()
	fakeHomeDir := t.TempDir()

	t.Setenv("HOME", fakeHomeDir)
	t.Setenv("FIGMA_API_KEY", "os_env_key_4444")
	t.Setenv("FIGMA_OAUTH_TOKEN", "")
	t.Setenv("FIGMA_TOKEN", "")

	// Step 1: Neither ./.env nor ~/.figma/.env exists -> falls back to OS environment (Priority 4)
	authOS := ResolveAuthWithDirs("", "", "", fakeProjectDir, fakeHomeDir)
	if err := authOS.Validate(); err != nil {
		t.Fatalf("expected valid auth from OS env, got: %v", err)
	}
	if authOS.APIKey != "os_env_key_4444" {
		t.Fatalf("expected OS env key 'os_env_key_4444', got %q", authOS.APIKey)
	}

	// Step 2: Create ~/.figma/.env -> overrides OS environment (Priority 3 > Priority 4)
	homeFigmaDir := filepath.Join(fakeHomeDir, ".figma")
	if err := os.MkdirAll(homeFigmaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	homeEnvFile := filepath.Join(homeFigmaDir, ".env")
	if err := os.WriteFile(homeEnvFile, []byte("FIGMA_API_KEY=home_figma_key_3333\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	authHome := ResolveAuthWithDirs("", "", "", fakeProjectDir, fakeHomeDir)
	if authHome.APIKey != "home_figma_key_3333" || authHome.EnvFile != homeEnvFile {
		t.Fatalf("expected ~/.figma/.env key 'home_figma_key_3333', got %+v", authHome)
	}

	// Step 3: Create project ./.env -> overrides ~/.figma/.env and OS environment (Priority 2 > Priority 3 > Priority 4)
	projEnvFile := filepath.Join(fakeProjectDir, ".env")
	if err := os.WriteFile(projEnvFile, []byte("FIGMA_API_KEY=project_dotenv_key_2222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	authProj := ResolveAuthWithDirs("", "", "", fakeProjectDir, fakeHomeDir)
	if authProj.APIKey != "project_dotenv_key_2222" || authProj.EnvFile != projEnvFile {
		t.Fatalf("expected project ./.env key 'project_dotenv_key_2222', got %+v", authProj)
	}

	// Step 4: CLI flag -> overrides project ./.env, ~/.figma/.env, and OS environment (Priority 1)
	authCLI := ResolveAuthWithDirs("cli_flag_key_1111", "", "", fakeProjectDir, fakeHomeDir)
	if authCLI.APIKey != "cli_flag_key_1111" || authCLI.Source != SourceCLI {
		t.Fatalf("expected CLI flag to win, got %+v", authCLI)
	}

	// Step 5: OAuth takes precedence within the same tier
	authOAuth := ResolveAuthWithDirs("cli_flag_key_1111", "oauth_tok_8888", "", fakeProjectDir, fakeHomeDir)
	if !authOAuth.UseOAuth || authOAuth.OAuthToken != "oauth_tok_8888" {
		t.Fatalf("expected OAuth precedence, got %+v", authOAuth)
	}

	// Step 6: When fm is invoked from inside <project>/.agents/skills/figma-cli/scripts,
	// it still resolves the open project's <project>/.env before ~/.figma/.env and OS env!
	skillScriptsDir := filepath.Join(fakeProjectDir, ".agents", "skills", "figma-cli", "scripts")
	if err := os.MkdirAll(skillScriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	authFromSkillSubdir := ResolveAuthWithDirs("", "", "", skillScriptsDir, fakeHomeDir)
	if authFromSkillSubdir.APIKey != "project_dotenv_key_2222" || authFromSkillSubdir.EnvFile != projEnvFile {
		t.Fatalf("expected open project .env when invoked from .agents/skills/figma-cli/scripts, got %+v", authFromSkillSubdir)
	}
}

func TestValidateAndMaskToken(t *testing.T) {
	fakeHome := t.TempDir()
	fakeProj := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("FIGMA_API_KEY", "")
	t.Setenv("FIGMA_OAUTH_TOKEN", "")
	t.Setenv("FIGMA_TOKEN", "")

	empty := ResolveAuthWithDirs("", "", "", fakeProj, fakeHome)
	if err := empty.Validate(); err == nil {
		t.Fatal("expected error when credentials are missing across all 4 tiers")
	}

	if got := MaskToken("figd_1234567890abcdef"); got != "****cdef" {
		t.Fatalf("expected ****cdef, got %q", got)
	}
	if got := MaskToken("abc"); got != "****" {
		t.Fatalf("expected **** for short token, got %q", got)
	}
}

func TestUpsertEnvFileSaveHomeTokenAndLoadEnv(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// 1. SaveHomeToken (PAT) -> writes ~/.figma/.env
	envPath, err := SaveHomeToken("figd_home_pat_1234", false, fakeHome)
	if err != nil {
		t.Fatalf("SaveHomeToken PAT failed: %v", err)
	}
	defPath, err := DefaultHomeEnvPath(fakeHome)
	if err != nil || envPath != defPath {
		t.Fatalf("expected %q, got %q (err=%v)", defPath, envPath, err)
	}

	// 2. UpsertEnvFile preserves existing comments/vars and updates OAuth token
	if err := os.WriteFile(envPath, []byte("# Comment line\nCUSTOM_VAR=hello\nFIGMA_API_KEY=old_key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveHomeToken("oauth_home_5678", true, fakeHome); err != nil {
		t.Fatalf("SaveHomeToken OAuth failed: %v", err)
	}
	if err := UpsertEnvFile(envPath, "figd_updated_9999", ""); err != nil {
		t.Fatalf("UpsertEnvFile update failed: %v", err)
	}

	// 3. Empty token rejected by SaveHomeToken
	if _, err := SaveHomeToken("   ", false, fakeHome); err == nil {
		t.Fatal("expected error for empty token in SaveHomeToken")
	}

	// 4. LoadEnvFile + ResolveAuth + Secrets()
	t.Setenv("CUSTOM_VAR", "")
	loadedPath, ok := LoadEnvFile(envPath)
	if !ok || loadedPath != envPath || os.Getenv("CUSTOM_VAR") != "hello" {
		t.Fatalf("LoadEnvFile failed: path=%q CUSTOM_VAR=%q ok=%v", loadedPath, os.Getenv("CUSTOM_VAR"), ok)
	}

	auth := ResolveAuth("pat_sec_1", "oauth_sec_2", envPath)
	secrets := auth.Secrets()
	if len(secrets) != 2 {
		t.Fatalf("expected 2 secrets, got %+v", secrets)
	}
}
