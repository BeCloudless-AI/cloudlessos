# Verifies the wallpaper colours actually move: renders the live .wall mesh but
# PAUSES its animation at several time offsets (negative animation-delay), so each
# screenshot is a different frame of the drift+colorflow cycle. If the PNGs differ,
# the colours are moving. Output: wall-t0/t18/t36/t54.png
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"

foreach ($t in 0, 18, 36, 54) {
  # pin both animations (drift, colorflow) at time = $t seconds
  $pin = "<style>.wall::before{animation-play-state:paused!important;animation-delay:-${t}s,-${t}s!important}</style>"
  $page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$pin</head><body><div class=`"wall`"></div></body></html>"
  $tmp = "$env:TEMP\wall-$t.html"
  [System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
  $out = "$env:TEMP\wall-t$t.png"
  & $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1280,820 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null | Out-Null
  if (Test-Path $out) { "OK  $out" } else { "NO SHOT t=$t" }
}
