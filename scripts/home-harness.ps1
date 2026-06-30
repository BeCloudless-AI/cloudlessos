# Renders the redesigned home (real CSS) populated with representative content:
# frosted menubar, hero + ask bar, app grid, glass Hardware + Places cards.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value

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
  "<div class=`"tile$running`"><div class=`"ic`">$($_[1])</div><div class=`"nm`">$($_[0])</div><div class=`"meta`">$($_[2])</div></div>"
}) -join ''

$body = @"
<div class="wall"></div>
<div class="menubar">
  <div class="brand"><span class="mark"></span> Cloudless</div>
  <div class="spacer"></div>
  <div class="mi"><span class="led ok"></span> RTX 5090</div>
  <div class="mi"><span class="led ok"></span> Ready</div>
  <div class="mi mi-net"><span class="led net-online"></span> Network</div>
  <div id="clock">19:53</div>
  <button class="gear">&#9645;</button><button class="gear">&#9672;</button><button class="gear">&#9881;</button>
</div>
<main>
  <div class="hero">
    <div class="greet-row">
      <div class="greet-txt"><h1>Good evening, Samuel</h1><p>Your private, local-AI workstation.</p></div>
      <div class="hero-time"><div class="ht-clock"><span id="big-clock">19:53</span><span class="accent-dot"></span></div><div class="hero-date">Tuesday, 30 June</div><div class="hero-zone">Europe/Paris</div></div>
    </div>
    <form class="ask"><span class="ask-spark"></span><input placeholder="What do you want to do with CloudlessOS?"><button class="ask-go" type="button">&#8594;</button></form>
    <button class="ghost-chat">Open the full chat &#8599;</button>
  </div>
  <section class="apps-sec">
    <div class="sec-row"><h3 class="sec">your apps</h3><button class="sec-act">&#9638; All apps</button></div>
    <div class="pad">$tiles</div>
  </section>
  <div class="cards">
    <section class="card">
      <div class="card-head"><span class="card-ic">&#128421;</span><h2>Hardware</h2><span class="card-live"><i></i>live</span></div>
      <div id="gpu-body"><div class="gpu">
        <div class="gpu-name">RTX 5090<span class="idx">#0</span></div>
        <div class="stat"><div class="stat-row"><span>Memory</span><b>14.2 / 32.0 GB</b></div><div class="track"><i style="width:44%"></i></div></div>
        <div class="stat"><div class="stat-row"><span>Utilization</span><b>61%</b></div><div class="track"><i style="width:61%"></i></div></div>
        <div class="chips"><span>58&deg;C</span><span><b>412 / 600 W</b></span><span>Driver 595.79</span></div>
      </div></div>
      <div id="sys-body"><div class="sys-sec">
        <div class="gpu-name">Ryzen 9 7950X<span class="idx">32 threads</span></div>
        <div class="stat"><div class="stat-row"><span>cpu</span><b>23%</b></div><div class="track"><i style="width:23%"></i></div></div>
        <div class="stat"><div class="stat-row"><span>memory</span><b>18.4 / 64.0 GB</b></div><div class="track"><i style="width:29%"></i></div></div>
      </div></div>
      <button class="card-link">&#9672; Qwen2.5-1.5B-Instruct &#8594;</button>
      <button class="card-link">&#9645; Inference &#8594;</button>
    </section>
    <section class="card">
      <div class="card-head"><span class="card-ic">&#128193;</span><h2>Places</h2></div>
      <div id="places-body">
        <div class="place"><span class="pic">&#128193;</span><span class="pl-txt"><span class="pl">Models</span><span class="pp">&hellip;/cloudless/models</span></span><span class="pl-go">&#8250;</span></div>
        <div class="place"><span class="pic">&#127912;</span><span class="pl-txt"><span class="pl">Outputs</span><span class="pp">&hellip;/cloudless/outputs</span></span><span class="pl-go">&#8250;</span></div>
        <div class="place"><span class="pic">&#128196;</span><span class="pl-txt"><span class="pl">Workspace</span><span class="pp">&hellip;/cloudless/work</span></span><span class="pl-go">&#8250;</span></div>
      </div>
    </section>
  </div>
</main>
<nav class="dock" id="dock">
  <button class="dock-item"><span class="dock-tip">Apps</span>&#9638;</button>
  <button class="dock-item"><span class="dock-tip">Models</span>&#9672;</button>
  <button class="dock-item running"><span class="dock-tip">Inference</span>&#9637;<span class="run-dot"></span></button>
  <span class="dock-sep"></span>
  <button class="dock-item accent"><span class="dock-tip">Assistant</span>&#10022;</button>
  <button class="dock-item"><span class="dock-tip">Settings</span>&#9881;</button>
</nav>
"@
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important;opacity:1!important}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze</head><body>$body</body></html>"
$tmp = "$env:TEMP\home-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\home-render.png"
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1320,940 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 900
if (Test-Path $out) { "OK $out" } else { "NO SHOT" }
