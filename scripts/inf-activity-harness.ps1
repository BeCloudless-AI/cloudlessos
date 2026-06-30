# Renders the Activity tab's historical view (Live/Hour/Day/Month/Year selector + throughput chart).
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value

$aranges = 'live','hour','day','month','year'
function ActToggle($active) {
  ($aranges | ForEach-Object { $on = if ($_ -eq $active) { ' on' } else { '' }; $lbl = $_.Substring(0,1).ToUpper() + $_.Substring(1); "<button class=`"us-rbtn$on`">$lbl</button>" }) -join ''
}
function Tile($label, $val, $sub) { "<div class=`"us-tile`"><div class=`"us-label`">$label</div><div class=`"us-val`">$val</div>" + $(if ($sub) { "<div class=`"us-sub`">$sub</div>" } else { '' }) + "</div>" }

# Day historical: 30 token bars, ends axis
$dayBars = ''
for ($i = 0; $i -lt 30; $i++) {
  $v = [math]::Round((18 + 64 * [math]::Abs([math]::Sin($i / 4.3)) + 16 * [math]::Abs([math]::Sin($i / 1.9))))
  $peak = ($i -eq 19); if ($peak) { $v = 100 }
  $cls = if ($peak) { 'us-bar peak' } else { 'us-bar' }
  $dayBars += "<div class=`"$cls`" style=`"height:$v%`"></div>"
}
$dayAxis = '<div class="us-axis us-axis-ends"><span>May 30</span><span>Jun 28</span></div>'
$dayTiles = (Tile 'Output tokens' '3.53M' 'generated &middot; last 30 days') + (Tile 'Input tokens' '5.21M' 'prompt') + (Tile 'Requests' '12.4k' 'last 30 days') + (Tile 'Busiest day' '243k <span style="font-size:12px;font-weight:600;color:var(--muted)">Jun 18</span>' 'output tokens')

# Month historical: 12 bars + per-bar axis
$months = 'Jul','Aug','Sep','Oct','Nov','Dec','Jan','Feb','Mar','Apr','May','Jun'
$monBars = ''; $monAxis = '<div class="us-axis">'
for ($i = 0; $i -lt 12; $i++) {
  $v = [math]::Round((22 + 58 * [math]::Abs([math]::Sin($i / 3.1)) + 14 * [math]::Abs([math]::Sin($i / 1.4))))
  $peak = ($i -eq 8); if ($peak) { $v = 100 }
  $cls = if ($peak) { 'us-bar peak' } else { 'us-bar' }
  $monBars += "<div class=`"$cls`" style=`"height:$v%`"></div>"
  $monAxis += "<span>$($months[$i])</span>"
}
$monAxis += '</div>'
$monTiles = (Tile 'Output tokens' '41.2M' 'generated &middot; last 12 months') + (Tile 'Input tokens' '60.8M' 'prompt') + (Tile 'Requests' '148k' 'last 12 months') + (Tile 'Busiest month' '6.1M <span style="font-size:12px;font-weight:600;color:var(--muted)">Mar</span>' 'output tokens')

function Section($active, $title, $sub, $bars, $axis, $tiles) {
@"
<div id="inf-panel" style="margin-bottom:34px">
  <div class="inf-actbar"><div class="us-range">$(ActToggle $active)</div></div>
  <div id="inf-act">
    <div class="us-chartwrap">
      <div class="us-head"><div><span class="us-htitle">$title</span> <span class="us-time">$sub</span></div></div>
      <div class="us-chart">$bars</div>
      $axis
    </div>
    <div class="inf-grid">$tiles</div>
    <div class="fld-help" style="margin-top:16px"><b>Live</b> streams real-time throughput; the ranges show output tokens the engine generated per period.</div>
  </div>
</div>
"@
}

$body = @"
<div class="mm" style="height:auto!important;max-height:none!important">
  <div class="lp-head"><div class="lp-ttl">Inference</div>
    <div class="inf-head on"><span class="inf-live"></span><b>vLLM</b><span class="sep">&middot;</span>Qwen2.5-1.5B-Instruct<span class="ih-state">live</span></div>
    <button class="lp-x">&times;</button></div>
  <div class="mm-body"><div id="inf-body">
    <div class="mm-tabs"><button class="mm-tab on">Activity</button><button class="mm-tab">Usage</button><button class="mm-tab">Engine</button><button class="mm-tab">API access</button></div>
    $(Section 'day' 'output tokens / day' 'last 30 days' $dayBars $dayAxis $dayTiles)
    $(Section 'month' 'output tokens / month' 'last 12 months' $monBars $monAxis $monTiles)
  </div></div>
</div>
"@
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;justify-content:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$body</body></html>"
$tmp = "$env:TEMP\inf-activity.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\inf-activity.png"
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1240,1040 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1000
if (Test-Path $out) { "OK $out" } else { "NO SHOT" }
