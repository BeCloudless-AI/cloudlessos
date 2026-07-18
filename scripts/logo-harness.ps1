# Renders the new Cloudless sparkle mark in its three brand lockups
# (menubar, onboarding welcome, assistant header) over the real CSS.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
# pull the real symbol/gradient defs straight out of index.html so the render is faithful
$defs = [regex]::Match($h, '(?s)<svg width="0" height="0".*?</svg>').Value
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important;opacity:1!important}body{padding:0;background:#0e0f14}</style>"
$body = @"
<div class="wall"></div>
$defs
<div class="menubar">
  <div class="brand"><svg class="mark cl-mark" aria-hidden="true"><use href="#cl-star"/></svg> cloudless</div>
  <div class="spacer"></div>
  <div id="clock">19:53</div>
</div>
<div style="display:flex;gap:26px;padding:40px 34px;align-items:flex-start;flex-wrap:wrap">
  <section class="card" style="width:340px">
    <div class="ob-body" style="text-align:center">
      <div class="ob-logo"><svg class="cl-mark" aria-hidden="true"><use href="#cl-star"/></svg></div>
      <h2>Welcome to Cloudless</h2>
      <p class="lead">A private AI workstation that runs on your own machine — no cloud, no accounts.</p>
    </div>
  </section>
  <section class="card" style="width:340px;padding:0;overflow:hidden">
    <div class="asst-head">
      <svg class="cl-mark asst-mark" aria-hidden="true"><use href="#cl-star"/></svg>
      <div class="asst-ttl">Cloudless Assistant<span class="asst-sub">here to help you decide</span></div>
    </div>
    <div style="padding:18px 16px;color:var(--muted);font-size:13px">Hi — I'm your Cloudless assistant.</div>
  </section>
</div>
"@
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze</head><body>$body</body></html>"
$tmp = "$env:TEMP\logo-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\logo-render.png"
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=2 --window-size=780,520 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null | Out-Null
if (Test-Path $out) { "OK $out" } else { "NO SHOT" }
