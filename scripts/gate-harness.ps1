# Renders the dashboard "engine starting" state (banner + gated app tiles + disabled
# ask) from the REAL index.html CSS, to PNG via headless Edge.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
function Tile($cls, $g, $nm, $meta) { "<div class=`"$cls`"><div class=`"ic`">$g<button class=`"stopx`">&times;</button></div><div class=`"nm`">$nm</div><div class=`"meta`">$meta</div><div class=`"tbar indet`"><i></i></div></div>" }
$wait = '<span class="dot wait"></span>Waiting for AI'
$tiles = (Tile 'tile gated' '&#128172;' 'Open WebUI' $wait) +
         (Tile 'tile' '&#127912;' 'ComfyUI' '') +
         (Tile 'tile gated' '&#129302;' 'OpenClaw' $wait) +
         (Tile 'tile gated' '&#129413;' 'Hermes' $wait)
$body = @"
<main>
  <div class="hero">
    <div class="greet-row"><div class="greet-txt"><h1>Welcome to Cloudless</h1><p>Good afternoon, Samuel. Your private, local-AI workstation.</p></div>
      <div class="hero-time"><div class="ht-clock"><span id="big-clock">14:32</span><span class="accent-dot"></span></div><div class="hero-date">Friday, 19 June</div><div class="hero-zone">Europe/Paris &middot; France</div></div></div>
    <form class="ask starting"><span class="ask-spark"></span><input placeholder="Cloudless AI is starting&#8230;" disabled><button class="ask-go" type="button" disabled>&rarr;</button></form>
    <button class="ghost-chat off">Open the full chat &#8599;</button>
  </div>
  <div class="eng-banner"><span class="eb-spin"></span><div class="eb-txt"><b>Cloudless AI is starting up&#8230;</b><span>SGLang is loading your model &mdash; chat and AI apps will be ready in a moment.</span></div></div>
  <section class="apps-sec"><div class="sec-row"><h3 class="sec">Your apps</h3><button class="sec-act">&#9638; All apps</button></div>
    <div class="pad">$tiles</div></section>
</main>
"@
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:var(--bg);margin:0}main{padding-top:34px}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$body</body></html>"
$tmp = "$env:TEMP\gate-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\gate.png"; if (Test-Path $out) { Remove-Item $out }
$edgeProfile = if ($env:CLOUDLESS_VISUAL_EDGE_PROFILE) { $env:CLOUDLESS_VISUAL_EDGE_PROFILE } else { Join-Path $env:TEMP "cloudless-visual-edge-$PID" }
& $edge --headless=new --disable-gpu --hide-scrollbars --no-first-run "--user-data-dir=$edgeProfile" --force-device-scale-factor=1 --window-size=1240,620 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1100
if (Test-Path $out) { "OK $out" } else { "NO SCREENSHOT" }
