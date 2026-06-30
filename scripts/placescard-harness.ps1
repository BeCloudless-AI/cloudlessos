# Focused render of the Places card for review.
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}body{padding:34px}</style>"
function Row($ic, $nm, $d) {
  "<div class=`"place`"><span class=`"pic`">$ic</span><span class=`"pl-txt`"><span class=`"pl`">$nm</span><span class=`"pl-d`">$d</span></span><span class=`"pl-go`"><span class=`"pl-go-tx`">Open</span><span class=`"pl-go-ar`">&#8599;</span></span></div>"
}
$rows = (Row '&#128230;' 'Models' 'AI models you download are stored here') +
        (Row '&#128444;' 'Outputs' 'Images and files your apps generate') +
        (Row '&#128451;' 'Workspace' 'Your own projects, notebooks and data') +
        (Row '&#11015;' 'Downloads' 'Files Cloudless downloads for you')
$body = @"
<div class="wall"></div>
<section class="card" style="max-width:440px;margin:0 auto">
  <div class="card-head"><span class="card-ic">&#128193;</span><h2>Places</h2></div>
  <div id="places-body">$rows</div>
</section>
"@
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze</head><body>$body</body></html>"
$tmp = "$env:TEMP\placescard.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\placescard.png"
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=2 --window-size=520,420 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null | Out-Null
if (Test-Path $out) { "OK $out" } else { "NO SHOT" }
