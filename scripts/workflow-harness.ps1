# Deterministic visual fixtures for workflow-heavy Cloudless surfaces. Markup is
# representative, while every style and icon comes from the production frontend.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$src = Join-Path $root "orchestrator\internal\api\web\index.html"
$html = Get-Content -LiteralPath $src -Raw -Encoding UTF8
$style = [regex]::Match($html, "(?s)<style>.*?</style>").Value
$sprite = [regex]::Match($html, "(?s)<!-- Cloudless brand mark.*?</svg>").Value
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"

function Shot {
  param([string]$Name, [string]$Body, [int]$Width, [int]$Height)
  $page = "<!doctype html><html data-theme=`"night`"><head><meta charset=`"utf-8`"><meta name=`"viewport`" content=`"width=device-width,initial-scale=1`">$style$freeze</head><body><div class=`"wall`"></div>$sprite$Body</body></html>"
  $tempHtml = Join-Path $env:TEMP "flow-$Name.html"
  $output = Join-Path $env:TEMP "flow-$Name.png"
  Remove-Item -LiteralPath $output -Force -ErrorAction SilentlyContinue
  [IO.File]::WriteAllText($tempHtml, $page, [Text.UTF8Encoding]::new($false))
  $profileBase = if ($env:CLOUDLESS_VISUAL_EDGE_PROFILE) { $env:CLOUDLESS_VISUAL_EDGE_PROFILE } else { Join-Path $env:TEMP "cloudless-flow-edge-$PID" }
  $profile = "$profileBase-$([guid]::NewGuid().ToString('N'))"
  $previousErrorPreference = $ErrorActionPreference
  $ErrorActionPreference = "SilentlyContinue"
  & $edge --headless=new --disable-gpu --hide-scrollbars --no-first-run "--user-data-dir=$profile" --force-device-scale-factor=1 "--window-size=$Width,$Height" --screenshot="$output" ("file:///" + ($tempHtml -replace "\\","/")) 2>$null | Out-Null
  $edgeExitCode = $LASTEXITCODE
  $ErrorActionPreference = $previousErrorPreference
  if ($edgeExitCode -ne 0) { throw "Edge failed to render $Name (exit $edgeExitCode)" }
  for ($attempt = 0; $attempt -lt 24 -and -not (Test-Path -LiteralPath $output); $attempt++) {
    Start-Sleep -Milliseconds 250
  }
  if (-not (Test-Path -LiteralPath $output)) { throw "No screenshot produced for $Name" }
  "OK $output"
}

$power = @'
<div class="key-dialog-layer" style="position:static;min-height:720px">
  <div class="key-dialog">
    <div class="key-dialog-head">
      <span class="key-dialog-icon danger"><svg class="ui-icon"><use href="#ui-power"/></svg></span>
      <div class="key-dialog-heading"><div class="key-dialog-title">Shut down CloudlessOS?</div><div class="key-dialog-sub">This safely turns off the machine.</div></div>
      <button class="key-dialog-close"><svg class="ui-icon"><use href="#ui-close"/></svg></button>
    </div>
    <div class="key-dialog-body power-dialog-body">
      <div class="power-dialog-summary">
        <div class="power-dialog-summary-title">All running apps and models will stop.</div>
        <div class="power-dialog-summary-detail"><svg class="ui-icon"><use href="#ui-check"/></svg><span>Your installed apps, models, and settings will be kept.</span></div>
      </div>
      <div class="key-dialog-actions"><button class="btn">Cancel</button><button class="btn danger-solid"><svg class="ui-icon"><use href="#ui-power"/></svg><span>Shut down</span></button></div>
    </div>
  </div>
</div>
'@

