<#
.SYNOPSIS
Synchronize the local main branch with QuantumNous/new-api.

.DESCRIPTION
The script fetches the configured upstream remote and merges upstream/main into
the current local main branch. It refuses to run with uncommitted changes and
never pushes unless -Push is specified.

.EXAMPLE
pwsh -File .\sync-upstream.ps1

.EXAMPLE
pwsh -File .\sync-upstream.ps1 -Push
#>

[CmdletBinding()]
param(
    [string]$Branch = "main",
    [string]$UpstreamRemote = "upstream",
    [string]$UpstreamUrl = "https://github.com/QuantumNous/new-api.git",
    [string]$ForkRemote = "origin",
    [switch]$Push
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Write-Step {
    param([Parameter(Mandatory)][string]$Message)

    Write-Host "`n>> $Message" -ForegroundColor Cyan
}

function Write-Success {
    param([Parameter(Mandatory)][string]$Message)

    Write-Host "   OK: $Message" -ForegroundColor Green
}

function Invoke-Git {
    param([Parameter(Mandatory)][string[]]$Arguments)

    & git @Arguments
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "Git command failed with exit code ${exitCode}: git $($Arguments -join ' ')"
    }
}

if ($Branch.StartsWith("-", [System.StringComparison]::Ordinal)) {
    throw "Branch name cannot start with '-': $Branch"
}
if ($UpstreamRemote.StartsWith("-", [System.StringComparison]::Ordinal)) {
    throw "Remote name cannot start with '-': $UpstreamRemote"
}
if ($ForkRemote.StartsWith("-", [System.StringComparison]::Ordinal)) {
    throw "Remote name cannot start with '-': $ForkRemote"
}
if ($Push -and $ForkRemote -eq $UpstreamRemote) {
    throw "ForkRemote and UpstreamRemote must be different when -Push is used."
}

if ($null -eq (Get-Command git -ErrorAction SilentlyContinue)) {
    throw "Git was not found. Install Git and ensure it is available in PATH."
}

Push-Location -LiteralPath $PSScriptRoot
try {
    $repoRoot = (Invoke-Git -Arguments @("rev-parse", "--show-toplevel") | Select-Object -First 1).Trim()
    Set-Location -LiteralPath $repoRoot

    Write-Step "Checking repository state"

    & git check-ref-format --branch $Branch | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "Invalid branch name: $Branch"
    }

    $currentBranch = (Invoke-Git -Arguments @("branch", "--show-current") | Select-Object -First 1).Trim()
    if ([string]::IsNullOrWhiteSpace($currentBranch)) {
        throw "HEAD is detached. Check out '$Branch' before synchronizing."
    }
    if ($currentBranch -ne $Branch) {
        throw "Current branch is '$currentBranch'. Check out '$Branch' before synchronizing."
    }

    $workingTreeChanges = @(Invoke-Git -Arguments @("status", "--porcelain=v1", "--untracked-files=all"))
    if ($workingTreeChanges.Count -gt 0) {
        Write-Host "Uncommitted changes were found:" -ForegroundColor Yellow
        $workingTreeChanges | ForEach-Object { Write-Host "   $_" -ForegroundColor Yellow }
        throw "Commit or stash these changes before synchronizing."
    }
    Write-Success "Branch '$Branch' is clean"

    Write-Step "Checking upstream remote"
    $remotes = @(Invoke-Git -Arguments @("remote"))
    if ($remotes -notcontains $UpstreamRemote) {
        Invoke-Git -Arguments @("remote", "add", $UpstreamRemote, $UpstreamUrl)
        Write-Success "Added remote '$UpstreamRemote' -> $UpstreamUrl"
    } else {
        $configuredUrl = (Invoke-Git -Arguments @("remote", "get-url", $UpstreamRemote) | Select-Object -First 1).Trim().TrimEnd("/")
        $expectedUrl = $UpstreamUrl.Trim().TrimEnd("/")
        if ($configuredUrl -ne $expectedUrl) {
            throw "Remote '$UpstreamRemote' points to '$configuredUrl', expected '$expectedUrl'. Pass the configured URL with -UpstreamUrl if this is intentional."
        }
        Write-Success "Remote '$UpstreamRemote' -> $configuredUrl"
    }

    if ($Push -and $remotes -notcontains $ForkRemote) {
        throw "Fork remote '$ForkRemote' does not exist."
    }

    Write-Step "Fetching $UpstreamRemote/$Branch"
    Invoke-Git -Arguments @("fetch", "--prune", $UpstreamRemote, $Branch)

    $upstreamRef = "$UpstreamRemote/$Branch"
    Invoke-Git -Arguments @("rev-parse", "--verify", $upstreamRef) | Out-Null
    $beforeCommit = (Invoke-Git -Arguments @("rev-parse", "HEAD") | Select-Object -First 1).Trim()

    Write-Step "Merging $upstreamRef into $Branch"
    & git merge --no-edit -- $upstreamRef
    $mergeExitCode = $LASTEXITCODE
    if ($mergeExitCode -ne 0) {
        $conflictedFiles = @(& git diff --name-only --diff-filter=U)
        if ($conflictedFiles.Count -gt 0) {
            Write-Host "`nMerge conflicts require manual resolution:" -ForegroundColor Red
            $conflictedFiles | ForEach-Object { Write-Host "   $_" -ForegroundColor Red }
            Write-Host "`nAfter resolving: git add <files>; git commit" -ForegroundColor Yellow
            Write-Host "To cancel:       git merge --abort" -ForegroundColor Yellow
        } else {
            Write-Host "`nMerge failed. Review the Git output above." -ForegroundColor Red
            Write-Host "If a merge is in progress, cancel it with: git merge --abort" -ForegroundColor Yellow
        }
        exit $mergeExitCode
    }

    $afterCommit = (Invoke-Git -Arguments @("rev-parse", "HEAD") | Select-Object -First 1).Trim()
    if ($beforeCommit -eq $afterCommit) {
        Write-Success "Local '$Branch' is already up to date"
    } else {
        Write-Success "Synchronized $($beforeCommit.Substring(0, 12)) -> $($afterCommit.Substring(0, 12))"
    }

    if ($Push) {
        Write-Step "Pushing $Branch to $ForkRemote"
        Invoke-Git -Arguments @("push", $ForkRemote, "HEAD:refs/heads/$Branch")
        Write-Success "Pushed '$Branch' to '$ForkRemote'"
    } else {
        Write-Host "`nLocal synchronization is complete. To update your fork, run:" -ForegroundColor Yellow
        Write-Host "   pwsh -File .\sync-upstream.ps1 -Push" -ForegroundColor Yellow
    }
} finally {
    Pop-Location
}
