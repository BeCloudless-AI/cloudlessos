# Renders a language-model detail card with the "Advanced - engine launch command"
# section expanded (real CSS; markup mirrors renderModelDetail + paintLaunch).
$src = 'D:\Cloudless\orchestrator\internal\api\web\index.html'
$h = Get-Content $src -Raw -Encoding UTF8
$style = [regex]::Match($h, '(?s)<style>.*?</style>').Value

$prefix = 'docker run -d --name cloudless-vllm --restart unless-stopped --gpus all --network cloudless --network-alias cloudless-ai -p 127.0.0.1:8000:8000 -v cloudless-hf:/root/.cache/huggingface vllm/vllm-openai:latest'
$cmd = 'Qwen/Qwen2.5-7B-Instruct --served-model-name cloudless --gpu-memory-utilization 0.85 --max-model-len 16384 --enable-auto-tool-choice --tool-call-parser hermes'
$preview = "$prefix $cmd"

$body = @"
<div class="mm" style="height:auto!important;max-height:none!important">
  <div class="lp-head"><div class="lp-ttl">Model manager</div><button class="lp-x">&times;</button></div>
  <div class="mm-body"><div id="mm-tab-body">
    <button class="lp-back">&larr; All models</button>
    <div class="md-detail">
      <div class="md-hero">
        <div class="md-ic2">&#129504;</div>
        <div class="md-hd">
          <div class="md-nm">Qwen2.5 7B Instruct</div>
          <div class="md-sub">Qwen/Qwen2.5-7B-Instruct</div>
          <div class="mdl-tags md-caps"><span class="mdl-cap">tools</span></div>
        </div>
      </div>
      <div class="md-banner ready">Downloaded and ready to launch.</div>
      <p class="md-desc">A strong general-purpose instruct model with tool calling.</p>
      <div class="md-specgrid">
        <div class="md-spec"><span class="md-k">Parameters</span><span class="md-v">7B</span></div>
        <div class="md-spec"><span class="md-k">Context</span><span class="md-v">32K tokens</span></div>
        <div class="md-spec"><span class="md-k">VRAM needed</span><span class="md-v">~16 GB</span></div>
      </div>
      <div class="md-acts"><button class="btn primary">Launch</button></div>
      <div class="md-adv">
        <button class="md-adv-tog open">Advanced - engine launch command <span class="md-adv-chev">&#9656;</span></button>
        <div class="md-adv-body">
          <div class="md-adv-note">The command Cloudless runs to launch this model with <b>vLLM</b>. Edit it and save - your version is used every time this model launches on this engine. The container name, GPU and network flags are managed by Cloudless. <span class="md-adv-badge">customized</span></div>
          <label class="md-adv-lbl">Container command - arguments passed to vLLM</label>
          <textarea class="md-adv-cmd" rows="5">$cmd</textarea>
          <label class="md-adv-lbl">Full command preview</label>
          <pre class="md-adv-prev">$preview</pre>
          <div class="md-adv-acts">
            <button class="btn primary">Save</button>
            <button class="btn">Save &amp; launch</button>
            <button class="btn">Reset to default</button>
          </div>
        </div>
      </div>
    </div>
  </div></div>
</div>
"@
$freeze = "<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>"
$bodyCss = "<style>body{background:#10121c;padding:26px;display:flex;justify-content:center}</style>"
$page = "<!doctype html><html><head><meta charset=`"utf-8`">$style$freeze$bodyCss</head><body>$body</body></html>"
$tmp = "$env:TEMP\models-launch.html"
[System.IO.File]::WriteAllText($tmp, $page, (New-Object System.Text.UTF8Encoding($false)))
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$out = "$env:TEMP\models-launch.png"
$edgeProfile = if ($env:CLOUDLESS_VISUAL_EDGE_PROFILE) { $env:CLOUDLESS_VISUAL_EDGE_PROFILE } else { Join-Path $env:TEMP "cloudless-visual-edge-$PID" }
& $edge --headless=new --disable-gpu --hide-scrollbars --no-first-run "--user-data-dir=$edgeProfile" --force-device-scale-factor=1 --window-size=900,1000 --screenshot="$out" ("file:///" + ($tmp -replace '\\','/')) 2>$null
Start-Sleep -Milliseconds 1000
if (Test-Path $out) { "OK $out" } else { "NO SHOT" }
