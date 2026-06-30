# Renders the Inference -> Usage tab (real CSS) with the range toggle, for Day + Month views.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value

$ranges = 'hour','day','month','year'
function RangeToggle($active) {
  ($ranges | ForEach-Object { $on = if ($_ -eq $active) { ' on' } else { '' }; $lbl = $_.Substring(0,1).ToUpper() + $_.Substring(1); "<button class=`"us-rbtn$on`">$lbl</button>" }) -join ''
}
function Tile($label, $val, $sub) { "<div class=`"us-tile`"><div class=`"us-label`">$label</div><div class=`"us-val`">$val</div>" + $(if ($sub) { "<div class=`"us-sub`">$sub</div>" } else { '' }) + "</div>" }

# ---- Day view: 30 bars, ends axis ----
$dayBars = ''
for ($i = 0; $i -lt 30; $i++) {
  $v = [math]::Round((15 + 70 * [math]::Abs([math]::Sin($i / 4.0)) + 18 * [math]::Abs([math]::Sin($i / 1.7))))
  $peak = ($i -eq 21); if ($peak) { $v = 100 }
  $cls = if ($peak) { 'us-bar peak' } else { 'us-bar' }
  $dayBars += "<div class=`"$cls`" style=`"height:$v%`"></div>"
}
$dayAxis = '<div class="us-axis us-axis-ends"><span>May 30</span><span>Jun 28</span></div>'

# ---- Month view: 12 bars, per-bar axis labels ----
$months = 'Jul','Aug','Sep','Oct','Nov','Dec','Jan','Feb','Mar','Apr','May','Jun'
$monBars = ''; $monAxis = '<div class="us-axis">'
for ($i = 0; $i -lt 12; $i++) {
  $v = [math]::Round((20 + 60 * [math]::Abs([math]::Sin($i / 3.0)) + 15 * [math]::Abs([math]::Sin($i / 1.3))))
  $peak = ($i -eq 9); if ($peak) { $v = 100 }
  $cls = if ($peak) { 'us-bar peak' } else { 'us-bar' }
  $monBars += "<div class=`"$cls`" style=`"height:$v%`"></div>"
  $monAxis += "<span>$($months[$i])</span>"
}
$monAxis += '</div>'

$tilesDay = (Tile 'Total requests' '12.4k' '') + (Tile 'Total tokens' '8.74M' '') + (Tile 'Input tokens' '5.21M' 'prompt') + (Tile 'Output tokens' '3.53M' 'completion') + (Tile 'Avg in / request' '420' '') + (Tile 'Avg out / request' '284' '') + (Tile 'Peak day' '1.2k <span style="font-size:12px;font-weight:600;color:var(--muted)">Jun 22</span>' '') + (Tile 'Success rate' '99%' '1.1k API requests') + (Tile 'Cache hits' '2.10M' '38% hit rate') + (Tile 'Cache misses' '3.41M' '') + (Tile 'Last request' '12 min ago' '')
$tilesMon = (Tile 'Total requests' '12.4k' '') + (Tile 'Total tokens' '8.74M' '') + (Tile 'Input tokens' '5.21M' 'prompt') + (Tile 'Output tokens' '3.53M' 'completion') + (Tile 'Avg in / request' '420' '') + (Tile 'Avg out / request' '284' '') + (Tile 'Peak month' '6.8k <span style="font-size:12px;font-weight:600;color:var(--muted)">Apr</span>' '') + (Tile 'Success rate' '99%' '1.1k API requests') + (Tile 'Cache hits' '2.10M' '38% hit rate') + (Tile 'Cache misses' '3.41M' '') + (Tile 'Last request' '12 min ago' '')

function Card($title, $sub, $active, $bars, $axis, $tiles) {
@"
<div id="inf-panel" style="margin-bottom:30px">
  <div class="us-chartwrap">
    <div class="us-head">
      <div><span class="us-htitle">$title</span> <span class="us-time">$sub</span></div>
      <div class="us-range">$(RangeToggle $active)</div>
    </div>
    <div class="us-chart">$bars</div>
    $axis
  </div>
  <div class="us-grid">$tiles</div>
</div>
"@
}

$body = @"
<div class="mm" style="height:auto!important;max-height:none!important">
  <div class="lp-head"><div class="lp-ttl">Inference</div>
    <div class="inf-head on"><span class="inf-live"></span><b>vLLM</b><span class="sep">&middot;</span>Qwen2.5-1.5B-Instruct<span class="ih-state">live</span></div>
    <button class="lp-x">&times;</button></div>
  <div class="mm-body"><div id="inf-body">
    <div class="mm-tabs"><button class="mm-tab">Activity</button><button class="mm-tab on">Usage</button><button class="mm-tab">Engine</button><button class="mm-tab">API access</button></div>
    $(Card 'daily requests' 'last 30 days' 'day' $dayBars $dayAxis $tilesDay)
    $(Card 'monthly requests' 'last 12 months' 'month' $monBars $monAxis $tilesMon)
  </div></div>
</div>
"@
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;justify-content:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$body</body></html>"
$tmp = "$env:TEMP\usage-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\usage-ranges.png"
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1240,1180 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1000
if (Test-Path $out) { "OK $out" } else { "NO SHOT" }
