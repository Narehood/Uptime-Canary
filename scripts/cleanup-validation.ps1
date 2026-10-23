param([switch]$Apply)

# Generated validation data only. Preview by default; -Apply performs deletion.
# Source, .env, databases, backups, identity state, the installed Go toolchain,
# and the three default application binaries are deliberately retained.
$ErrorActionPreference = 'Stop'
$workspaceRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$generatedRoot = [IO.Path]::GetFullPath((Join-Path $workspaceRoot '.canary'))
$protectedArtifact = Join-Path $generatedRoot 'browser-auth-proof\run-ZgaV8S'
$targets = [Collections.Generic.List[string]]::new()
$directoryNames = @(
    'jsonata-runtime-candidate\r3-intrinsic-20261022T223939Z\gocache',
    'jsonata-runtime-candidate\r3-intrinsic-20261022T223939Z\gomodcache',
    'jsonata-runtime-candidate\r2-resource-20261022T221741Z\gocache',
    'jsonata-runtime-candidate\r2-resource-20261022T221741Z\gomodcache',
    'jsonata-runtime-candidate\run-20261022T213352Z\gocache',
    'jsonata-runtime-candidate\run-20261022T213352Z\gomodcache',
    'jsonata-candidate-review\run-20261022-first\gocache',
    'jsonata-candidate-review\run-20261022-first\gomodcache',
    'alpine-validation\windows-race\cache',
    'hierarchy-review\first\go-build',
    'jsonata-runtime-study\run-bf7ace51f517\gocache',
    'jsonata-runtime-study\run-bf7ace51f517\gomodcache',
    'alpine-validation\go-cache',
    'alpine-validation\vm',
    'alpine-validation\tools',
    'alpine-validation\downloads',
    'tools\race-gcc'
)
foreach ($relative in $directoryNames) {
    $candidate = Join-Path $generatedRoot $relative
    if (Test-Path -LiteralPath $candidate) { $targets.Add($candidate) }
}
$download = Join-Path $generatedRoot 'tools\go1.27.3.windows-amd64.zip'
if (Test-Path -LiteralPath $download) { $targets.Add($download) }
$compiledArchive = Join-Path $generatedRoot 'alpine-validation\runs\20261022T165314Z-leaf-runtime\artifacts.tar.gz'
if (Test-Path -LiteralPath $compiledArchive) { $targets.Add($compiledArchive) }

# Browser profiles belong to the disposable contract-review runs, not the user.
$browserRoot = Join-Path $generatedRoot 'dashboard-contract-review'
if (Test-Path -LiteralPath $browserRoot) {
    foreach ($run in Get-ChildItem -LiteralPath $browserRoot -Directory -Force) {
        foreach ($profile in Get-ChildItem -LiteralPath $run.FullName -Directory -Force) {
            if ($profile.Name -like 'chrome-profile*' -or $profile.Name -eq 'dist') {
                $targets.Add($profile.FullName)
            }
        }
    }
}

# Old compiled validation executables are reproducible. Keep reports, hashes,
# screenshots, source snapshots, source archives and the current private work.
$artifactRoots = @('bin', 'staging', 'worker-race', 'process-proofs',
    'alpine-validation', 'jsonata-runtime-candidate',
    'jsonata-candidate-review', 'jsonata-integration-preflight',
    'jsonata-runtime-study', 'hierarchy-review', 'dashboard-contract-review',
    'jsonata-profile-review')
