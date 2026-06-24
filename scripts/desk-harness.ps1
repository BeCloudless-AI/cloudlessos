# Builds a static harness of the desktop from the REAL CSS + faithful sample markup,
# renders it to PNG via headless Edge. Param -Body supplies the <main> inner HTML and
# -Out the png name; -Theme sets html data-theme. Usage:
#   powershell -File desk-harness.ps1 -HtmlFile current -Out desk-current.png
param([string]$Variant = 'current', [string]$Out = 'desk.png', [string]$Theme = '')

$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
# pull the <body>..menubar+main markup straight from the file so the harness matches reality
$bodyStart = $h.IndexOf('<div class="menubar">')
$mainEnd = $h.IndexOf('</main>') + '</main>'.Length
$shell = $h.Substring($bodyStart, $mainEnd - $bodyStart)

# inject sample content into the live placeholders
$gpu = @'
<div class="gpu"><div class="gpu-name">NVIDIA GeForce RTX 5090<span class="idx">#0</span></div>
  <div class="stat"><div class="stat-row"><span>Memory</span><b>8.1 / 32.0 GB</b></div><div class="track"><i style="width:25%"></i></div></div>
  <div class="stat"><div class="stat-row"><span>Utilization</span><b>12%</b></div><div class="track"><i style="width:12%"></i></div></div>
  <div class="chips"><span>45&deg;C</span><span><b>230 / 600 W</b></span><span>Driver 595.79</span></div></div>
'@
$places = @'
<div class="place"><span class="pic">&#128229;</span><span class="pl">Downloads</span><span class="pp">…/cloudless/downloads</span></div>
<div class="place"><span class="pic">&#129504;</span><span class="pl">Models</span><span class="pp">…/cloudless/models</span></div>
<div class="place"><span class="pic">&#127912;</span><span class="pl">Outputs</span><span class="pp">…/cloudless/outputs</span></div>
'@
function Tile($cls,$g,$nm,$meta){ "<div class=`"$cls`"><div class=`"ic`">$g<button class=`"stopx`">&times;</button></div><div class=`"nm`">$nm</div><div class=`"meta`">$meta</div><div class=`"tbar indet`"><i></i></div></div>" }
$tiles = (Tile 'tile running' '&#128172;' 'Open WebUI' '<span class="dot"></span>Running') +
         (Tile 'tile' '&#127912;' 'ComfyUI' '') +
         (Tile 'tile' '&#9889;' 'vLLM' '') +
         (Tile 'tile' '&#129302;' 'OpenClaw' '') +
         (Tile 'tile disabled' '&#127916;' 'Hermes' 'Coming soon')

# ellipsis byte is unreliable through PS 5.1, so match the gpu-body slot with a regex
$shell = [regex]::Replace($shell, '(?s)<div id="gpu-body">.*?</div></div>', "<div id=`"gpu-body`">$gpu</div>")
$shell = $shell.Replace('<div id="places-body"></div>', "<div id=`"places-body`">$places</div>")
$shell = $shell.Replace('<div class="pad" id="pad"></div>', "<div class=`"pad`" id=`"pad`">$tiles</div>")
$shell = $shell.Replace('--:--', '14:32')
$shell = $shell.Replace('<span id="mi-gpu">GPU</span>', '<span>RTX 5090</span>').Replace('<span class="led" id="led-gpu"></span>','<span class="led ok"></span>')
$shell = $shell.Replace('<span id="mi-sys">System</span>', '<span>System</span>').Replace('<span class="led" id="led-sys"></span>','<span class="led ok"></span>')
$shell = $shell.Replace('<span id="net-label">Network</span>', '<span>Online</span>').Replace('<span class="led" id="led-net"></span>','<span class="led net-online"></span>')
$shell = $shell.Replace('<span id="gpu-model">Model Manager</span>', '<span>Qwen2.5-7B-Instruct &middot; loaded</span>')
$shell = $shell.Replace('<div class="hero-date" id="hero-date"></div>', '<div class="hero-date" id="hero-date">Friday, 19 June</div>')
$shell = $shell.Replace('<div class="hero-zone" id="hero-zone"></div>', '<div class="hero-zone" id="hero-zone">Europe/Paris &middot; France 🇫🇷</div>')
$shell = [regex]::Replace($shell, '<p id="hero-sub">.*?</p>', '<p id="hero-sub">Good afternoon, Samuel. Your private, local-AI workstation.</p>')

# allow a variant override file (the redesign) to replace the <main> block
$variantPath = "D:\Cloudless\scripts\desk-$Variant.html"
if (Test-Path $variantPath) {
  $mainNew = Get-Content $variantPath -Raw -Encoding UTF8
  $shell = [regex]::Replace($shell, '(?s)<main>.*?</main>', { param($m) $mainNew })
}

$themeAttr = if ($Theme) { " data-theme=`"$Theme`" style=`"color-scheme:light`"" } else { '' }
# optional variant CSS overrides (authored while iterating on the redesign)
$variantCss = ''
$cssPath = "D:\Cloudless\scripts\desk-$Variant.css"
if (Test-Path $cssPath) { $variantCss = "<style>" + (Get-Content $cssPath -Raw -Encoding UTF8) + "</style>" }
# freeze entrance animations so a static screenshot shows final state (no opacity:0)
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$page = "<!doctype html><html$themeAttr><head><meta charset=`"utf-8`">$style$variantCss$freeze</head><body>$shell</body></html>"
$tmp = "$env:TEMP\desk-render.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))

$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$outPath = "$env:TEMP\$Out"; if (Test-Path $outPath) { Remove-Item $outPath }
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1440,1100 --screenshot="$outPath" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1200
if (Test-Path $outPath) { "OK $outPath $((Get-Item $outPath).Length) bytes" } else { "NO SCREENSHOT" }
