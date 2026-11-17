param(
    [switch]$SetupOnly
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$simRoot = Join-Path $root "sim"
$goBin = if (Get-Command go -ErrorAction SilentlyContinue) { "go" } else { "$env:LOCALAPPDATA\go\bin\go.exe" }

# Topologia de 4 roteadores em loopback (127.0.0.1) com portas 6000..6003:
#   R1 (10.0.0.1 :6000) --10-- R2 (10.0.0.2 :6001) --10-- R4 (10.0.0.4 :6003)
#    \--5-- R3 (10.0.0.3 :6002) --50--/
$peersContent = @"
10.0.0.1;127.0.0.1:6000
10.0.0.2;127.0.0.1:6001
10.0.0.3;127.0.0.1:6002
10.0.0.4;127.0.0.1:6003
"@

$routers = @(
    @{ Name = "r1"; IP = "10.0.0.1"; Port = 6000; Neighbors = "10.0.0.2;10`n10.0.0.3;5" },
    @{ Name = "r2"; IP = "10.0.0.2"; Port = 6001; Neighbors = "10.0.0.1;10`n10.0.0.4;10" },
    @{ Name = "r3"; IP = "10.0.0.3"; Port = 6002; Neighbors = "10.0.0.1;5`n10.0.0.4;50" },
    @{ Name = "r4"; IP = "10.0.0.4"; Port = 6003; Neighbors = "10.0.0.2;10`n10.0.0.3;50" }
)

$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
foreach ($r in $routers) {
    $dir = Join-Path $simRoot $r.Name
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $dir "roteadores.txt"), $r.Neighbors + "`n", $utf8NoBom)
    [System.IO.File]::WriteAllText((Join-Path $dir "peers.txt"), $peersContent + "`n", $utf8NoBom)
}

Write-Host "Arquivos de simulação criados em $simRoot (r1..r4)." -ForegroundColor Green

if ($SetupOnly) {
    Write-Host "Comandos para rodar manualmente em 4 terminais:" -ForegroundColor Yellow
    foreach ($r in $routers) {
        $dir = Join-Path $simRoot $r.Name
        Write-Host "  `"$goBin`" run . -local -ip $($r.IP) -port $($r.Port) -dir `"$dir`""
    }
    exit 0
}

foreach ($r in $routers) {
    $dir = Join-Path $simRoot $r.Name
    $cmd = "cd `"$root`"; Write-Host 'Roteador $($r.Name) ($($r.IP) -> 127.0.0.1:$($r.Port))' -ForegroundColor Cyan; & `"$goBin`" run . -local -ip $($r.IP) -port $($r.Port) -dir `"$dir`""
    Start-Process powershell -ArgumentList "-NoExit", "-Command", $cmd
}

Write-Host "4 janelas iniciadas! Aguarde ~5-10s para convergência inicial e teste comandos como 'lsdb', 'rotas', 'data 10.0.0.4 Oi!'." -ForegroundColor Cyan
