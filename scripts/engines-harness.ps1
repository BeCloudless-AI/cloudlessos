# Renders the engine comparison (Inference -> Engine tab) and the dashboard Hardware
# card (GPU + CPU + RAM) from the REAL index.html CSS, to PNG via headless Edge.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
function Traits($arr) { "<ul class=`"ec-traits`">" + (($arr | ForEach-Object { "<li>$_</li>" }) -join '') + "</ul>" }
$vllm = Traits @('Highest throughput when the model fits in VRAM', 'Full-precision Hugging Face models', 'Needs enough GPU memory')
$sgl = Traits @('High throughput &amp; low latency (RadixAttention)', 'Great at structured output and tool calling', 'Full-precision Hugging Face models')
$llama = Traits @('Offloads to system RAM &mdash; run models bigger than your VRAM', 'Works on CPU alone, or CPU + GPU together', 'Uses compact GGUF model files')

$engine = @"
<div class="mm" style="height:auto!important;max-height:none!important;margin-bottom:26px">
  <div class="lp-head"><div class="lp-ttl">Inference</div><button class="lp-x">&times;</button></div>
  <div class="mm-body">
    <div class="mm-tabs"><button class="mm-tab">Activity</button><button class="mm-tab on">Engine</button><button class="mm-tab">API access</button></div>
    <div style="max-width:780px;margin:0 auto">
      <div class="set-block"><h3>inference engine</h3>
        <div class="ec-bar"><span class="fld-help" style="margin:0">One engine runs at a time and powers everything &mdash; chat, apps and the API. Switching briefly reloads the model; pick the model itself in the Model Manager.</span><span class="er-state ready">ready</span></div>
        <div class="ec-grid">
          <div class="ec"><div class="ec-head"><span class="ec-ic">&#x1F680;</span><span class="ec-name">vLLM</span><span class="ec-runs">GPU</span></div><div class="ec-best">Maximum speed on a capable GPU</div>$vllm<div class="ec-foot"><button class="btn">Use this engine</button></div></div>
          <div class="ec on"><div class="ec-head"><span class="ec-ic">&#x1F9E9;</span><span class="ec-name">SGLang</span><span class="ec-runs">GPU</span></div><div class="ec-best">Fast GPU serving, strong for agents</div>$sgl<div class="ec-foot"><span class="ec-active">&#9679; Active</span></div></div>
          <div class="ec"><div class="ec-head"><span class="ec-ic">&#x1F999;</span><span class="ec-name">llama.cpp</span><span class="ec-runs">CPU + GPU</span></div><div class="ec-best">Runs anywhere &mdash; even low on VRAM</div>$llama<div class="ec-foot"><button class="btn">Use this engine</button></div></div>
        </div>
      </div>
    </div>
  </div>
</div>
"@

$card = @'
<section class="card" style="width:380px">
  <h2>Hardware</h2>
  <div id="gpu-body"><div class="gpu"><div class="gpu-name">NVIDIA GeForce RTX 5090<span class="idx">#0</span></div>
    <div class="stat"><div class="stat-row"><span>Memory</span><b>8.1 / 32.0 GB</b></div><div class="track"><i style="width:25%"></i></div></div>
    <div class="stat"><div class="stat-row"><span>Utilization</span><b>12%</b></div><div class="track"><i style="width:12%"></i></div></div>
    <div class="chips"><span>45&deg;C</span><span><b>230 / 600 W</b></span><span>Driver 595.79</span></div></div></div>
  <div id="sys-body"><div class="sys-sec"><div class="gpu-name">AMD Ryzen 9 9900X<span class="idx">24 threads</span></div>
    <div class="stat"><div class="stat-row"><span>cpu</span><b>5%</b></div><div class="track"><i style="width:5%"></i></div></div>
    <div class="stat"><div class="stat-row"><span>memory</span><b>6.3 / 15.2 GB</b></div><div class="track"><i style="width:42%"></i></div></div></div></div>
  <button class="card-link">&#9672; Qwen2.5-1.5B &middot; loaded &rarr;</button>
  <button class="card-link">&#9637; Inference &rarr;</button>
</section>
'@

$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:#e8e8e7;padding:26px;display:flex;flex-direction:column;align-items:center;gap:24px}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$engine$card</body></html>"
$tmp = "$env:TEMP\engines-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\engines.png"; if (Test-Path $out) { Remove-Item $out }
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1240,1020 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1200
if (Test-Path $out) { "OK $out" } else { "NO SCREENSHOT" }
