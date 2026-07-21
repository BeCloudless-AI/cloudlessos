# Renders the redesigned secondary screens (real CSS) for visual review against the
# brand gradient + UmbrelOS feel: Settings, Model Manager, App Launcher, Onboarding, Assistant.
# Each is drawn over the live wallpaper so the frosted-glass scrims read correctly.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
$sprite = [regex]::Match($h, '(?s)<!-- Cloudless brand mark.*?</svg>').Value
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important;opacity:1!important}</style>"

function Shot($name, $body, $w, $ht) {
  $page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze</head><body><div class=`"wall`"></div>$sprite$body</body></html>"
  $tmp = "$env:TEMP\dh-$name.html"
  [System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
  $out = "$env:TEMP\dh-$name.png"
  & $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=$w,$ht --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null | Out-Null
  if (Test-Path $out) { "OK  $out" } else { "NO SHOT $name" }
}

function ShotFirstLaunchState($name, $w, $ht, $state) {
  $launchSrc = 'D:\Cloudless\orchestrator\internal\api\web\first-launch\index.html'
  $page = Get-Content $launchSrc -Raw -Encoding UTF8
  $page = $page.Replace('url("cloud-opening.png")', 'url("file:///D:/Cloudless/orchestrator/internal/api/web/first-launch/cloud-opening.png")')
  $wallpaper = 'html{background:linear-gradient(135deg,#dce8f8,#eef3fb 48%,#e8def4)!important}'
  if ($state -eq 'word') {
    $override = $wallpaper + 'body{background:transparent!important}#clouds{display:none!important}.logo path{animation:none!important;opacity:1!important;filter:none!important;transform:none!important}.more{display:none!important}'
  } else {
    $override = $wallpaper + 'body{background:rgba(2,5,12,.34)!important}#clouds{opacity:.38!important;transform:scale(1.1)!important}.logo path{animation:none!important}.logo path:first-of-type{opacity:1!important;filter:none!important;transform:translateX(190px)!important}.logo path:not(:first-of-type){opacity:0!important}.more{display:none!important}'
  }
  $page = $page -replace '</style>', ($override + '</style>')
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
  "<button class=`"$cls`"><div class=`"mdl-top`"><span class=`"mdl-icon`"><svg class=`"ui-icon`"><use href=`"#ui-models`"/></svg></span><span class=`"mdl-title`"><span class=`"mdl-name`">$name</span><span class=`"mdl-meta`">$meta</span></span>$status</div>" +
  "<div class=`"mdl-notes`">$notes</div>" +
  "<div class=`"mdl-tags`">$caps$tags</div>" +
  "<div class=`"mdl-foot`"><span class=`"mdl-fit fit-$fit`">$(if($fit -eq 'fits'){'fits your GPU'}elseif($fit -eq 'tight'){'tight fit'}else{'needs more VRAM'})</span><span class=`"mdl-use`"><svg class=`"ui-icon`"><use href=`"#ui-arrow-right`"/></svg></span></div></button>"
}
$rec = '<span class="mdl-cap rec">&#9733; recommended</span>'
$vis = '<span class="mdl-cap vis">vision</span>'
$tool = '<span class="mdl-cap">tools</span>'
$t = '<span class="mdl-t">chat</span><span class="mdl-t">general</span>'
$cards = (Mdl 'Qwen2.5-1.5B-Instruct' '1.5B &middot; Q4 &middot; 32K ctx' 'Fast, capable small chat model. Great default for quick local use.' $rec $t $true $true 'fits' '<span class="mdl-status active"><i></i>Loaded</span>') +
  (Mdl 'Llama-3.1-8B-Instruct' '8B &middot; Q4 &middot; 128K ctx' 'Strong general assistant with long context and tool calling.' "$tool" $t $false $false 'fits' '<span class="mdl-status">Downloaded</span>') +
  (Mdl 'Qwen2.5-VL-7B' '7B &middot; Q4 &middot; 32K ctx &middot; ~7 GB' 'Multimodal model that can read images and screenshots.' "$vis$tool" $t $false $false 'fits' '') +
  (Mdl 'Mixtral-8x7B' '47B MoE &middot; Q4 &middot; ~28 GB' 'Powerful mixture-of-experts; tight on a single 32 GB card.' '' $t $false $false 'tight' '') +
  (Mdl 'Llama-3.1-70B' '70B &middot; Q4 &middot; ~40 GB' 'Frontier-class quality; needs more VRAM than available.' '' $t $false $false 'over' '')
$mm = @"
<div class="overlay" style="position:static">
  <div class="mm">
    <div class="lp-head mm-head"><span class="mm-head-icon"><svg class="ui-icon"><use href="#ui-models"/></svg></span><div class="mm-head-copy"><div class="lp-ttl">Model Manager</div><div class="mm-head-sub">Choose what powers your local AI</div></div><button class="lp-x"><svg class="ui-icon"><use href="#ui-close"/></svg></button></div>
    <div class="mm-body">
      <div class="mm-tabs"><button class="mm-tab on"><svg class="ui-icon"><use href="#ui-models"/></svg>Language models</button><button class="mm-tab"><svg class="ui-icon"><use href="#ui-appearance"/></svg>Image models</button></div>
      <div class="mm-bar"><div class="mm-machine"><svg class="ui-icon"><use href="#ui-machine"/></svg><span><small>Your hardware</small><b>32 GB VRAM</b></span></div><label class="mm-search"><svg class="ui-icon"><use href="#ui-search"/></svg><input placeholder="Search language models&hellip;"></label></div>
      <div class="mm-filter-row"><span class="mm-filter-label">Filter</span><div class="mm-filters"><button class="mm-chip">Fits my GPU</button><button class="mm-chip">Vision</button><button class="mm-chip">Tool calling</button></div><span class="mm-result-count">5 models</span></div>
      <div class="mdl-sec"><span>recommended for your machine</span><span class="mdl-count">5</span></div>
      <div class="mdl-grid">$cards</div>
    </div>
  </div>
</div>
"@

$mmDetail = @"
<div class="overlay" style="position:static">
  <div class="mm">
    <div class="lp-head mm-head"><span class="mm-head-icon"><svg class="ui-icon"><use href="#ui-models"/></svg></span><div class="mm-head-copy"><div class="lp-ttl">Model Manager</div><div class="mm-head-sub">Choose what powers your local AI</div></div><button class="lp-x"><svg class="ui-icon"><use href="#ui-close"/></svg></button></div>
    <div class="mm-body">
      <button class="lp-back"><svg class="ui-icon"><use href="#ui-arrow-right"/></svg><span>All models</span></button>
      <div class="md-detail">
        <div class="md-hero"><div class="md-ic2">&#129504;</div><div class="md-hd"><div class="md-eyebrow">Language model</div><div class="md-nm">Qwen2.5-1.5B-Instruct</div><div class="md-sub">Qwen/Qwen2.5-1.5B-Instruct</div><div class="mdl-tags md-caps"><span class="mdl-cap rec">&#9733; recommended</span><span class="mdl-cap">tools</span></div></div><div class="md-hero-actions"><button class="btn">Re-download weights</button></div></div>
        <div class="md-banner loaded">This model is loaded in vLLM right now.</div>
        <p class="md-desc">A compact general-purpose assistant that runs quickly on local hardware.</p>
        <div class="md-specgrid"><div class="md-spec"><span class="md-k">Parameters</span><span class="md-v">1.5B</span></div><div class="md-spec"><span class="md-k">Context</span><span class="md-v">32K tokens</span></div><div class="md-spec"><span class="md-k">VRAM needed</span><span class="md-v">~2 GB</span></div></div>
      </div>
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
  @('ember','Ember','Warm paper, clay, and soft coral.'),
  @('sunbeam','Sunbeam','Luminous yellow with a warm orange glow.')
) | ForEach-Object {
  $on = if ($_[0] -eq 'cloudless') { ' on' } else { '' }
  "<button class=`"theme-option theme-$($_[0])$on`"><span class=`"theme-preview`"></span><span class=`"theme-copy`"><span><span class=`"theme-name`">$($_[1])</span><span class=`"theme-desc`">$($_[2])</span></span><span class=`"theme-check`">&#10003;</span></span></button>"
}
$appearance = @"
<div class="overlay" style="position:static">
  <div class="set">
    <aside class="set-sidebar"><div class="set-brand">Settings</div><nav class="set-nav">
      <div class="nav-group">you</div><div class="nav-item"><span class="nav-ic"><svg class="ui-icon"><use href="#ui-user"/></svg></span>Profile</div>
      <div class="nav-group">cloudless</div><div class="nav-item"><span class="nav-ic"><svg class="ui-icon"><use href="#ui-machine"/></svg></span>Machine</div>
      <div class="nav-group">system</div><div class="nav-item active"><span class="nav-ic"><svg class="ui-icon"><use href="#ui-appearance"/></svg></span>Appearance</div><div class="nav-item"><span class="nav-ic"><svg class="ui-icon"><use href="#ui-general"/></svg></span>General</div>
    </nav></aside>
    <main class="set-main">
      <header class="set-topbar"><span class="set-page-title">Appearance</span><button class="set-x"><svg class="ui-icon"><use href="#ui-close"/></svg></button></header>
      <div class="set-content"><div class="set-block"><h3>theme</h3><div class="theme-grid">$($themeCards -join '')</div>
        <div class="fld-help appearance-note">This preference is saved only in this browser. Automatic follows dawn, daytime, dusk, and night on the machine's clock.</div>
      </div></div>
    </main>
  </div>
