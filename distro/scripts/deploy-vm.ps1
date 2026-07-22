[CmdletBinding()]
param(
    [string]$VMName = "CloudlessOS",
    [string]$VMUser = "samcllo",
    [ValidateSet("orchestrator", "shell", "branding", "hardware", "firstboot", "all")]
    [string[]]$Packages = @("orchestrator"),
    [int]$SshPort = 2222,
    [SecureString]$Password,
    [string]$HostKey,
    [switch]$SkipBuild,
    [switch]$NoDesktopRestart,
    [switch]$KeepSshForward
)

$ErrorActionPreference = "Stop"
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$VBoxManage = "D:\VirtualBox\VBoxManage.exe"
$Plink = "C:\Program Files\PuTTY\plink.exe"
$Pscp = "C:\Program Files\PuTTY\pscp.exe"
$ForwardName = "cloudless-dev-ssh"
$ForwardCreated = $false
$PasswordPtr = [IntPtr]::Zero
$PlainPassword = $null

function Require-CommandPath([string]$Path, [string]$Label) {
    if (-not (Test-Path -LiteralPath $Path)) {
        throw "$Label was not found at $Path"
    }
}

function Get-VMState {
    $line = & $VBoxManage showvminfo $VMName --machinereadable 2>$null |
        Select-String '^VMState=' | Select-Object -First 1
    if (-not $line) { throw "VirtualBox VM '$VMName' was not found" }
    return (($line.ToString() -split '=', 2)[1]).Trim('"')
}

function Test-LocalPort([int]$Port) {
    $client = [System.Net.Sockets.TcpClient]::new()
    try {
        $task = $client.ConnectAsync("127.0.0.1", $Port)
        return $task.Wait(500) -and $client.Connected
    } catch {
        return $false
    } finally {
        $client.Dispose()
    }
}

function Remove-TemporaryForward {
    if (-not $ForwardCreated -or $KeepSshForward) { return }
    $state = Get-VMState
    if ($state -eq "running") {
        & $VBoxManage controlvm $VMName natpf1 delete $ForwardName 2>$null | Out-Null
    } else {
        & $VBoxManage modifyvm $VMName --natpf1 delete $ForwardName 2>$null | Out-Null
    }
}

