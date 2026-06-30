# Renders the Activity → Live view in the "no engine serving" state: the slim engine
# notice + the always-live Hardware band (real CSS, real infHwSkeleton markup, real
# infGauge rings; sparklines drawn by a small harness stand-in for infChart).
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value

# --- pull the real infHwSkeleton() markup out of the source (between its two backticks) ---
$skelFn = [regex]::Match($h, '(?s)function infHwSkeleton\(\)\s*\{.*?return\s*`(.*?)`;').Groups[1].Value
if (-not $skelFn) { Write-Error 'could not extract infHwSkeleton'; exit 1 }

$body = @"
<div class="mm" style="height:auto!important;max-height:none!important">
  <div class="lp-head"><div class="lp-ttl">Inference</div>
    <div class="inf-head"><b>vLLM</b><span class="ih-state">loading…</span></div>
    <button class="lp-x">&times;</button></div>
  <div class="mm-body"><div id="inf-body">
    <div class="mm-tabs"><button class="mm-tab on">Activity</button><button class="mm-tab">Usage</button><button class="mm-tab">Power</button><button class="mm-tab">Engine</button><button class="mm-tab">API access</button></div>
    <div id="inf-panel">
      <div class="inf-actbar"><div class="us-range"><button class="us-rbtn on">Live</button><button class="us-rbtn">Hour</button><button class="us-rbtn">Day</button><button class="us-rbtn">Month</button><button class="us-rbtn">Year</button></div></div>
      <div id="inf-act">
        <div id="inf-eng">
          <div class="inf-engnote">
            <div class="inf-engnote-msg">vLLM is running, but it isn't reporting live metrics yet.</div>
            <div class="inf-engnote-sub">Throughput, latency and KV-cache metrics appear here once a model is loaded and serving. Hardware metrics below are live.</div>
            <button class="btn primary" style="margin-top:14px">Turn on live metrics (restarts the engine)</button>
          </div>
        </div>
        <div class="inf-hwwrap" id="inf-hw">$skelFn</div>
      </div>
    </div>
  </div></div>
</div>
"@

# infGauge copied verbatim from index.html (small, self-contained), + a harness sparkline.
$script = @"
const cssRgb = v => { const h = getComputedStyle(document.documentElement).getPropertyValue(v).trim().replace('#',''); if (h.length===3) return [0,1,2].map(i=>parseInt(h[i]+h[i],16)); if (h.length>=6) return [0,2,4].map(i=>parseInt(h.slice(i,i+2),16)); return [130,130,130]; };
const setText=(id,v)=>{const e=document.getElementById(id);if(e)e.textContent=v;};
const setHTML=(id,v)=>{const e=document.getElementById(id);if(e)e.innerHTML=v;};
function infGauge(id, pct, rgb) {
  const cv = document.getElementById(id); if (!cv) return;
  const dpr = window.devicePixelRatio || 1, w = cv.clientWidth, h = cv.clientHeight; if (!w || !h) return;
  if (cv.width !== Math.round(w*dpr) || cv.height !== Math.round(h*dpr)) { cv.width = Math.round(w*dpr); cv.height = Math.round(h*dpr); }
  const ctx = cv.getContext('2d'); ctx.setTransform(dpr,0,0,dpr,0,0); ctx.clearRect(0,0,w,h);
  const cx=w/2, cy=h/2, r=Math.min(w,h)/2-9, a0=Math.PI*0.75, a1=Math.PI*2.25;
  ctx.lineCap='round'; ctx.lineWidth=9;
  ctx.strokeStyle=getComputedStyle(document.documentElement).getPropertyValue('--hair').trim();
  ctx.beginPath(); ctx.arc(cx,cy,r,a0,a1); ctx.stroke();
  const p=Math.max(0,Math.min(1,pct/100)); const lite=rgb.map(c=>Math.min(255,c+55));
  const g=ctx.createLinearGradient(0,0,w,h); g.addColorStop(0,'rgb('+rgb[0]+','+rgb[1]+','+rgb[2]+')'); g.addColorStop(1,'rgb('+lite[0]+','+lite[1]+','+lite[2]+')');
  ctx.strokeStyle=g; ctx.shadowColor='rgba('+rgb[0]+','+rgb[1]+','+rgb[2]+',0.45)'; ctx.shadowBlur=7;
  ctx.beginPath(); ctx.arc(cx,cy,r,a0,a0+(a1-a0)*p); ctx.stroke();
}
function spark(id, data, rgb) { // harness stand-in for infChart (area+line)
  const cv=document.getElementById(id); if(!cv) return; const dpr=window.devicePixelRatio||1, w=cv.clientWidth, h=cv.clientHeight; if(!w||!h) return;
  cv.width=Math.round(w*dpr); cv.height=Math.round(h*dpr); const ctx=cv.getContext('2d'); ctx.setTransform(dpr,0,0,dpr,0,0); ctx.clearRect(0,0,w,h);
  let max=0; data.forEach(v=>{if(v>max)max=v;}); max=Math.max(max,1)*1.15; const pad=3;
  const xOf=i=>pad+(w-2*pad)*(i/(data.length-1)), yOf=v=>h-pad-(h-2*pad)*Math.min(1,v/max);
  const pts=data.map((v,i)=>[xOf(i),yOf(v)]);
  ctx.beginPath(); ctx.moveTo(pts[0][0],pts[0][1]); pts.forEach(p=>ctx.lineTo(p[0],p[1])); ctx.lineTo(pts[pts.length-1][0],h-pad); ctx.lineTo(pts[0][0],h-pad); ctx.closePath();
  const g=ctx.createLinearGradient(0,0,0,h); g.addColorStop(0,'rgba('+rgb[0]+','+rgb[1]+','+rgb[2]+',0.28)'); g.addColorStop(1,'rgba('+rgb[0]+','+rgb[1]+','+rgb[2]+',0)'); ctx.fillStyle=g; ctx.fill();
  ctx.beginPath(); ctx.moveTo(pts[0][0],pts[0][1]); pts.forEach(p=>ctx.lineTo(p[0],p[1])); ctx.strokeStyle='rgb('+rgb[0]+','+rgb[1]+','+rgb[2]+')'; ctx.lineWidth=2; ctx.stroke();
}
const blue=cssRgb('--blue'), accent=cssRgb('--accent');
const rnd=(seed)=>{let x=Math.sin(seed)*10000;return x-Math.floor(x);};
// GPU 5090: util 64%, mem 21.4/32, power 412W cap 600, temp 61C
setText('m-gutil','64%'); infGauge('c-gutil',64,blue); setText('m-gutilsub',' ');
setText('m-gmem','67%'); infGauge('c-gmem',67,blue); setText('m-gmemsub','21.4 / 32.0 GB');
setText('m-gpow','412'); setHTML('m-gpowsub','61°C · 600 W cap'); spark('c-gpow', Array.from({length:40},(_,i)=>340+120*Math.abs(Math.sin(i/5))), accent);
setText('m-cpu','23'); setText('m-cpuname','Ryzen 9 7950X · 32 threads'); spark('c-cpu', Array.from({length:40},(_,i)=>14+22*Math.abs(Math.sin(i/4))), blue);
setText('m-ram','29%'); infGauge('c-ram',29,blue); setText('m-ramsub','18.4 / 64.0 GB');
"@

$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;justify-content:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$body<script>$script</script></body></html>"
$tmp = "$env:TEMP\inf-hardware.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\inf-hardware.png"
& $edge --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 --window-size=1240,900 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1200
if (Test-Path $out) { "OK $out" } else { "NO SHOT" }