</div>
"@

# ---- Metrics: Engine + API access ----
$infTabsEngine = @"
<div class="mm-tabs inf-tabs"><button class="mm-tab"><svg class="ui-icon"><use href="#ui-metrics"/></svg>Activity</button><button class="mm-tab"><svg class="ui-icon"><use href="#ui-apps"/></svg>Usage</button><button class="mm-tab"><svg class="ui-icon"><use href="#ui-power"/></svg>Power</button><button class="mm-tab on"><svg class="ui-icon"><use href="#ui-machine"/></svg>Engine</button><button class="mm-tab"><svg class="ui-icon"><use href="#ui-key"/></svg>API access</button></div>
"@
$engineCards = @(
  @('rocket','vLLM','GPU','Maximum throughput on a capable GPU',@('Fastest when the model fits in VRAM','Serves Hugging Face model weights','Best for many simultaneous requests'),$true),
  @('network','SGLang','GPU','Responsive serving for agents and tools',@('Low latency with RadixAttention','Strong structured output and tool calling','Efficient prompt and prefix reuse'),$false),
  @('machine','llama.cpp','CPU + GPU','Flexible local inference on almost any machine',@('Offloads models between RAM and VRAM','Can run entirely on the CPU','Uses compact GGUF model files'),$false)
) | ForEach-Object {
  $on = if ($_[5]) { ' on' } else { '' }
  $traits = ($_[4] | ForEach-Object { "<li>$_</li>" }) -join ''
  $foot = if ($_[5]) { '<span class="ec-active">Active engine</span>' } else { '<button class="btn">Use this engine</button>' }
  "<div class=`"ec$on`"><div class=`"ec-head`"><span class=`"ec-ic`"><svg class=`"ui-icon`"><use href=`"#ui-$($_[0])`"/></svg></span><span class=`"ec-title`"><span class=`"ec-name`">$($_[1])</span><span class=`"ec-runs`">$($_[2])</span></span></div><div class=`"ec-best`">$($_[3])</div><ul class=`"ec-traits`">$traits</ul><div class=`"ec-foot`">$foot</div></div>"
}
$infEngine = @"
<div class="overlay" style="position:static"><div class="mm" style="height:760px">
  <div class="lp-head"><div class="lp-ttl">Metrics</div><div class="inf-head on"><span class="inf-live"></span><b>vLLM</b><span class="ih-state">live</span></div><button class="lp-x"><svg class="ui-icon"><use href="#ui-close"/></svg></button></div>
  <div class="mm-body">$infTabsEngine<div id="inf-panel"><div class="inf-page">
    <div class="inf-page-hero"><span class="inf-page-icon"><svg class="ui-icon"><use href="#ui-machine"/></svg></span><div class="inf-page-copy"><div class="inf-page-eyebrow">Runtime</div><div class="inf-page-title">Inference engine</div><div class="inf-page-desc">Choose the runtime that powers chat, apps, and the API. Switching reloads the current model and usually takes under a minute.</div></div><span class="inf-state-pill ready">Ready</span></div>
    <div class="ec-grid">$($engineCards -join '')</div><div class="fld-help">The model itself is selected in Model Manager. Engine availability depends on your hardware and installation.</div>
  </div></div></div>
