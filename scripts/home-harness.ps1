# Renders the redesigned home (real CSS) populated with representative content:
# frosted menubar, hero + ask bar, app grid, glass Hardware + Places cards.
param(
  [int]$Width = 1320,
  [int]$Height = 940,
  [string]$Name = 'home-render'
)

$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
$sprite = [regex]::Match($h, '(?s)<!-- Cloudless brand mark.*?</svg>').Value

$apps = @(
  @('Cloudless AI','&#128172;','&#9679; running'),
  @('ComfyUI','&#127912;','image gen'),
  @('Open WebUI','&#128172;','chat'),
  @('Ollama','&#129302;',''),
  @('Jupyter','&#128200;',''),
  @('Stable Diffusion','&#10024;','')
)
$tiles = ($apps | ForEach-Object {
  $running = if ($_[2] -like '*running*') { ' running' } else { '' }
  $stop = if ($running) { '<button class="stopx" style="display:grid"><svg class="ui-icon sm"><use href="#ui-close"/></svg></button>' } else { '' }
  "<div class=`"tile$running`"><div class=`"ic`">$($_[1])</div>$stop<div class=`"tile-copy`"><div class=`"nm`">$($_[0])</div><div class=`"meta`">$($_[2])</div></div><div class=`"tbar`"><i></i></div></div>"
}) -join ''

$body = @"
<div class="wall"></div>
$sprite
<div class="menubar">
  <div class="brand"><svg class="mark cl-mark"><use href="#cl-star"/></svg> cloudless</div>
  <div class="spacer"></div>
  <div class="mi"><span class="led ok"></span> Ready</div>
  <div class="mi mi-net"><span class="led net-online"></span> Network</div>
  <div id="clock">19:53</div>
  <button class="gear shutdown"><svg class="ui-icon"><use href="#ui-power"/></svg></button>
</div>
<main>
  <div class="hero">
    <div class="greet-row">
      <div class="greet-txt"><div class="hero-kicker">Private AI control plane</div><h1>Good evening, Samuel</h1><p>Your models, apps, and data. Running here, on your hardware.</p></div>
      <div class="hero-time"><div class="ht-clock"><span id="big-clock">19:53</span><span class="accent-dot"></span></div><div class="hero-date">Tuesday, 30 June</div><div class="hero-zone">Europe/Paris</div></div>
    </div>
    <form class="ask"><span class="ask-spark"></span><input placeholder="What do you want to do with CloudlessOS?"><button class="ask-go" type="button"><svg class="ui-icon"><use href="#ui-arrow-right"/></svg></button></form>
    <button class="ghost-chat">Open the full chat &#8599;</button>
  </div>
  <section class="apps-sec">
    <div class="sec-row"><h3 class="sec">your apps</h3><button class="sec-act"><svg class="ui-icon sm"><use href="#ui-apps"/></svg>All apps</button></div>
    <div class="pad">$tiles</div>
  </section>
  <div class="cards">
    <section class="card">
      <div class="card-head"><span class="card-ic">&#128421;</span><h2>Hardware</h2><span class="card-live"><i></i>live</span></div>
      <div id="gpu-body"><div class="gpu">
        <div class="gpu-name">RTX 5090<span class="idx">#0</span></div>
        <div class="stat"><div class="stat-row"><span>Memory</span><b>14.2 / 32.0 GB</b></div><div class="track"><i style="width:44%"></i></div></div>
        <div class="stat"><div class="stat-row"><span>Utilization</span><b>91%</b></div><div class="track"><i class="hi" style="width:91%"></i></div></div>
        <div class="chips"><span>58&deg;C</span><span><b>412 / 600 W</b></span><span>Driver 595.79</span></div>
      </div></div>
      <div id="sys-body"><div class="sys-sec">
        <div class="gpu-name">Ryzen 9 7950X<span class="idx">32 threads</span></div>
        <div class="stat"><div class="stat-row"><span>cpu</span><b>68%</b></div><div class="track"><i class="mid" style="width:68%"></i></div></div>
        <div class="stat"><div class="stat-row"><span>memory</span><b>18.4 / 64.0 GB</b></div><div class="track"><i style="width:29%"></i></div></div>
      </div></div>
      <div class="hw-foot">
        <button class="hw-link">
          <span class="hw-link-dot on"></span>
          <span class="hw-link-tx"><span class="hw-link-k">Loaded in vLLM</span><span class="hw-link-v">Qwen2.5-1.5B-Instruct</span></span>
          <span class="hw-link-go">&#8250;</span>
        </button>
        <button class="hw-metrics"><span class="hw-metrics-ic"><i></i><i></i><i></i></span>Metrics</button>
      </div>
    </section>
    <section class="card">
      <div class="card-head"><span class="card-ic">&#128193;</span><h2>Places</h2></div>
      <div id="places-body">
        <div class="place"><span class="pic">&#128230;</span><span class="pl-txt"><span class="pl">Models</span><span class="pl-d">AI models you download are stored here</span></span><span class="pl-go"><span class="pl-go-tx">Open</span><span class="pl-go-ar">&#8599;</span></span></div>
        <div class="place"><span class="pic">&#128444;</span><span class="pl-txt"><span class="pl">Outputs</span><span class="pl-d">Images and files your apps generate</span></span><span class="pl-go"><span class="pl-go-tx">Open</span><span class="pl-go-ar">&#8599;</span></span></div>
        <div class="place"><span class="pic">&#128451;</span><span class="pl-txt"><span class="pl">Workspace</span><span class="pl-d">Your own projects, notebooks and data</span></span><span class="pl-go"><span class="pl-go-tx">Open</span><span class="pl-go-ar">&#8599;</span></span></div>
      </div>
    </section>
  </div>
</main>
<nav class="dock" id="dock">
  <button class="dock-item"><span class="dock-tip">Apps</span><span class="dock-glyph"><svg class="ui-icon"><use href="#ui-apps"/></svg></span></button>
  <button class="dock-item"><span class="dock-tip">Models</span><span class="dock-glyph"><svg class="ui-icon"><use href="#ui-models"/></svg></span></button>
  <button class="dock-item running"><span class="dock-tip">Metrics</span><span class="dock-glyph"><svg class="ui-icon"><use href="#ui-metrics"/></svg></span><span class="run-dot"></span></button>
  <span class="dock-sep"></span>
  <button class="dock-item accent"><span class="dock-tip">Assistant</span><span class="dock-glyph"><svg class="ui-icon"><use href="#ui-assistant"/></svg></span></button>
  <button class="dock-item"><span class="dock-tip">Settings</span><span class="dock-glyph"><svg class="ui-icon"><use href="#ui-settings"/></svg></span></button>
</nav>
"@
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`"><meta name=`"viewport`" content=`"width=device-width, initial-scale=1`">$style$freeze</head><body>$body</body></html>"
$tmp = "$env:TEMP\$Name.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\$Name.png"
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 "--window-size=$Width,$Height" --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 900
if (Test-Path $out) { "OK $out" } else { "NO SHOT" }
