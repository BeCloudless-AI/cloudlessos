# Renders the redesigned Inference Activity tab (real CSS + real infChart/infGauge)
# against sample data, to PNG via headless Edge.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
$cssRgb = [regex]::Match($h, '(?s)const cssRgb = v => \{.*?return \[130, 130, 130\]; \};').Value
$infChart = [regex]::Match($h, '(?s)// Dependency-free smoothed area\+line chart.*?\n\}\n').Value
$infGauge = [regex]::Match($h, '(?s)// A radial gauge.*?\n\}\n').Value
if (-not ($cssRgb -and $infChart -and $infGauge)) { Write-Host "extract failed"; exit 1 }

$body = @'
<div class="mm" style="height:auto!important;max-height:none!important">
  <div class="lp-head"><div class="lp-ttl">Inference</div>
    <div class="inf-head on"><span class="inf-live"></span><b>SGLang</b><span class="sep">&middot;</span>Qwen2.5-1.5B-Instruct<span class="ih-state">live</span></div>
    <button class="lp-x">&times;</button></div>
  <div class="mm-body"><div id="inf-body"><div id="inf-panel">
    <div class="inf-hero">
      <div class="inf-hero-head">
        <div><div class="inf-hero-val"><span>142</span><small>tokens / sec</small></div><div class="inf-hero-cap">output generation speed</div></div>
        <div class="inf-hero-aux"><div>peak <b>213</b> tok/s</div><div>prompt <b>0</b> tok/s</div></div>
      </div>
      <canvas class="inf-hero-canvas" id="c-tput"></canvas>
      <div class="inf-hero-foot"><div class="inf-legend"><span><i style="background:var(--blue)"></i>output tokens / second</span></div><span class="inf-time">last 60 seconds</span></div>
    </div>
    <div class="inf-grid">
      <div class="inf-metric"><div class="im-label">Active requests</div><div class="im-val"><span>2</span></div><div class="im-sub">1 queued</div><canvas class="im-spark" id="c-active"></canvas></div>
      <div class="inf-metric"><div class="im-label">Memory &middot; KV cache</div><div class="im-ringwrap"><canvas class="im-ring" id="c-kv"></canvas><span class="im-ringval">34%</span></div></div>
      <div class="inf-metric"><div class="im-label">Time to first token</div><div class="im-val"><span>38</span><small>ms</small></div><div class="im-sub">how fast a reply starts</div><canvas class="im-spark" id="c-ttft"></canvas></div>
      <div class="inf-metric"><div class="im-label">Per output token</div><div class="im-val"><span>11</span><small>ms</small></div><div class="im-sub">speed of each token</div><canvas class="im-spark" id="c-tpot"></canvas></div>
    </div>
  </div></div></div>
</div>
'@

$script = @"
<script>
$cssRgb
$infGauge
$infChart
window.addEventListener('load', () => {
  const blue = cssRgb('--blue'), accent = cssRgb('--accent'), muted = cssRgb('--muted');
  const N = 60, gen = [], run = [], ttft = [], tpot = [];
  for (let i = 0; i < N; i++) { const t = i / 6;
    gen.push(Math.max(0, 135 + 55*Math.sin(t) + 22*Math.sin(t*2.3)));
    run.push(Math.max(0, Math.round(2 + 1.4*Math.sin(t*0.7))));
    ttft.push(34 + 7*Math.sin(t*0.4) + (i % 17 === 0 ? 12 : 0));
    tpot.push(10.5 + 2*Math.sin(t*0.6)); }
  infChart('c-tput', [{ rgb: blue, data: gen }], 0, true);
  infChart('c-active', [{ rgb: blue, data: run }]);
  infChart('c-ttft', [{ rgb: muted, data: ttft }]);
  infChart('c-tpot', [{ rgb: muted, data: tpot }]);
  infGauge('c-kv', 34, blue);
});
</script>
"@

$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;justify-content:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$bodyCss</head><body>$body$script</body></html>"
$tmp = "$env:TEMP\inf-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\inference.png"; if (Test-Path $out) { Remove-Item $out }
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1240,760 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1500
if (Test-Path $out) { "OK $out" } else { "NO SCREENSHOT" }
