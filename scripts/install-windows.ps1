# ==============================================================================
# figma-cli (fm) Universal Installer for Windows PowerShell
# Usage:
#   $env:IDE="antigravity"; irm https://raw.githubusercontent.com/ducconit/figma-cli/main/scripts/install-windows.ps1 | iex
#   $env:IDE="all"; irm https://raw.githubusercontent.com/ducconit/figma-cli/main/scripts/install-windows.ps1 | iex
# ==============================================================================

$ErrorActionPreference = "Stop"

$Repo = "ducconit/figma-cli"
$DefaultSkillPath = Join-Path $HOME ".agents\skills"
$TargetPath = if ($env:SKILL_PATH) { $env:SKILL_PATH } else { $DefaultSkillPath }
$SkillDir = Join-Path $TargetPath "figma-cli"
$ScriptsDir = Join-Path $SkillDir "scripts"

Write-Host "==> Installing figma-cli (fm) for Windows..." -ForegroundColor Cyan

# 1. Detect architecture
$Arch = if ([System.Environment]::Is64BitOperatingSystem) { "amd64" } else { "arm64" }
$AssetName = "fm-windows-$Arch.exe"
$DownloadUrl = "https://github.com/$Repo/releases/latest/download/$AssetName"

# 2. Ensure scripts directory exists
if (-not (Test-Path $ScriptsDir)) {
    New-Item -ItemType Directory -Path $ScriptsDir -Force | Out-Null
}

$ExePath = Join-Path $ScriptsDir "fm.exe"
$TempExe = Join-Path $ScriptsDir "fm_temp.exe"

Write-Host "==> Downloading latest release binary: $AssetName..." -ForegroundColor Cyan
Invoke-WebRequest -Uri $DownloadUrl -OutFile $TempExe -UseBasicParsing

# Move to fm.exe
if (Test-Path $ExePath) {
    Remove-Item -Path $ExePath -Force -ErrorAction SilentlyContinue
}
Move-Item -Path $TempExe -Destination $ExePath -Force

# 3. Parse IDE environment variable
$TargetIDE = if ($env:IDE) { $env:IDE.ToLower() } else { "all" }
$IdeFlags = @()

$IdeList = $TargetIDE -split "[, ]+"
foreach ($item in $IdeList) {
    switch ($item) {
        { $_ -in "antigravity", "agy", "gemini" } { $IdeFlags += "--antigravity" }
        "claude" { $IdeFlags += "--claude" }
        "codex" { $IdeFlags += "--codex" }
        "cursor" { $IdeFlags += "--cursor" }
        "windsurf" { $IdeFlags += "--windsurf" }
        "copilot" { $IdeFlags += "--copilot" }
        "opencode" { $IdeFlags += "--opencode" }
        "cline" { $IdeFlags += "--cline" }
        "all" { $IdeFlags += "--all" }
        default { $IdeFlags += "--all" }
    }
}

# 4. Run `fm install`
Write-Host "==> Configuring Skill and AI Agent symlinks (IDE: $TargetIDE)..." -ForegroundColor Cyan
$InstallArgs = @("install", "--path", $TargetPath, "--os", "windows") + $IdeFlags
& $ExePath @InstallArgs

# 5. Optional token setup if TOKEN or FIGMA_API_KEY is provided
$TokenVal = if ($env:TOKEN) { $env:TOKEN } else { $env:FIGMA_API_KEY }
if ($TokenVal) {
    Write-Host "==> Configuring Figma Personal Access Token..." -ForegroundColor Cyan
    & $ExePath set-token $TokenVal
}

Write-Host ""
Write-Host "🎉 figma-cli (fm) installed successfully for Windows!" -ForegroundColor Green
Write-Host "   Binary path : $ExePath"
Write-Host "   Skill path  : $SkillDir"
if (-not $TokenVal) {
    Write-Host "👉 Remember to set your Figma token:" -ForegroundColor Yellow
    Write-Host "   & '$ExePath' set-token figd_YOUR_PERSONAL_ACCESS_TOKEN" -ForegroundColor Yellow
}
