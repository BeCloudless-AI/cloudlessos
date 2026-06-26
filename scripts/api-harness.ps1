# Renders the Settings → API access page (Cloudless Proxy) from the REAL index.html
# CSS, to PNG via headless Edge.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
$body = @'
<div class="set" style="height:auto!important;width:min(820px,96vw)">
  <div class="set-main">
    <div class="set-topbar"><div class="set-page-title">API access</div></div>
    <div class="set-content">
      <div class="set-block"><h3>your AI as an API</h3>
        <div class="fld-help" style="margin:0 0 12px">Cloudless serves your local model as a drop-in <b>OpenAI-compatible API</b>. Point any OpenAI client at the base URL below and authenticate with a key you generate here — it all still runs on your GPU.</div>
        <div class="adm-creds">
          <div class="adm-row"><span class="adm-k">Base URL</span><code class="adm-v">http://localhost:8766/v1</code></div>
          <div class="adm-row"><span class="adm-k">Model name</span><code class="adm-v">cloudless</code></div>
          <div class="adm-row"><span class="adm-k">Backed by</span><span>Qwen/Qwen2.5-1.5B-Instruct</span></div>
        </div>
        <div class="code-box"><pre>curl http://localhost:8766/v1/chat/completions \
  -H "Authorization: Bearer YOUR_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"cloudless","messages":[{"role":"user","content":"Hello!"}]}'</pre></div>
      </div>
      <div class="set-block"><h3>keys</h3>
        <div id="key-list">
          <div class="key-row"><div class="key-main"><span class="key-name">My website</span><code class="key-prefix">sk-cloudless-1cd61ec&#8230;</code></div><div class="key-meta">128 requests &middot; last used 26/06/2026</div><button class="btn danger">Revoke</button></div>
          <div class="key-row"><div class="key-main"><span class="key-name">Phone app</span><code class="key-prefix">sk-cloudless-9af02bb&#8230;</code></div><div class="key-meta">0 requests &middot; never used</div><button class="btn danger">Revoke</button></div>
        </div>
        <div id="key-new" class="tun-out">
          <div class="key-reveal"><div class="kr-top">New key &middot; Phone app — copy it now, you won't be able to see it again:</div>
            <div class="kr-key"><code>sk-cloudless-9af02bb7c4e1d8a6f035b219ce7740aa12d9</code><button class="btn">Copy</button></div></div>
        </div>
        <div class="cfg-actions" style="justify-content:flex-start"><button class="btn primary">Generate new key</button></div>
      </div>
      <div class="set-block"><h3>serve it to others</h3>
        <div class="set-row"><span>On your local network</span>
          <span class="row-actions"><label class="switch"><input type="checkbox" checked><span class="track2"></span></label></span></div>
        <div class="fld-help">Lets other devices on your Wi-Fi reach the API at <b>http://192.168.1.50:8766/v1</b>. A key is still required.</div>
        <div class="set-row"><span>On the internet (public link)</span>
          <span class="row-actions"><label class="switch"><input type="checkbox" checked><span class="track2"></span></label></span></div>
        <div class="tun-out"><div class="tun-row"><span class="tun-dot"></span><a class="tun-link">https://calm-river-1234.trycloudflare.com/v1</a></div>
          <div class="fld-help" style="margin-top:6px">Give this base URL + a key to whoever you want to serve.</div></div>
        <div class="sec-warn">&#x26A0; Anyone you give a key to can use your AI and your GPU from anywhere this is reachable. Share keys only with people you trust, and revoke them if needed.</div>
      </div>
    </div>
  </div>
</div>
'@
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;justify-content:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$body</body></html>"
$tmp = "$env:TEMP\api-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\apiaccess.png"; if (Test-Path $out) { Remove-Item $out }
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=980,1180 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1100
if (Test-Path $out) { "OK $out" } else { "NO SCREENSHOT" }