$update = @'
<div class="overlay" style="position:static;min-height:900px">
  <div class="uc">
    <div class="lp-head"><span class="uc-head-icon"><svg class="ui-icon"><use href="#ui-download"/></svg></span><span class="uc-head-copy"><b>Update Center</b><span>CloudlessOS, inference engines, drivers and applications</span></span><button class="lp-x"><svg class="ui-icon"><use href="#ui-close"/></svg></button></div>
    <div class="uc-body">
      <div class="uc-hero"><span class="uc-hero-mark"><svg class="ui-icon"><use href="#ui-download"/></svg></span><div class="uc-hero-copy"><h2>2 updates available</h2><p>Review each available system, engine or application update below.</p><div class="uc-hero-meta">LAST CHECKED JUST NOW</div></div><button class="btn uc-check-button"><svg class="ui-icon"><use href="#ui-reload"/></svg><span>Check again</span></button></div>
      <section class="uc-section"><div class="uc-section-head"><span class="uc-section-title"><b>System</b><span>Signed CloudlessOS packages and platform-owned drivers</span></span></div><div class="uc-grid uc-system-grid">
        <article class="uc-card"><div class="uc-card-top"><span class="uc-card-icon"><svg class="ui-icon"><use href="#ui-download"/></svg></span><span class="uc-card-copy"><b>CloudlessOS 0.2.7</b><span>INSTALLED 0.2.6</span></span><span class="uc-state available">AVAILABLE</span></div><div class="uc-card-message">Reliability and interface improvements are ready.</div><ul class="uc-changes"><li>More durable recipe operations</li><li>Safer display changes</li></ul><div class="uc-card-actions"><button class="btn primary"><svg class="ui-icon"><use href="#ui-download"/></svg><span>Install update</span></button></div></article>
        <article class="uc-card"><div class="uc-card-top"><span class="uc-card-icon"><svg class="ui-icon"><use href="#ui-machine"/></svg></span><span class="uc-card-copy"><b>NVIDIA driver</b><span>VERSION 610.74</span></span><span class="uc-state ready">CURRENT</span></div><div class="uc-card-message">The recommended driver for this platform is installed.</div></article>
      </div></section>
      <section class="uc-section"><div class="uc-section-head"><span class="uc-section-title"><b>Inference engines</b><span>Only engines with an available update appear here</span></span></div><div class="uc-grid"><article class="uc-card full active"><div class="uc-card-top"><span class="uc-card-icon"><svg class="ui-icon"><use href="#ui-models"/></svg></span><span class="uc-card-copy"><b>SGLang</b><span>ACTIVE ENGINE</span></span><span class="uc-state available">UPDATE</span></div><div class="uc-card-message">A newer validated runtime is available for DGX Spark.</div><div class="uc-card-actions"><button class="btn primary">Update engine</button></div></article></div></section>
    </div>
  </div>
</div>
'@

$recipe = @'
<div class="overlay" style="position:static;min-height:940px">
  <div class="mm">
    <div class="lp-head mm-head"><span class="mm-head-icon"><svg class="ui-icon"><use href="#ui-models"/></svg></span><div class="mm-head-copy"><div class="lp-ttl">Model Manager</div><div class="mm-head-sub">Choose what powers your local AI</div></div><button class="lp-x"><svg class="ui-icon"><use href="#ui-close"/></svg></button></div>
    <div class="mm-body">
      <section class="recipe-intro"><div class="recipe-intro-copy"><span class="recipe-section-icon"><svg class="ui-icon"><use href="#ui-recipes"/></svg></span><div><div class="recipe-heading-row"><h3>Recipe Library</h3><span>2</span></div><p>Manage inference recipes saved on this machine.</p></div></div><div class="recipe-toolbar"><button class="btn"><svg class="ui-icon"><use href="#ui-download"/></svg><span>Import recipe</span></button><button class="btn primary"><svg class="ui-icon"><use href="#ui-plus"/></svg><span>New recipe</span></button></div></section>
      <div class="recipe-controls"><label class="recipe-search"><svg class="ui-icon"><use href="#ui-search"/></svg><input placeholder="Search saved recipes"></label><div class="recipe-filter-grid"><label class="recipe-select"><span>Platform</span><select><option>All platforms</option></select></label><label class="recipe-select"><span>Runtime</span><select><option>All runtimes</option></select></label><label class="recipe-select"><span>Topology</span><select><option>All topologies</option></select></label><label class="recipe-select"><span>Status</span><select><option>All statuses</option></select></label></div></div>
      <div class="recipe-grid">
        <article class="recipe-card"><div class="recipe-main"><span class="recipe-icon"><svg class="ui-icon"><use href="#ui-recipes"/></svg></span><div class="recipe-copy"><div class="recipe-eyebrow"><span>Cloudless native</span><span>Updated today</span></div><div class="recipe-title-row"><span class="recipe-title">DeepSeek V4 Flash · Dual Spark</span><span class="recipe-state ready">READY</span></div><div class="recipe-desc">Partitioned inference across two DGX Sparks with a stable Cloudless endpoint.</div><div class="recipe-specs"><div><span>Model</span><b>DeepSeek-V4-Flash</b></div><div><span>Runtime</span><b>vLLM</b></div><div><span>Topology</span><b>2 nodes · TP 2</b></div><div><span>Context</span><b>1,000,000 tokens</b></div></div><details class="recipe-preflight pass"><summary><svg class="ui-icon"><use href="#ui-check"/></svg><b>Checks passed for this revision</b><small>JUST NOW</small></summary></details></div><div class="recipe-side"><div class="recipe-actions"><button class="btn primary recipe-primary-action"><svg class="ui-icon"><use href="#ui-rocket"/></svg><span>Run recipe</span></button><button class="btn"><svg class="ui-icon"><use href="#ui-search"/></svg><span>Check</span></button><button class="btn"><svg class="ui-icon"><use href="#ui-settings"/></svg><span>Edit</span></button><button class="btn recipe-remove"><svg class="ui-icon"><use href="#ui-trash"/></svg><span>Remove</span></button></div></div></div></article>
      </div>
    </div>
  </div>
