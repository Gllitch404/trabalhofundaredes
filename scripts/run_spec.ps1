param(
    [Parameter(Mandatory=$false)]
    [string]$IP = "",
    [Parameter(Mandatory=$false)]
    [string]$Dir = "."
)

$goBin = if (Get-Command go -ErrorAction SilentlyContinue) { "go" } else { "$env:LOCALAPPDATA\go\bin\go.exe" }

if (-not (Test-Path (Join-Path $Dir "roteadores.txt"))) {
    Write-Error "Arquivo roteadores.txt não encontrado em '$Dir'. Crie-o usando roteadores.txt.example como base."
    exit 1
}

$argsList = @("run", ".", "-port", "6000", "-dir", $Dir)
if ($IP -ne "") {
    $argsList += @("-ip", $IP)
}

Write-Host "Iniciando roteador em MODO ESPECIFICACAO (UDP porta 6000)..." -ForegroundColor Cyan
& $goBin @argsList