</div></div>
"@

$infTabsApi = $infTabsEngine -replace 'mm-tab on"><svg class="ui-icon"><use href="#ui-machine"/></svg>Engine','mm-tab"><svg class="ui-icon"><use href="#ui-machine"/></svg>Engine' -replace 'mm-tab"><svg class="ui-icon"><use href="#ui-key"/></svg>API access','mm-tab on"><svg class="ui-icon"><use href="#ui-key"/></svg>API access'
$infApi = @"
<div class="overlay" style="position:static"><div class="mm" style="height:820px">
  <div class="lp-head"><div class="lp-ttl">Metrics</div><div class="inf-head on"><span class="inf-live"></span><b>vLLM</b><span class="ih-state">live</span></div><button class="lp-x"><svg class="ui-icon"><use href="#ui-close"/></svg></button></div>
  <div class="mm-body">$infTabsApi<div id="inf-panel"><div class="inf-page">
    <div class="inf-page-hero"><span class="inf-page-icon"><svg class="ui-icon"><use href="#ui-key"/></svg></span><div class="inf-page-copy"><div class="inf-page-eyebrow">OpenAI compatible</div><div class="inf-page-title">API access</div><div class="inf-page-desc">Connect local tools and apps to the model running on this machine. Requests stay on your hardware unless you enable network access.</div></div><span class="inf-state-pill ready">Local</span></div>
    <section class="api-section"><div class="api-section-head"><div><div class="api-section-title">Connect a client</div><div class="api-section-desc">Use these values with any OpenAI-compatible SDK.</div></div></div>
      <div class="api-endpoints"><div class="api-endpoint"><span class="api-endpoint-label">Base URL</span><code>http://cloudless.local:8765/v1</code><button class="api-copy"><svg class="ui-icon"><use href="#ui-copy"/></svg></button></div><div class="api-endpoint"><span class="api-endpoint-label">Model</span><code>cloudless</code><button class="api-copy"><svg class="ui-icon"><use href="#ui-copy"/></svg></button></div><div class="api-endpoint"><span class="api-endpoint-label">Backed by</span><code>Qwen2.5-1.5B-Instruct</code><span></span></div></div>
      <div class="api-code"><div class="code-box"><pre>curl http://cloudless.local:8765/v1/chat/completions \
  -H "Authorization: Bearer YOUR_KEY" \
  -d '{"model":"cloudless","messages":[...]}'</pre></div><button class="api-copy"><svg class="ui-icon"><use href="#ui-copy"/></svg></button></div>
    </section>
    <section class="api-section"><div class="api-section-head"><div><div class="api-section-title">API keys</div><div class="api-section-desc">Keys control who can send requests to your model.</div></div><button class="btn primary">Generate key</button></div><div class="api-keys-empty">No keys yet. Generate one when you are ready to connect a client.</div></section>
    <section class="api-section"><div class="api-section-head"><div><div class="api-section-title">Network access</div><div class="api-section-desc">Choose where the API can be reached. Authentication is always required.</div></div></div><div class="api-access-grid"><div class="api-access-card"><div class="api-access-top"><span class="api-access-icon"><svg class="ui-icon"><use href="#ui-network"/></svg></span><span class="api-access-name">Local network</span><label class="switch"><input type="checkbox" checked><span class="track2"></span></label></div><div class="fld-help">Reach Cloudless from devices on your Wi-Fi.</div></div><div class="api-access-card"><div class="api-access-top"><span class="api-access-icon"><svg class="ui-icon"><use href="#ui-expand"/></svg></span><span class="api-access-name">Public link</span><label class="switch"><input type="checkbox"><span class="track2"></span></label></div><div class="fld-help">Create a secure internet address for approved remote clients.</div></div></div></section>
  </div></div></div>
</div></div>
"@

ShotFirstLaunchState 'first-launch-seat' 1180 760 'seat'
ShotFirstLaunchState 'first-launch-word' 1180 760 'word'
ShotFirstLaunchState 'first-launch-mobile' 600 900 'word'
Shot 'settings'   $set  900 720
Shot 'appearance' $appearance 900 720
Shot 'metrics-engine' $infEngine 1180 840
Shot 'metrics-engine-compact' $infEngine 700 900
Shot 'metrics-api' $infApi 1180 900
Shot 'metrics-api-compact' $infApi 700 940
Shot 'models'     $mm   1320 940
Shot 'models-compact' $mm 760 940
Shot 'models-mobile' $mm 600 940
Shot 'model-detail' $mmDetail 1040 760
Shot 'model-detail-compact' $mmDetail 760 900
Shot 'model-detail-mobile' $mmDetail 600 900
Shot 'launcher'   $lp   1180 820
Shot 'onboarding' $ob   720 640
Shot 'assistant'  $asst 520 720