try {
    Require-CommandPath $VBoxManage "VBoxManage"
    Require-CommandPath $Plink "PuTTY plink"
    Require-CommandPath $Pscp "PuTTY pscp"
    if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) {
        throw "WSL is required to build the Linux packages"
    }

    if (-not $SkipBuild) {
        Write-Host "==> Building CloudlessOS packages"
        $RepoRootForWsl = $RepoRoot.Replace("\", "/")
        $RepoRootWsl = (& wsl.exe wslpath -a $RepoRootForWsl).Trim()
        if ($LASTEXITCODE -ne 0 -or -not $RepoRootWsl) { throw "Could not resolve the repository path in WSL" }
        $BuilderDir = "$RepoRootWsl/distro/docker/package-builder"
        & wsl.exe docker build --quiet --tag cloudless-package-builder:dev $BuilderDir
        if ($LASTEXITCODE -ne 0) { throw "Package-builder image failed" }
        & wsl.exe docker run --rm --volume "${RepoRootWsl}:/src" --workdir /src `
            cloudless-package-builder:dev bash distro/scripts/test-packages.sh
        if ($LASTEXITCODE -ne 0) { throw "CloudlessOS package build failed" }
    }

    $PackageMap = @{
        orchestrator = "cloudless-orchestrator"
        shell         = "cloudless-shell"
        branding      = "cloudless-branding"
        hardware      = "cloudless-hardware"
        firstboot     = "cloudless-firstboot"
    }
    $Selected = if ($Packages -contains "all") { @($PackageMap.Keys) } else { @($Packages) }
    $Debs = foreach ($shortName in $Selected) {
        $packageName = $PackageMap[$shortName]
        $deb = Get-ChildItem -LiteralPath (Join-Path $RepoRoot "distro\out\packages") `
            -Filter "${packageName}_*.deb" | Sort-Object LastWriteTime -Descending | Select-Object -First 1
        if (-not $deb) { throw "No built package found for $packageName" }
        $deb.FullName
    }

    $vmInfo = & $VBoxManage showvminfo $VMName --machinereadable 2>$null
    if ($LASTEXITCODE -ne 0) { throw "VirtualBox VM '$VMName' was not found" }
    $existingForward = $vmInfo | Select-String "=$([regex]::Escape('"' + $ForwardName + ','))"
    $state = Get-VMState
    if (-not $existingForward) {
        Write-Host "==> Opening temporary SSH access on 127.0.0.1:$SshPort"
        $rule = "$ForwardName,tcp,127.0.0.1,$SshPort,,22"
        if ($state -eq "running") {
            & $VBoxManage controlvm $VMName natpf1 $rule | Out-Null
        } else {
            & $VBoxManage modifyvm $VMName --natpf1 $rule | Out-Null
        }
        if ($LASTEXITCODE -ne 0) { throw "Could not add the VirtualBox SSH forwarding rule" }
        $ForwardCreated = $true
    }

    if ($state -ne "running") {
        Write-Host "==> Starting $VMName"
        & $VBoxManage startvm $VMName --type headless | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "Could not start '$VMName'" }
    }

    Write-Host "==> Waiting for SSH"
    $sshReady = $false
    for ($attempt = 0; $attempt -lt 45; $attempt++) {
        if (Test-LocalPort $SshPort) { $sshReady = $true; break }
        Start-Sleep -Seconds 2
    }
    if (-not $sshReady) { throw "SSH did not become available on port $SshPort" }

    if (-not $Password) {
        $Password = Read-Host "Password for $VMUser in $VMName" -AsSecureString
    }
    $PasswordPtr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Password)
    $PlainPassword = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($PasswordPtr)

    if (-not $HostKey) {
        $probe = (& $Plink -v -batch -P $SshPort -pw $PlainPassword "$VMUser@127.0.0.1" exit 2>&1 | Out-String)
        $match = [regex]::Match($probe, 'ssh-ed25519 255 SHA256:[A-Za-z0-9+/=]+')
        if (-not $match.Success) { throw "Could not determine the VM SSH host key" }
        $HostKey = $match.Value
    }

    $BaseArgs = @("-batch", "-P", "$SshPort", "-hostkey", $HostKey, "-pw", $PlainPassword)
    Write-Host "==> Uploading $($Debs.Count) package(s)"
    & $Plink @BaseArgs "$VMUser@127.0.0.1" "rm -rf /tmp/cloudless-deploy && mkdir -p /tmp/cloudless-deploy"
    if ($LASTEXITCODE -ne 0) { throw "Could not prepare the VM upload directory" }
    & $Pscp @BaseArgs @Debs "$VMUser@127.0.0.1:/tmp/cloudless-deploy/"
    if ($LASTEXITCODE -ne 0) { throw "Package upload failed" }

    $PasswordBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($PlainPassword))
    $restartDesktop = if ($NoDesktopRestart) { "" } else { " && systemctl restart lightdm" }
    $remote = "printf '%s' '$PasswordBase64' | base64 -d | sudo -S env DEBIAN_FRONTEND=noninteractive apt-get install -y /tmp/cloudless-deploy/*.deb && sudo systemctl restart cloudlessd$restartDesktop"
    Write-Host "==> Installing and activating packages"
    & $Plink @BaseArgs "$VMUser@127.0.0.1" $remote
    if ($LASTEXITCODE -ne 0) { throw "Package installation failed" }

    Write-Host "==> Verifying CloudlessOS"
    $health = $null
    for ($attempt = 0; $attempt -lt 20; $attempt++) {
        $health = (& $Plink @BaseArgs "$VMUser@127.0.0.1" "curl -fsS http://127.0.0.1:8765/api/health" 2>$null | Out-String).Trim()
        if ($LASTEXITCODE -eq 0 -and $health) { break }
        Start-Sleep -Seconds 1
    }
    if (-not $health) { throw "CloudlessOS did not return a healthy response after deployment" }
    & $Plink @BaseArgs "$VMUser@127.0.0.1" "rm -rf /tmp/cloudless-deploy" | Out-Null

    Write-Host "==> Deployment complete: $health" -ForegroundColor Green
    if ($Selected -contains "branding" -or $Selected -contains "hardware") {
        Write-Host "A VM reboot is recommended for branding or hardware changes." -ForegroundColor Yellow
    }
} finally {
    try { Remove-TemporaryForward } catch { Write-Warning $_ }
    if ($PasswordPtr -ne [IntPtr]::Zero) {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($PasswordPtr)
    }
    $PlainPassword = $null
}