</div>
'@

$cluster = @'
<section class="app-surface" style="position:static;height:900px">
  <div class="app-surface-bar"><button class="app-surface-back"><svg class="ui-icon"><use href="#ui-arrow-left"/></svg><span>DGX Dashboard</span></button><div class="app-surface-identity"><span class="app-surface-glyph"><svg class="ui-icon"><use href="#ui-network"/></svg></span><span class="app-surface-title">Spark Cluster</span></div><button class="app-surface-action"><svg class="ui-icon"><use href="#ui-close"/></svg></button></div>
  <div class="cluster-surface-content"><div class="cluster-page">
    <div class="inf-page-hero cluster-hero"><span class="inf-page-icon"><svg class="ui-icon"><use href="#ui-network"/></svg></span><div class="inf-page-copy"><div class="inf-page-eyebrow">NVIDIA DGX Spark</div><div class="inf-page-title">Spark cluster</div><div class="inf-page-desc">Connect two to eight Sparks for distributed AI. Management Wi-Fi or Ethernet remains unchanged.</div></div><span class="inf-state-pill">NOT CONFIGURED</span></div>
    <div class="cluster-progress-head"><div class="cluster-steps"><div class="cluster-step done"><i><svg class="ui-icon"><use href="#ui-check"/></svg></i><span class="cluster-step-copy"><b>Choose Spark</b><span>Find the other computer</span></span></div><div class="cluster-step active"><i>2</i><span class="cluster-step-copy"><b>Connect cable</b><span>Plug in and test</span></span></div><div class="cluster-step"><i>3</i><span class="cluster-step-copy"><b>Finish setup</b><span>Review and connect</span></span></div></div><span class="cluster-time">About 2 minutes</span></div>
    <section class="machine-card"><div class="cluster-cable-guide"><div class="cluster-cable-title">Connect the high-speed cable</div><div class="cluster-cable-intro">Use one supported QSFP112 cable between the dedicated ConnectX-7 ports on both Sparks.</div><div class="cluster-cable-visual"><div class="cluster-spark-device"><strong>This Spark</strong><div class="cluster-spark-back"><span class="cluster-spark-port connected"></span></div></div><div class="cluster-cable-route"><span>200 Gbps private link</span><i class="cluster-cable-flow"></i></div><div class="cluster-spark-device"><strong>Second Spark</strong><div class="cluster-spark-back"><span class="cluster-spark-port connected"></span></div></div></div><label class="cluster-cable-confirm"><input type="checkbox" checked><span><b>The cable is firmly connected at both ends</b><span>Cloudless will verify the real interface and packet path next.</span></span></label></div><div class="cluster-actions"><button class="btn">Back</button><button class="btn primary"><svg class="ui-icon"><use href="#ui-search"/></svg><span>Check connection</span></button></div></section>
  </div></div>
</section>
'@

Shot "power-dialog" $power 760 720
Shot "update-center" $update 1280 900
Shot "recipe-library" $recipe 1280 940
Shot "cluster-wizard" $cluster 1280 900
