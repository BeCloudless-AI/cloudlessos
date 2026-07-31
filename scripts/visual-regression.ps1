param(
  [string]$OutputDirectory = "artifacts/visual-regression",
  [string]$BaselinePath = "scripts/visual-baseline.json",
  [switch]$Quick,
  [switch]$UpdateBaseline
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$output = if ([IO.Path]::IsPathRooted($OutputDirectory)) {
  $OutputDirectory
} else {
  Join-Path $root $OutputDirectory
}
$baseline = if ([IO.Path]::IsPathRooted($BaselinePath)) {
  $BaselinePath
} else {
  Join-Path $root $BaselinePath
}
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"

if (-not (Test-Path -LiteralPath $edge)) {
  throw "Microsoft Edge was not found at $edge"
}

New-Item -ItemType Directory -Force -Path $output | Out-Null

function Invoke-Harness {
  param(
    [Parameter(Mandatory)][string]$Script,
    [string[]]$Arguments = @()
  )

  $path = Join-Path $PSScriptRoot $Script
  if (-not (Test-Path -LiteralPath $path)) {
    throw "Visual harness is missing: $path"
  }

  $previousProfile = $env:CLOUDLESS_VISUAL_EDGE_PROFILE
  $env:CLOUDLESS_VISUAL_EDGE_PROFILE = Join-Path $env:TEMP ("cloudless-visual-edge-" + [guid]::NewGuid().ToString("N"))
  try {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $path @Arguments
    if ($LASTEXITCODE -ne 0) {
      throw "$Script failed with exit code $LASTEXITCODE"
    }
    # Edge's screenshot process can briefly outlive its launcher. A short drain
    # prevents a large capture matrix from exhausting browser processes.
    Start-Sleep -Milliseconds 1800
  } finally {
    $env:CLOUDLESS_VISUAL_EDGE_PROFILE = $previousProfile
  }
}

function Copy-Capture {
  param(
    [Parameter(Mandatory)][string]$TemporaryName,
    [Parameter(Mandatory)][string]$DestinationName,
    [Parameter(Mandatory)][int]$ExpectedWidth,
    [Parameter(Mandatory)][int]$ExpectedHeight
  )

  $source = Join-Path $env:TEMP $TemporaryName
  if (-not (Test-Path -LiteralPath $source)) {
    throw "Expected screenshot was not produced: $source"
  }

  $destination = Join-Path $output $DestinationName
  Copy-Item -LiteralPath $source -Destination $destination -Force

  Add-Type -AssemblyName System.Drawing
  $image = [Drawing.Image]::FromFile($destination)
  try {
    if ($image.Width -ne $ExpectedWidth -or $image.Height -ne $ExpectedHeight) {
      throw "$DestinationName has size $($image.Width)x$($image.Height), expected ${ExpectedWidth}x${ExpectedHeight}"
    }
    if ((Get-Item -LiteralPath $destination).Length -lt 4096) {
      throw "$DestinationName is unexpectedly small and is probably blank"
    }
    [PSCustomObject]@{
      file = $DestinationName
      width = $image.Width
      height = $image.Height
      bytes = (Get-Item -LiteralPath $destination).Length
      sha256 = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()
    }
  } finally {
    $image.Dispose()
  }
}

function Get-VisualSignature {
  param(
    [Parameter(Mandatory)][string]$Path,
    [int]$Size = 32
  )

  Add-Type -AssemblyName System.Drawing
  $source = [Drawing.Bitmap]::FromFile($Path)
  $sample = [Drawing.Bitmap]::new($Size, $Size)
  $graphics = [Drawing.Graphics]::FromImage($sample)
  try {
    $graphics.Clear([Drawing.Color]::Black)
    $graphics.InterpolationMode = [Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $graphics.PixelOffsetMode = [Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $graphics.DrawImage($source, 0, 0, $Size, $Size)
    $values = [Collections.Generic.List[int]]::new($Size * $Size)
    for ($y = 0; $y -lt $Size; $y++) {
      for ($x = 0; $x -lt $Size; $x++) {
        $pixel = $sample.GetPixel($x, $y)
        $luminance = [Math]::Round((0.2126 * $pixel.R) + (0.7152 * $pixel.G) + (0.0722 * $pixel.B))
        $values.Add([int]$luminance)
      }
    }
    return @($values)
  } finally {
    $graphics.Dispose()
    $sample.Dispose()
    $source.Dispose()
  }
}

$captures = [Collections.Generic.List[object]]::new()

$homeSizes = @(
  @{ Name = "desktop-720p"; Width = 1280; Height = 720 },
  @{ Name = "desktop-1080p"; Width = 1920; Height = 1080 },
  @{ Name = "desktop-1440p"; Width = 2560; Height = 1440 }
)
foreach ($size in $homeSizes) {
  Remove-Item -LiteralPath (Join-Path $env:TEMP "visual-$($size.Name).png") -Force -ErrorAction SilentlyContinue
  Invoke-Harness "home-harness.ps1" @(
    "-Width", [string]$size.Width,
    "-Height", [string]$size.Height,
    "-Name", "visual-$($size.Name)"
  )
  $captures.Add((Copy-Capture "visual-$($size.Name).png" "$($size.Name).png" $size.Width $size.Height))
}

Get-ChildItem -LiteralPath $env:TEMP -Filter "dh-*.png" -ErrorAction SilentlyContinue |
  Remove-Item -Force -ErrorAction SilentlyContinue
Invoke-Harness "design-harness.ps1"
$designCaptures = @(
  @{ Temp = "dh-settings.png"; Name = "settings.png"; Width = 900; Height = 720 },
  @{ Temp = "dh-models.png"; Name = "model-manager.png"; Width = 1320; Height = 940 },
  @{ Temp = "dh-models-compact.png"; Name = "model-manager-compact.png"; Width = 760; Height = 940 },
  @{ Temp = "dh-model-detail.png"; Name = "model-detail.png"; Width = 1040; Height = 760 },
  @{ Temp = "dh-launcher.png"; Name = "app-launcher.png"; Width = 1180; Height = 820 },
  @{ Temp = "dh-assistant.png"; Name = "assistant.png"; Width = 520; Height = 720 }
)
foreach ($capture in $designCaptures) {
  $captures.Add((Copy-Capture $capture.Temp $capture.Name $capture.Width $capture.Height))
}

Invoke-Harness "gate-harness.ps1"
$captures.Add((Copy-Capture "gate.png" "model-startup.png" 1240 620))

if (-not $Quick) {
  Invoke-Harness "api-harness.ps1"
  $captures.Add((Copy-Capture "apiaccess.png" "api-access.png" 980 1180))

  Invoke-Harness "models-launch-harness.ps1"
  $captures.Add((Copy-Capture "models-launch.png" "model-launch.png" 900 1000))

  Invoke-Harness "inf-tabs-harness.ps1"
  $captures.Add((Copy-Capture "inftabs.png" "inference-tabs.png" 1240 880))

  Invoke-Harness "workflow-harness.ps1"
  $workflowCaptures = @(
    @{ Temp = "flow-power-dialog.png"; Name = "power-dialog.png"; Width = 760; Height = 720 },
    @{ Temp = "flow-update-center.png"; Name = "update-center.png"; Width = 1280; Height = 900 },
    @{ Temp = "flow-recipe-library.png"; Name = "recipe-library.png"; Width = 1280; Height = 940 },
    @{ Temp = "flow-cluster-wizard.png"; Name = "cluster-wizard.png"; Width = 1280; Height = 900 }
  )
  foreach ($capture in $workflowCaptures) {
    $captures.Add((Copy-Capture $capture.Temp $capture.Name $capture.Width $capture.Height))
  }
}

$signatureSize = 32
$defaultThreshold = 3.0
$signatureCaptures = [Collections.Generic.List[object]]::new()
foreach ($capture in $captures) {
  $signatureCaptures.Add([PSCustomObject]@{
    file = $capture.file
    width = $capture.width
    height = $capture.height
    signature = @(Get-VisualSignature (Join-Path $output $capture.file) $signatureSize)
  })
}

$comparisonRows = [Collections.Generic.List[object]]::new()
$comparisonFailures = [Collections.Generic.List[string]]::new()
if ($UpdateBaseline) {
  $baselineDirectory = Split-Path -Parent $baseline
  if ($baselineDirectory) {
    New-Item -ItemType Directory -Force -Path $baselineDirectory | Out-Null
  }
  [PSCustomObject]@{
    schema = 1
    source = "orchestrator/internal/api/web/index.html"
    sample_size = $signatureSize
    max_mean_absolute_error = $defaultThreshold
    captures = $signatureCaptures
  } | ConvertTo-Json -Depth 8 -Compress | Set-Content -LiteralPath $baseline -Encoding UTF8
} else {
  if (-not (Test-Path -LiteralPath $baseline)) {
    throw "Visual baseline is missing: $baseline. Review the captures, then run with -UpdateBaseline."
  }
  $expected = Get-Content -LiteralPath $baseline -Raw | ConvertFrom-Json
  if ($expected.schema -ne 1 -or $expected.sample_size -ne $signatureSize) {
    throw "Unsupported visual baseline schema or sample size in $baseline"
  }
  $threshold = if ($null -ne $expected.max_mean_absolute_error) {
    [double]$expected.max_mean_absolute_error
  } else {
    $defaultThreshold
  }
  $expectedByFile = @{}
  foreach ($entry in $expected.captures) { $expectedByFile[$entry.file] = $entry }
  foreach ($actual in $signatureCaptures) {
    if (-not $expectedByFile.ContainsKey($actual.file)) {
      $comparisonFailures.Add("$($actual.file): no reviewed baseline")
      continue
    }
    $want = $expectedByFile[$actual.file]
    if ($want.width -ne $actual.width -or $want.height -ne $actual.height) {
      $comparisonFailures.Add("$($actual.file): dimensions differ from baseline")
      continue
    }
    if ($want.signature.Count -ne $actual.signature.Count -or $actual.signature.Count -ne ($signatureSize * $signatureSize)) {
      $comparisonFailures.Add("$($actual.file): visual signature is incomplete")
      continue
    }
    $sum = 0.0
    $maximum = 0
    for ($index = 0; $index -lt $actual.signature.Count; $index++) {
      $difference = [Math]::Abs([int]$actual.signature[$index] - [int]$want.signature[$index])
      $sum += $difference
      if ($difference -gt $maximum) { $maximum = $difference }
    }
    $mean = $sum / $actual.signature.Count
    $passed = $mean -le $threshold
    $comparisonRows.Add([PSCustomObject]@{
      file = $actual.file
      mean_absolute_error = [Math]::Round($mean, 4)
      maximum_sample_error = $maximum
      threshold = $threshold
      passed = $passed
    })
    if (-not $passed) {
      $comparisonFailures.Add("$($actual.file): mean visual difference $([Math]::Round($mean, 2)) exceeds $threshold")
    }
  }
}

$comparison = [PSCustomObject]@{
  baseline = $baseline
  updated = [bool]$UpdateBaseline
  passed = $comparisonFailures.Count -eq 0
  results = $comparisonRows
  failures = $comparisonFailures
}
$manifest = [PSCustomObject]@{
  schema = 1
  generated_at = [DateTime]::UtcNow.ToString("o")
  source = "orchestrator/internal/api/web/index.html"
  captures = $captures
  comparison = $comparison
}
$manifestPath = Join-Path $output "manifest.json"
$manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $manifestPath -Encoding UTF8

Write-Host ""
Write-Host "Visual-regression capture set is ready:"
Write-Host "  $output"
Write-Host "  $($captures.Count) validated PNG files"
Write-Host "  $manifestPath"
if ($UpdateBaseline) {
  Write-Host "  reviewed baseline updated: $baseline"
} elseif ($comparisonFailures.Count -gt 0) {
  throw ("Visual regression detected:`n - " + ($comparisonFailures -join "`n - "))
} else {
  Write-Host "  all captures are within the reviewed visual baseline threshold"
}
