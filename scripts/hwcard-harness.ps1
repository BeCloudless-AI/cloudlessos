# Focused render of the Hardware card so the footer (active-model chip + Metrics
# button) is fully visible for review.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important;opacity:1!important}body{padding:34px}</style>"
$body = @"
<div class="wall"></div>
<section class="card" style="max-width:520px;margin:0 auto">
  <div class="card-head"><span class="card-ic">&#128421;</span><h2>Hardware</h2><span class="card-live"><i></i>live</span></div>
  <div class="gpu">
    <div class="gpu-name">RTX 5090<span class="idx">#0</span></div>
    <div class="stat"><div class="stat-row"><span>Memory</span><b>14.2 / 32.0 GB</b></div><div class="track"><i style="width:44%"></i></div></div>
    <div class="stat"><div class="stat-row"><span>Utilization</span><b>61%</b></div><div class="track"><i class="mid" style="width:61%"></i></div></div>
    <div class="chips"><span>58&deg;C</span><span><b>412 / 600 W</b></span><span>Driver 595.79</span></div>
  </div>
  <div class="hw-foot">
    <button class="hw-link">
      <span class="hw-link-dot on"></span>
      <span class="hw-link-tx"><span class="hw-link-k">Loaded in vLLM</span><span class="hw-link-v">Qwen2.5-1.5B-Instruct</span></span>
      <span class="hw-link-go">&#8250;</span>
    </button>
    <button class="hw-metrics"><span class="hw-metrics-ic"><i></i><i></i><i></i></span>Metrics</button>
  </div>
</section>
"@
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze</head><body>$body</body></html>"
$tmp = "$env:TEMP\hwcard.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\hwcard.png"
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=2 --window-size=600,440 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null | Out-Null
if (Test-Path $out) { "OK $out" } else { "NO SHOT" }
