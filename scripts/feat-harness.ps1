# Renders the new feature surfaces (Model Manager "Recommended in France" + Profile
# settings page) from the REAL index.html CSS, to PNG via headless Edge.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value

# Everything below is a literal here-string; entities keep PowerShell out of the way.
# FR flag = &#x1F1EB;&#x1F1F7;  star = &#x2605;  chevron = &#x203A;  search = &#x2315;
$mm = @'
<div class="mm" style="height:auto!important;max-height:none!important">
  <div class="lp-head"><div class="lp-ttl">Model Manager</div><button class="lp-x">&times;</button></div>
  <div class="mm-body">
    <div class="mm-tabs"><button class="mm-tab on">Language models</button><button class="mm-tab">Image models</button></div>
    <div class="mm-bar"><div class="mm-sum">Your GPU: <b>31 GB</b> &middot; <span class="mm-sum-fit">green fits comfortably</span></div>
      <div class="mm-search"><span class="mm-search-ic">&#x2315;</span><input placeholder="Search language models…"></div></div>
    <div class="mm-filters"><button class="mm-chip">Fits my GPU</button><button class="mm-chip">Vision</button><button class="mm-chip">Tool-calling</button></div>
    <div class="mdl-sec">Recommended in France &#x1F1EB;&#x1F1F7;</div>
    <div class="mdl-grid">
      <button class="mdl rec fit-fits"><div class="mdl-top"><span class="mdl-name">Mistral 7B Instruct</span></div>
        <div class="mdl-meta">7B &middot; 32K ctx &middot; ~18 GB</div>
        <div class="mdl-notes">Mistral AI's open 7B — a strong, French-built assistant with excellent French.</div>
        <div class="mdl-tags"><span class="mdl-cap rec">&#x2605; recommended</span><span class="mdl-cap">tools</span><span class="mdl-cap gated">gated</span><span class="mdl-t">chat</span><span class="mdl-t">french</span></div>
        <div class="mdl-foot"><span class="mdl-fit fit-fits">fits your GPU</span><span class="mdl-use">details &#x203A;</span></div></button>
      <button class="mdl rec fit-fits"><div class="mdl-top"><span class="mdl-name">Mistral Nemo 12B</span></div>
        <div class="mdl-meta">12B &middot; 128K ctx &middot; ~28 GB</div>
        <div class="mdl-notes">Mistral's 12B with a 128K context and strong multilingual skills.</div>
        <div class="mdl-tags"><span class="mdl-cap rec">&#x2605; recommended</span><span class="mdl-cap">tools</span><span class="mdl-cap gated">gated</span><span class="mdl-t">long-context</span><span class="mdl-t">french</span></div>
        <div class="mdl-foot"><span class="mdl-fit fit-fits">fits your GPU</span><span class="mdl-use">details &#x203A;</span></div></button>
    </div>
    <div class="mdl-sec">cloudless highlights</div>
    <div class="mdl-grid">
      <button class="mdl fit-fits"><div class="mdl-top"><span class="mdl-name">Qwen2.5 7B</span></div>
        <div class="mdl-meta">7B &middot; 32K ctx &middot; ~18 GB</div>
        <div class="mdl-notes">Excellent all-rounder — the sweet spot for a 24GB+ GPU.</div>
        <div class="mdl-tags"><span class="mdl-cap">tools</span><span class="mdl-t">chat</span></div>
        <div class="mdl-foot"><span class="mdl-fit fit-fits">fits your GPU</span><span class="mdl-use">details &#x203A;</span></div></button>
      <button class="mdl fit-fits"><div class="mdl-top"><span class="mdl-name">Phi-3.5 mini</span></div>
        <div class="mdl-meta">3.8B &middot; 128K ctx &middot; ~11 GB</div>
        <div class="mdl-notes">Strong small model with a long 128K context.</div>
        <div class="mdl-tags"><span class="mdl-cap">tools</span><span class="mdl-t">reasoning</span></div>
        <div class="mdl-foot"><span class="mdl-fit fit-fits">fits your GPU</span><span class="mdl-use">details &#x203A;</span></div></button>
    </div>
  </div>
</div>
<div class="set" style="height:auto!important;width:min(720px,96vw)">
  <div class="set-main"><div class="set-topbar"><div class="set-page-title">Profile</div></div>
  <div class="set-content">
    <div class="set-block"><h3>profile</h3>
      <div class="fld"><span class="fld-label">Your name</span><input class="fld-input" value="Samuel"></div>
      <div class="fld-help">Used to greet you on the home screen. Stays on this machine.</div>
      <div class="fld"><span class="fld-label">Region</span><select class="fld-input"><option>Auto-detect (France)</option></select></div>
      <div class="fld-help">Cloudless tailors recommendations to your region — e.g. it highlights Mistral's French-built models in France. Leave on Auto-detect to follow the machine.</div>
      <div class="set-row" style="margin-top:6px"><span class="row-actions"><button class="btn primary">Save</button></span></div>
    </div>
    <div class="set-block"><h3>location &amp; time &#x1F1EB;&#x1F1F7;</h3>
      <div class="set-row"><span class="srow-label">region</span><span><b>France</b> <span class="set-hint" style="margin:0">&middot; from system language</span></span></div>
      <div class="set-row"><span class="srow-label">timezone</span><span>Europe/Paris <span class="set-hint" style="margin:0">(UTC +02:00)</span></span></div>
      <div class="set-row"><span class="srow-label">language</span><span>fr_FR.UTF-8</span></div>
      <div class="fld-help">Detected locally from your system clock and language — nothing is looked up over the internet.</div>
      <div class="md-banner rec" style="margin-top:10px">France detected — Mistral models are recommended in the Model Manager.</div>
    </div>
  </div></div>
</div>
'@

$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;flex-direction:column;gap:26px;align-items:flex-start}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$mm</body></html>"
$tmp = "$env:TEMP\feat-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\feat.png"; if (Test-Path $out) { Remove-Item $out }
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1320,1560 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1200
if (Test-Path $out) { "OK $out" } else { "NO SCREENSHOT" }