foreach ($relative in $artifactRoots) {
    $area = Join-Path $generatedRoot $relative
    if (-not (Test-Path -LiteralPath $area)) { continue }
    # Some old runs kept compiled executables under artifacts/ or recovery-fix/.
    # Walk generated folders while excluding source and cache subtrees entirely.
    $artifactFiles = [Collections.Generic.List[object]]::new()
    $pendingAreas = [Collections.Generic.Queue[string]]::new()
    $pendingAreas.Enqueue($area)
    while ($pendingAreas.Count) {
        $currentArea = $pendingAreas.Dequeue()
        foreach ($child in Get-ChildItem -LiteralPath $currentArea -Force) {
            if ($child.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Refusing generated link: $($child.FullName)" }
            if (-not $child.PSIsContainer) { $artifactFiles.Add($child); continue }
            if ($child.Name -in @('source', 'sources', 'control', 'reference', 'mutations', 'gocache', 'gomodcache', 'go-build', 'cache', 'tools')) { continue }
            $pendingAreas.Enqueue($child.FullName)
        }
    }
    foreach ($file in $artifactFiles) {
        if ($file.Length -lt 1MB) { continue }
        if ($file.FullName -match '\\(source|control|reference|mutations|gocache|gomodcache|go-build)\\') { continue }
        if ($relative -eq 'bin' -and $file.Name -in @('canary.exe', 'canary-dev.exe', 'canary-cli.exe')) { continue }
        $stream = [IO.File]::OpenRead($file.FullName)
        try { $signature = New-Object byte[] 4; $count = $stream.Read($signature, 0, 4) }
        finally { $stream.Dispose() }
        if ($count -eq 4 -and (($signature[0] -eq 77 -and $signature[1] -eq 90) -or
            ($signature[0] -eq 127 -and $signature[1] -eq 69 -and $signature[2] -eq 76 -and $signature[3] -eq 70))) {
            $targets.Add($file.FullName)
        }
    }
}

$processes = @(Get-CimInstance Win32_Process | Where-Object {
    $_.Name -match '^(qemu.*|chrome|canary.*|go|compile|link|gcc.*|python.*)\.exe$'
})
$verified = [Collections.Generic.List[object]]::new()
foreach ($target in ($targets | Sort-Object -Unique)) {
    $full = [IO.Path]::GetFullPath($target)
    if (-not $full.StartsWith($generatedRoot + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing target outside generated files: $full"
    }
    if ($protectedArtifact.StartsWith($full + '\', [StringComparison]::OrdinalIgnoreCase) -or
        $full.StartsWith($protectedArtifact, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Refusing protected artifact.'
    }
    # Windows PowerShell requires the native long-path form for downloaded
    # module fixtures that exceed MAX_PATH. Safety checks use the normal path.
    $native = '\\?\' + $full
    $item = Get-Item -LiteralPath $native -Force
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Refusing link: $full" }
    $children = @()
    if ($item.PSIsContainer) {
        $children = @(Get-ChildItem -LiteralPath $native -Recurse -Force)
        if ($children | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint } | Select-Object -First 1) {
            throw "Refusing directory containing links: $full"
        }
    }
    foreach ($process in $processes) {
        if (($process.ExecutablePath -and $process.ExecutablePath.StartsWith($full, [StringComparison]::OrdinalIgnoreCase)) -or
            ($process.CommandLine -and $process.CommandLine.IndexOf($full, [StringComparison]::OrdinalIgnoreCase) -ge 0)) {
            throw "Target is in use by process $($process.ProcessId): $full"
        }
    }
    $bytes = $item.Length
    if ($item.PSIsContainer) { $bytes = ($children | Where-Object { -not $_.PSIsContainer } | Measure-Object Length -Sum).Sum }
    $verified.Add([pscustomobject]@{ path = $full; bytes = [long]$bytes })
}
$total = ($verified | Measure-Object bytes -Sum).Sum
Write-Output ('Generated data selected: {0:N2} GiB in {1} targets' -f ($total / 1GB), $verified.Count)
if (-not $Apply) {
    $verified | Select-Object @{Name='GiB';Expression={[math]::Round($_.bytes / 1GB, 3)}}, path | Format-Table -AutoSize
    Write-Output 'Preview only. Run this script with -Apply to remove the listed generated data.'
    exit 0
}
$recordRoot = Join-Path $generatedRoot 'cleanup-20261022'
New-Item -ItemType Directory -Path $recordRoot -Force | Out-Null
$verified | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath (Join-Path $recordRoot 'manual-cleanup-targets.json')
foreach ($entry in $verified) {
    Remove-Item -LiteralPath ('\\?\' + $entry.path) -Recurse -Force -ErrorAction Stop
    Write-Output ('Removed ' + $entry.path.Substring($generatedRoot.Length + 1))
}
Write-Output ('Removed {0:N2} GiB of generated validation data.' -f ($total / 1GB))
