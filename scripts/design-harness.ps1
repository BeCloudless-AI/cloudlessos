# Renders the redesigned secondary screens (real CSS) for visual review against the
# brand gradient + UmbrelOS feel: Settings, Model Manager, App Launcher, Onboarding, Assistant.
# Each is drawn over the live wallpaper so the frosted-glass scrims read correctly.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important;opacity:1!important}</style>"

function Shot($name, $body, $w, $ht) {
  $page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze</head><body><div class=`"wall`"></div>$body</body></html>"
  $tmp = "$env:TEMP\dh-$name.html"
  [System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
  $out = "$env:TEMP\dh-$name.png"
  & $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=$w,$ht --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null | Out-Null
  if (Test-Path $out) { "OK  $out" } else { "NO SHOT $name" }
}

# ---- Settings ----
$navGroups = @(
  @('general', @(@('&#9881;','Profile',$true), @('&#127760;','Location & time',$false), @('&#128268;','Network',$false))),
  @('ai', @(@('&#9672;','Default model',$false), @('&#128279;','API access',$false), @('&#9889;','Inference engine',$false))),
  @('apps', @(@('&#128172;','Open WebUI',$false), @('&#127912;','ComfyUI',$false)))
)
$nav = ($navGroups | ForEach-Object {
  $g = "<div class=`"nav-group`">$($_[0])</div>"
  $g + (($_[1] | ForEach-Object {
    $a = if ($_[2]) { ' active' } else { '' }
    "<div class=`"nav-item$a`"><span class=`"nav-ic`">$($_[0])</span>$($_[1])</div>"
  }) -join '')
}) -join ''
$setContent = @"
<div class="set-block"><h3>profile</h3>
  <div class="set-row"><span class="srow-label">name</span><span><b>Samuel</b></span></div>
  <div class="set-row"><span class="srow-label">machine</span><span>cloudless-5090</span></div>
  <div class="set-row" style="margin-top:6px"><span class="row-actions"><button class="btn primary">Save</button></span></div>
</div>
<div class="set-block"><h3>location &amp; time</h3>
  <div class="set-row"><span class="srow-label">region</span><span><b>France</b> <span class="set-hint" style="margin:0">&middot; detected</span></span></div>
  <div class="set-row"><span class="srow-label">timezone</span><span>Europe/Paris <span class="set-hint" style="margin:0">(UTC +02:00)</span></span></div>
  <div class="set-row"><span class="srow-label">language</span><span>en-US</span></div>
</div>
<div class="set-block"><h3>your AI as an API</h3>
  <div class="code-box"><pre>curl http://localhost:8765/v1/chat/completions \
  -H "Authorization: Bearer sk-cl-..." \
  -d '{"model":"cloudless","messages":[...]}'</pre></div>
</div>
"@
$set = @"
<div class="overlay" style="position:static">
  <div class="set">
    <aside class="set-sidebar"><div class="set-brand">Settings</div><nav class="set-nav">$nav</nav></aside>
    <main class="set-main">
      <header class="set-topbar"><span class="win-traffic"><i class="t-c"></i><i class="t-m"></i><i class="t-f"></i></span><span class="set-page-title">Profile</span><button class="set-x">&#10005;</button></header>
      <div class="set-content">$setContent</div>
    </main>
  </div>
</div>
"@

