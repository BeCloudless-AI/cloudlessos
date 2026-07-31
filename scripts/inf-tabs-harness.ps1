# Renders the Inference hub with the tab bar (Activity / Engine / API access) and the
# Engine + API panels, from the REAL index.html CSS, to PNG via headless Edge.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value

$engine = @'
<div class="mm" style="height:auto!important;max-height:none!important;margin-bottom:26px">
  <div class="lp-head"><div class="lp-ttl">Inference</div><button class="lp-x">&times;</button></div>
  <div class="mm-body">
    <div class="mm-tabs"><button class="mm-tab">Activity</button><button class="mm-tab on">Engine</button><button class="mm-tab">API access</button></div>
    <div style="max-width:780px;margin:0 auto">
      <div class="set-block"><h3>inference engine</h3>
        <div class="set-row"><div class="er-pills"><button class="er-pill on" disabled>SGLang</button><button class="er-pill">vLLM</button></div><span class="er-state ready">ready</span></div>
        <div class="fld-help">The engine that powers Cloudless AI (vLLM or SGLang). One runs at a time; switching briefly reloads the model. Most people never need to change this &mdash; pick models in the Model Manager.</div>
      </div>
    </div>
  </div>
</div>
'@

$api = @'
<div class="mm" style="height:auto!important;max-height:none!important">
  <div class="lp-head"><div class="lp-ttl">Inference</div><button class="lp-x">&times;</button></div>
  <div class="mm-body">
    <div class="mm-tabs"><button class="mm-tab">Activity</button><button class="mm-tab">Engine</button><button class="mm-tab on">API access</button></div>
    <div style="max-width:780px;margin:0 auto">
      <div class="set-block"><h3>your AI as an API</h3>
        <div class="fld-help" style="margin:0 0 12px">Cloudless serves your local model as a drop-in <b>OpenAI-compatible API</b>. Point any OpenAI client at the base URL below and authenticate with a key you generate here.</div>
        <div class="adm-creds"><div class="adm-row"><span class="adm-k">Base URL</span><code class="adm-v">http://localhost:8766/v1</code></div><div class="adm-row"><span class="adm-k">Model name</span><code class="adm-v">cloudless</code></div></div>
        <div class="code-box"><pre>curl http://localhost:8766/v1/chat/completions \
  -H "Authorization: Bearer YOUR_KEY" -d '{"model":"cloudless","messages":[...]}'</pre></div>
      </div>
      <div class="set-block"><h3>keys</h3>
        <div class="key-row"><div class="key-main"><span class="key-name">My website</span><code class="key-prefix">sk-cloudless-1cd61ec&#8230;</code></div><div class="key-meta">128 requests &middot; last used 27/06/2026</div><button class="btn danger">Revoke</button></div>
        <div class="cfg-actions" style="justify-content:flex-start"><button class="btn primary">Generate new key</button></div>
      </div>
    </div>
  </div>
</div>
'@

$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;flex-direction:column;align-items:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$engine$api</body></html>"
$tmp = "$env:TEMP\inf-tabs-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\inftabs.png"; if (Test-Path $out) { Remove-Item $out }
$edgeProfile = if ($env:CLOUDLESS_VISUAL_EDGE_PROFILE) { $env:CLOUDLESS_VISUAL_EDGE_PROFILE } else { Join-Path $env:TEMP "cloudless-visual-edge-$PID" }
& $edge --headless=new --disable-gpu --hide-scrollbars --no-first-run "--user-data-dir=$edgeProfile" --force-device-scale-factor=1 --window-size=1240,880 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1200
if (Test-Path $out) { "OK $out" } else { "NO SCREENSHOT" }
