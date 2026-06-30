# Renders the Inference -> Power tab (real CSS) with the range toggle, for Day + Month views.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value

$ranges = 'hour','day','month','year'
function RangeToggle($active) {
  ($ranges | ForEach-Object { $on = if ($_ -eq $active) { ' on' } else { '' }; $lbl = $_.Substring(0,1).ToUpper() + $_.Substring(1); "<button class=`"us-rbtn$on`">$lbl</button>" }) -join ''
}
function Tile($label, $val, $sub) { "<div class=`"us-tile`"><div class=`"us-label`">$label</div><div class=`"us-val`">$val</div>" + $(if ($sub) { "<div class=`"us-sub`">$sub</div>" } else { '' }) + "</div>" }

# ---- Day view: 30 energy bars, a couple empty, one peak ----
$dayBars = ''
for ($i = 0; $i -lt 30; $i++) {
  $v = [math]::Round((18 + 60 * [math]::Abs([math]::Sin($i / 4.0)) + 16 * [math]::Abs([math]::Sin($i / 1.7))))
  $cls = 'us-bar pw-bar'
  if ($i -lt 2) { $v = 2; $cls = 'us-bar pw-bar pw-empty' }       # no-data days
  elseif ($i -eq 23) { $v = 100; $cls = 'us-bar pw-bar peak' }    # peak day
  $dayBars += "<div class=`"$cls`" style=`"height:$v%`"></div>"
}
$dayAxis = '<div class="us-axis us-axis-ends"><span>Jun 1</span><span>Jun 30</span></div>'
$dayTiles = (Tile 'This view' '14.2 kWh' 'last 30 days') + (Tile 'Lifetime total' '286 kWh' 'all recorded') + (Tile 'Now' '312 W' 'current draw') + (Tile 'Avg power' '243 W' 'while sampled') + (Tile 'Peak power' '598 W' 'highest reading') + (Tile 'Peak day' '1.21 kWh <span style="font-size:12px;font-weight:600;color:var(--muted)">Jun 24</span>' 'energy')

# ---- Month view: 12 bars, per-bar axis ----
$months = 'Jul','Aug','Sep','Oct','Nov','Dec','Jan','Feb','Mar','Apr','May','Jun'
$monBars = ''; $monAxis = '<div class="us-axis">'
for ($i = 0; $i -lt 12; $i++) {
  $v = [math]::Round((24 + 56 * [math]::Abs([math]::Sin($i / 3.1)) + 14 * [math]::Abs([math]::Sin($i / 1.4))))
  $cls = 'us-bar pw-bar'; if ($i -eq 8) { $v = 100; $cls = 'us-bar pw-bar peak' }
  $monBars += "<div class=`"$cls`" style=`"height:$v%`"></div>"
  $monAxis += "<span>$($months[$i])</span>"
}
$monAxis += '</div>'
$monTiles = (Tile 'This view' '286 kWh' 'last 12 months') + (Tile 'Lifetime total' '286 kWh' 'all recorded') + (Tile 'Now' '312 W' 'current draw') + (Tile 'Avg power' '251 W' 'while sampled') + (Tile 'Peak power' '604 W' 'highest reading') + (Tile 'Peak month' '38.4 kWh <span style="font-size:12px;font-weight:600;color:var(--muted)">Mar</span>' 'energy')

function Card($title, $sub, $active, $bars, $axis, $tiles, $unit) {
@"
<div id="inf-panel" style="margin-bottom:30px">
  <div class="us-chartwrap">
    <div class="us-head">
      <div><span class="us-htitle">electricity / $title</span> <span class="us-time">$sub</span></div>
      <div class="us-range">$(RangeToggle $active)</div>
    </div>
    <div class="us-chart">$bars</div>
    $axis
  </div>
  <div class="us-grid">$tiles</div>
  <div class="pw-actions">
    <span class="pw-erasehint">Click a bar to erase that $unit.</span>
    <button class="btn danger pw-clear">Clear all electricity logs</button>
  </div>
  <div class="fld-help" style="margin-top:14px">Electricity is the GPUs' board power (via nvidia-smi), sampled continuously and integrated into watt-hours &mdash; it records the machine's AI energy use even while idle. Erasing a day also corrects its month and year totals. <b>Cost isn't shown</b> &mdash; it depends on your electricity tariff.</div>
</div>
"@
}

$body = @"
<div class="mm" style="height:auto!important;max-height:none!important">
  <div class="lp-head"><div class="lp-ttl">Inference</div>
    <div class="inf-head on"><span class="inf-live"></span><b>vLLM</b><span class="sep">&middot;</span>Qwen2.5-1.5B-Instruct<span class="ih-state">live</span></div>
    <button class="lp-x">&times;</button></div>
  <div class="mm-body"><div id="inf-body">
    <div class="mm-tabs"><button class="mm-tab">Activity</button><button class="mm-tab">Usage</button><button class="mm-tab on">Power</button><button class="mm-tab">Engine</button><button class="mm-tab">API access</button></div>
    $(Card 'day' 'last 30 days' 'day' $dayBars $dayAxis $dayTiles 'day')
    $(Card 'month' 'last 12 months' 'month' $monBars $monAxis $monTiles 'month')
  </div></div>
</div>
"@
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;justify-content:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$body</body></html>"
$tmp = "$env:TEMP\power-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\power-tab.png"
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1240,1180 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1000
if (Test-Path $out) { "OK $out" } else { "NO SHOT" }
