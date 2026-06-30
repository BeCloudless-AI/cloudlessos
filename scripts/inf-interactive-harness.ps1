# Renders the Activity tab with the REAL chart code + a forced hover (crosshair + tooltip), to PNG.
$lines = Get-Content 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$all = $lines -join "`n"
$style = ([regex]::Match($all, '(?s)<style>.*?</style>')).Value
# Real chart code block: from "function infChart" to "function infHideTip" line.
$startIdx = ($lines | Select-String -Pattern '^function infChart\(' | Select-Object -First 1).LineNumber - 1
$endIdx   = ($lines | Select-String -Pattern '^function infHideTip\(' | Select-Object -First 1).LineNumber - 1
# include the preceding comment line for infChart
$chartCode = $lines[($startIdx-1)..$endIdx] -join "`n"
$infRate = ($lines | Select-String -Pattern '^const infRate =' | Select-Object -First 1).Line

$gen=@();$run=@();$ttft=@();$tpot=@()
for ($i=0; $i -lt 80; $i++) {
  $gen  += [int][math]::Round(120 + 90*[math]::Sin($i/6.0) + 40*[math]::Sin($i/2.3))
  $run  += [int][math]::Max(0,[math]::Round(2 + 2*[math]::Sin($i/7.0)))
  $ttft += [int][math]::Round(180 + 60*[math]::Sin($i/9.0))
  $tpot += [int][math]::Round(14 + 5*[math]::Sin($i/5.0))
}
$genJs='['+($gen -join ',')+']'; $runJs='['+($run -join ',')+']'; $ttftJs='['+($ttft -join ',')+']'; $tpotJs='['+($tpot -join ',')+']'

$body = @"
<div class="mm" style="height:auto!important;max-height:none!important">
  <div class="lp-head"><div class="lp-ttl">Inference</div>
    <div class="inf-head on"><span class="inf-live"></span><b>vLLM</b><span class="sep">&middot;</span>Qwen2.5-1.5B-Instruct<span class="ih-state">live</span></div>
    <button class="lp-x">&times;</button></div>
  <div class="mm-body"><div id="inf-body">
    <div class="mm-tabs"><button class="mm-tab on">Activity</button><button class="mm-tab">Usage</button><button class="mm-tab">Engine</button><button class="mm-tab">API access</button></div>
    <div id="inf-panel">
      <div class="inf-hero">
        <div class="inf-hero-head">
          <div><div class="inf-hero-val"><span>182</span><small>tokens / sec</small></div><div class="inf-hero-cap">output generation speed</div></div>
          <div class="inf-hero-aux"><div>peak <b>240</b> tok/s</div><div>prompt <b>1.2k</b> tok/s</div></div>
        </div>
        <canvas class="inf-hero-canvas" id="c-tput"></canvas>
        <div class="inf-hero-foot"><div class="inf-legend"><span><i style="background:var(--blue)"></i>output tokens / second</span></div><span class="inf-time">last 60 seconds &middot; hover to inspect</span></div>
      </div>
      <div class="inf-grid">
        <div class="inf-metric"><div class="im-label">Active requests</div><div class="im-val"><span>3</span></div><div class="im-sub">2 queued</div><canvas class="im-spark" id="c-active"></canvas></div>
        <div class="inf-metric"><div class="im-label">Memory &middot; KV cache</div><div class="im-ringwrap"><canvas class="im-ring" id="c-kv"></canvas><span class="im-ringval">61%</span></div></div>
        <div class="inf-metric"><div class="im-label">Time to first token</div><div class="im-val"><span>196</span><small>ms</small></div><div class="im-sub">how fast a reply starts</div><canvas class="im-spark" id="c-ttft"></canvas></div>
        <div class="inf-metric"><div class="im-label">Per output token</div><div class="im-val"><span>16</span><small>ms</small></div><div class="im-sub">speed of each token</div><canvas class="im-spark" id="c-tpot"></canvas></div>
      </div>
    </div>
  </div></div>
</div>
"@

$script = @"
<script>
let infTipEl = null; const INF_SAMPLE_MS = 750;
function escapeHtml(s){return String(s).replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));}
const cssRgb = v => { const h = getComputedStyle(document.documentElement).getPropertyValue(v).trim().replace('#',''); if(h.length===3)return[0,1,2].map(i=>parseInt(h[i]+h[i],16)); if(h.length>=6)return[0,2,4].map(i=>parseInt(h.slice(i,i+2),16)); return[130,130,130]; };
$infRate
$chartCode
const blue = cssRgb('--blue'), muted = cssRgb('--muted');
infChart('c-tput',[{rgb:blue,data:$genJs}],0,{glow:true,label:'output',fmt:v=>infRate(v)+' tok/s'});
infChart('c-active',[{rgb:blue,data:$runJs}],0,{label:'active requests',fmt:v=>infRate(v)});
infChart('c-ttft',[{rgb:muted,data:$ttftJs}],0,{label:'time to first token',fmt:v=>Math.round(v)+' ms'});
infChart('c-tpot',[{rgb:muted,data:$tpotJs}],0,{label:'per output token',fmt:v=>Math.round(v)+' ms'});
setTimeout(function(){
  const cv=document.getElementById('c-tput'); const g=cv._geo; const hi=Math.round((g.n-1)*0.62);
  cv._hi=hi; infChart('c-tput',cv._args.series,cv._args.fixedMax,cv._args.opts);
  const rect=cv.getBoundingClientRect();
  const data=$genJs; const v=data[hi];
  infShowTip({clientX:rect.left+g.xOf(hi),clientY:rect.top+46}, '<b>'+infRate(v)+' tok/s</b><span>output &middot; '+Math.round((g.n-1-hi)*INF_SAMPLE_MS/1000)+'s ago</span>');
},120);
</script>
"@
$freeze = "<style>*,*::before,*::after{animation:none!important}</style>"
$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;justify-content:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$body$script</body></html>"
$tmp = "$env:TEMP\inf-interactive.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\inf-interactive2.png"
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1240,720 --virtual-time-budget=1400 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 700
if (Test-Path $out) { "OK $out (chart lines $($startIdx+1)..$($endIdx+1))" } else { "NO SHOT" }
