# run_client.ps1
# Script para ejecutar el cliente/agente gRPC del túnel en PowerShell

# --- Configuración ---
# Cambia estas variables si es necesario
$MY_AUTH_TOKEN = "e4d90a1b2c87f3e6a5b1d0c9e8f7a6b5d4c3b2a10f9e8d7c6b5a4f3e2d1c0b9a"
$CERT_DIR = Join-Path (Get-Location) "certs"
$CLIENT_MAIN_GO = ".\cmd\client\main.go"  # Ruta al archivo main del cliente

# Dirección donde contactar al servidor gRPC (el puerto del agente :50051)
$GRPC_SERVER_AGENT_ADDR = "127.0.0.1:50051"

# Dirección del servidor Firebird REAL (cambia si es necesario)
$FB_ADDRESS = "127.0.0.1:30505"

# --- Variables de Entorno para el Cliente/Agente ---
$env:TUNNEL_AUTH_TOKEN = $MY_AUTH_TOKEN
$env:TUNNEL_SERVER_ADDR = $GRPC_SERVER_AGENT_ADDR
$env:FIREBIRD_LOCAL_ADDR = $FB_ADDRESS
$env:TUNNEL_CA_CERT = Join-Path $CERT_DIR "server.crt"
$env:TUNNEL_INSECURE = "false"
$env:DANGEROUS_SKIP_VERIFY = "false"

# --- Ejecución ---
Write-Host ">>> Iniciando Cliente/Agente gRPC del Túnel..."
Write-Host "    Conectando a Servidor gRPC: $env:TUNNEL_SERVER_ADDR"
Write-Host "    Proxy hacia Firebird local: $env:FIREBIRD_LOCAL_ADDR"
Write-Host "    Usando Token: $env:TUNNEL_AUTH_TOKEN"
Write-Host "    Usando CA Cert: $env:TUNNEL_CA_CERT"
Write-Host "--- Logs del Cliente/Agente ---"

# Ejecuta el cliente con go run
go run $CLIENT_MAIN_GO

if ($LASTEXITCODE -ne 0) {
    Write-Host ">>> Error al ejecutar el cliente/agente." -ForegroundColor Red
    exit 1
}
