# Renders the Open WebUI app settings page (auth toggles + administration block)
# from the REAL index.html CSS, to PNG via headless Edge.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
$body = @'
<div class="set" style="height:auto!important;width:min(760px,96vw)">
  <div class="set-main">
    <div class="set-topbar"><div class="set-page-title">Open WebUI</div></div>
    <div class="set-content">
      <div class="app-head"><div class="app-status"><span class="led ok"></span>running</div><a><button class="btn primary">Open</button></a></div>
      <div class="set-block"><h3>configuration</h3>
        <div class="fld-form">
          <div class="fld"><span class="fld-label">Require an account to use it</span>
            <label class="switch"><input type="checkbox"><span class="track2"></span></label></div>
          <div class="fld-help">Off: anyone who opens Open WebUI can use it, no login. On: people must sign in with an account — the first account created becomes the administrator.</div>
          <div class="fld"><span class="fld-label">Allow new sign-ups</span>
            <label class="switch"><input type="checkbox" checked><span class="track2"></span></label></div>
          <div class="fld-help">Let people create their own accounts. Turn off once your accounts exist to lock new sign-ups.</div>
        </div>
        <div class="cfg-actions"><button class="btn primary">Save &amp; apply</button></div>
      </div>
      <div class="set-block"><h3>administration</h3>
        <div class="fld-help" style="margin:0 0 12px">Everything else about Open WebUI — users, model access, permissions, document/RAG settings and more — is managed inside Open WebUI's own Admin Panel (open the app, then top-right menu &rarr; Admin Panel). When account login is on, the first account you create becomes the administrator.</div>
        <div class="adm-creds">
          <div class="adm-row"><span class="adm-k">Administrator</span><code class="adm-v">No default account</code></div>
          <div class="fld-help" style="margin:8px 0 0">The first account you create becomes the administrator. CloudlessOS does not ship a shared password.</div>
        </div>
      </div>
      <div class="set-block"><h3>share online</h3>
        <div class="set-row"><span>Make Open WebUI reachable from the internet</span>
          <span class="row-actions"><label class="switch"><input type="checkbox"><span class="track2"></span></label></span></div>
        <div class="fld-help">Creates a public Cloudflare link to this app. <b>Anyone with the link can use it</b> — only share it with people you trust.</div>
        <div class="sec-warn">&#x26A0; Open WebUI has no login right now — anyone with the public link can use your AI and your GPU, with no account. Turn on &ldquo;Require an account to use it&rdquo; above before sharing, or only share with people you fully trust.</div>
      </div>
      <div class="set-block"><h3>manage</h3>
        <div class="set-row"><span>Reset to defaults</span><span class="row-actions"><button class="btn">Reset</button></span></div>
        <div class="set-row"><span>Uninstall</span><span class="row-actions"><button class="btn danger">Uninstall</button></span></div>
      </div>
    </div>
  </div>
</div>
'@
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;justify-content:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$body</body></html>"
$tmp = "$env:TEMP\owui-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\owui.png"; if (Test-Path $out) { Remove-Item $out }
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=900,1000 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1100
if (Test-Path $out) { "OK $out" } else { "NO SCREENSHOT" }