# ---- Model Manager ----
function Mdl($name,$meta,$notes,$caps,$tags,$on,$rec,$fit,$status) {
  $cls = "mdl"; if ($on) { $cls += " on" }; if ($rec) { $cls += " rec" }; $cls += " fit-$fit"
  "<button class=`"$cls`"><div class=`"mdl-top`"><span class=`"mdl-name`">$name</span>$status</div>" +
  "<div class=`"mdl-meta`">$meta</div><div class=`"mdl-notes`">$notes</div>" +
  "<div class=`"mdl-tags`">$caps$tags</div>" +
  "<div class=`"mdl-foot`"><span class=`"mdl-fit fit-$fit`">$(if($fit -eq 'fits'){'fits comfortably'}elseif($fit -eq 'tight'){'tight fit'}else{'too large'})</span><span class=`"mdl-use`">details &#8250;</span></div></button>"
}
$rec = '<span class="mdl-cap rec">&#9733; recommended</span>'
$vis = '<span class="mdl-cap vis">vision</span>'
$tool = '<span class="mdl-cap">tools</span>'
$t = '<span class="mdl-t">chat</span><span class="mdl-t">general</span>'
$cards = (Mdl 'Qwen2.5-1.5B-Instruct' '1.5B &middot; Q4 &middot; 32K ctx &middot; ~2 GB' 'Fast, capable small chat model. Great default for quick local use.' $rec $t $true $true 'fits' '<span class="mdl-active">&#9679; loaded</span>') +
  (Mdl 'Llama-3.1-8B-Instruct' '8B &middot; Q4 &middot; 128K ctx &middot; ~6 GB' 'Strong general assistant with long context and tool calling.' "$tool" $t $false $false 'fits' '<span class="mdl-disk">on disk</span>') +
  (Mdl 'Qwen2.5-VL-7B' '7B &middot; Q4 &middot; 32K ctx &middot; ~7 GB' 'Multimodal model that can read images and screenshots.' "$vis$tool" $t $false $false 'fits' '') +
  (Mdl 'Mixtral-8x7B' '47B MoE &middot; Q4 &middot; ~28 GB' 'Powerful mixture-of-experts; tight on a single 32 GB card.' '' $t $false $false 'tight' '') +
  (Mdl 'Llama-3.1-70B' '70B &middot; Q4 &middot; ~40 GB' 'Frontier-class quality; needs more VRAM than available.' '' $t $false $false 'over' '')
$mm = @"
<div class="overlay" style="position:static">
  <div class="mm">
    <div class="lp-head"><span class="win-traffic"><i class="t-c"></i><i class="t-m"></i><i class="t-f"></i></span><div class="lp-ttl">Model Manager</div><button class="lp-x">&#215;</button></div>
    <div class="mm-body">
      <div class="mm-tabs"><button class="mm-tab on">Language models</button><button class="mm-tab">Diffusion</button><button class="mm-tab">Downloaded</button></div>
      <div class="mm-bar"><span class="mm-sum">Showing <b>5</b> models that fit your <b>RTX 5090 (32 GB)</b></span></div>
      <div class="mdl-sec">recommended for your machine</div>
      <div class="mdl-grid">$cards</div>
    </div>
  </div>
</div>
"@

# ---- App Launcher ----
$liApps = @(
  @('&#128172;','Cloudless AI',$true,$true), @('&#127912;','ComfyUI',$false,$true),
  @('&#128172;','Open WebUI',$false,$true), @('&#129302;','Ollama',$false,$false),
  @('&#128200;','Jupyter',$false,$false), @('&#10024;','Stable Diffusion',$false,$false)
)
$li = ($liApps | ForEach-Object {
  $a = if ($_[2]) { ' active' } else { '' }
  $dot = if ($_[3]) { '<span class="li-dot"></span>' } else { '' }
  "<button class=`"li$a`"><span class=`"li-ic`">$($_[0])</span><span class=`"li-nm`">$($_[1])</span>$dot</button>"
}) -join ''
$lp = @"
<div class="overlay" style="position:static">
  <div class="lp">
    <div class="lp-head"><span class="win-traffic"><i class="t-c"></i><i class="t-m"></i><i class="t-f"></i></span><div class="lp-ttl">App Launcher</div><button class="lp-x">&#215;</button></div>
    <div class="lp-cols">
      <div class="lp-side">
        <input class="lp-search" placeholder="Search apps&hellip;">
        <div class="lp-list"><div class="lp-cat">installed</div>$li</div>
      </div>
      <div class="lp-detail">
        <div class="lp-intro">
          <div class="ii"></div>
          <h2>One place for every local AI app</h2>
          <p>Browse vetted apps, install them in one click, and run them on your own GPU. No setup, no cloud.</p>
          <div class="lp-cats">
            <div class="lp-cc"><span class="cc-ic">&#128172;</span><div class="cc-d"><div class="cc-t">Chat &amp; assistants</div><span>Talk to local models with a friendly UI.</span></div></div>
            <div class="lp-cc"><span class="cc-ic">&#127912;</span><div class="cc-d"><div class="cc-t">Image generation</div><span>ComfyUI, Stable Diffusion and more.</span></div></div>
            <div class="lp-cc"><span class="cc-ic">&#128295;</span><div class="cc-d"><div class="cc-t">Developer tools</div><span>Notebooks, APIs and model serving.</span></div></div>
          </div>
        </div>
      </div>
    </div>
  </div>
</div>
"@

# ---- Onboarding ----
$ob = @"
<div class="overlay" style="position:static">
  <div class="ob">
    <div class="ob-body">
      <h2>Your machine</h2>
      <p class="lead">Checking what your hardware can run&hellip;</p>
      <div class="hw"><div class="hi">&#9889;</div><div><div class="hn">RTX 5090</div><div class="hs">32 GB VRAM &middot; driver 595.79</div></div><div class="verdict ok">&#10003; Ready</div></div>
    </div>
    <div class="ob-foot">
      <div class="dots"><span></span><span class="on"></span><span></span><span></span></div>
      <div class="ob-act"><button class="btn link">Back</button><button class="btn primary">Continue</button></div>
    </div>
  </div>
</div>
"@

# ---- Assistant (docked + chips) ----
$asst = @"
<div class="asst" style="position:static;margin:40px auto">
  <div class="asst-head"><span class="asst-mark"></span><div class="asst-ttl">Cloudless Assistant<span class="asst-sub">here to help you decide</span></div><button class="asst-exp">&#10530;</button><button class="asst-x">&#215;</button></div>
  <div class="asst-body">
    <div class="msg bot">Hi Samuel &#128075; I can help you pick a model, install an app, or explain what your machine can run. What would you like to do?</div>
    <div class="msg user">Which model is best for coding?</div>
    <div class="msg bot">For coding on your RTX 5090, I'd suggest <b>Qwen2.5-Coder-7B</b> &mdash; it fits comfortably and is strong at code. Want me to load it?</div>
  </div>
  <div class="asst-chips"><button class="asst-chip">Recommend a model</button><button class="asst-chip">Install ComfyUI</button><button class="asst-chip">What can my GPU run?</button></div>
  <form class="asst-in"><input placeholder="Ask anything&hellip;"><button class="asst-send">&#8593;</button></form>
</div>
"@

# ---- Appearance ----
$themeCards = @(
  @('auto','Automatic','Changes with the time of day.'),
  @('cloudless','Cloudless','Bright, cool, and quietly atmospheric.'),
  @('midnight','Midnight','Deep navy with crisp blue light.'),
  @('aurora','Aurora','Dark teal with a cool luminous accent.'),
  @('ember','Ember','Warm paper, clay, and soft coral.')
) | ForEach-Object {
  $on = if ($_[0] -eq 'cloudless') { ' on' } else { '' }
  "<button class=`"theme-option theme-$($_[0])$on`"><span class=`"theme-preview`"></span><span class=`"theme-copy`"><span><span class=`"theme-name`">$($_[1])</span><span class=`"theme-desc`">$($_[2])</span></span><span class=`"theme-check`">&#10003;</span></span></button>"
}
$appearance = @"
<div class="overlay" style="position:static">
  <div class="set">
    <aside class="set-sidebar"><div class="set-brand">Settings</div><nav class="set-nav">
      <div class="nav-group">you</div><div class="nav-item"><span class="nav-ic">&#9680;</span>Profile</div>
      <div class="nav-group">cloudless</div><div class="nav-item"><span class="nav-ic">&#9636;</span>Machine</div>
      <div class="nav-group">system</div><div class="nav-item active"><span class="nav-ic">&#9682;</span>Appearance</div><div class="nav-item"><span class="nav-ic">&#10687;</span>General</div>
    </nav></aside>
    <main class="set-main">
      <header class="set-topbar"><span class="set-page-title">Appearance</span><button class="set-x">&#10005;</button></header>
      <div class="set-content"><div class="set-block"><h3>theme</h3><div class="theme-grid">$($themeCards -join '')</div>
        <div class="fld-help appearance-note">This preference is saved only in this browser. Automatic follows dawn, daytime, dusk, and night on the machine's clock.</div>
      </div></div>
    </main>
  </div>
</div>
"@

Shot 'settings'   $set  900 720
Shot 'appearance' $appearance 900 720
Shot 'models'     $mm   1320 940
Shot 'launcher'   $lp   1180 820
Shot 'onboarding' $ob   720 640
Shot 'assistant'  $asst 520 720
