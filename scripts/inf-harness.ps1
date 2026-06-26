# Renders the Inference activity dashboard (real CSS + the real infChart canvas code)
# against sample data, to PNG via headless Edge.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
# pull the real infChart function out of index.html so we render exactly what ships
$infChart = [regex]::Match($h, '(?s)// Dependency-free area\+line chart.*?\n\}\n').Value
if (-not $infChart) { Write-Host 'could not extract infChart'; exit 1 }

$body = @'
<div class="mm" style="height:auto!important;max-height:none!important">
  <div class="lp-head"><div class="lp-ttl">Inference activity<span class="inf-eng">SGLANG</span></div><button class="lp-x">&times;</button></div>
  <div class="mm-body"><div id="inf-body">
    <div class="inf-stats">
      <div class="inf-tile accent"><div class="it-label">Decoding now</div><div class="it-val"><span id="v-run">2</span><span class="it-unit">reqs</span></div></div>
      <div class="inf-tile"><div class="it-label">Queued</div><div class="it-val"><span id="v-wait">0</span><span class="it-unit">reqs</span></div></div>
      <div class="inf-tile"><div class="it-label">KV cache</div><div class="it-val"><span id="v-kv">34</span><span class="it-unit">%</span></div></div>
      <div class="inf-tile accent"><div class="it-label">Output</div><div class="it-val"><span id="v-gtps">142</span><span class="it-unit">tok/s</span></div></div>
      <div class="inf-tile warm"><div class="it-label">Prefill</div><div class="it-val"><span id="v-ptps">0</span><span class="it-unit">tok/s</span></div></div>
      <div class="inf-tile"><div class="it-label">First token</div><div class="it-val"><span id="v-ttft">38</span><span class="it-unit">ms</span></div></div>
      <div class="inf-tile"><div class="it-label">Per token</div><div class="it-val"><span id="v-tpot">11</span><span class="it-unit">ms</span></div></div>
    </div>
    <div class="inf-charts">
      <div class="inf-card"><div class="ic-head"><span class="ic-title">throughput</span><span class="ic-now">142<small>tok/s out</small></span></div><canvas class="inf-canvas" id="c-tput"></canvas><div class="inf-legend"><span><i style="background:var(--accent)"></i>prefill</span><span><i style="background:var(--blue)"></i>decode</span></div></div>
      <div class="inf-card"><div class="ic-head"><span class="ic-title">active requests</span><span class="ic-now">2<small>running</small></span></div><canvas class="inf-canvas" id="c-active"></canvas><div class="inf-legend"><span><i style="background:var(--blue)"></i>running</span><span><i style="background:var(--muted)"></i>queued</span></div></div>
      <div class="inf-card"><div class="ic-head"><span class="ic-title">kv cache</span><span class="ic-now">34<small>%</small></span></div><canvas class="inf-canvas" id="c-kv"></canvas></div>
    </div>
  </div></div>
</div>
'@

$script = @"
<script>
const cssRgb = v => { const h = getComputedStyle(document.documentElement).getPropertyValue(v).trim().replace('#', '');
  if (h.length === 3) return [0,1,2].map(i => parseInt(h[i]+h[i],16));
  if (h.length >= 6) return [0,2,4].map(i => parseInt(h.slice(i,i+2),16)); return [130,130,130]; };
$infChart
window.addEventListener('load', () => {
  const blue = cssRgb('--blue'), accent = cssRgb('--accent'), muted = cssRgb('--muted');
  const N = 60, gen = [], pre = [], run = [], wait = [], kv = [];
  for (let i = 0; i < N; i++) {
    const t = i / 6;
    gen.push(Math.max(0, 120 + 45*Math.sin(t) + 25*Math.sin(t*2.3)));
    pre.push(i % 11 === 0 ? 600 + 300*Math.abs(Math.sin(t)) : 0);
    run.push(Math.max(0, Math.round(2 + 1.5*Math.sin(t*0.7))));
    wait.push(i % 9 === 0 ? 1 : 0);
    kv.push(28 + 9*Math.sin(t*0.5) + 3*Math.sin(t*1.7));
  }
  infChart('c-tput', [{ rgb: accent, data: pre }, { rgb: blue, data: gen }]);
  infChart('c-active', [{ rgb: blue, data: run }, { rgb: muted, data: wait }]);
  infChart('c-kv', [{ rgb: accent, data: kv }], 100);
});
</script>
"@

$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;justify-content:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$bodyCss</head><body>$body$script</body></html>"
$tmp = "$env:TEMP\inf-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\inference.png"; if (Test-Path $out) { Remove-Item $out }
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1240,780 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1400
if (Test-Path $out) { "OK $out" } else { "NO SCREENSHOT" }
