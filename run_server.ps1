# run_server.ps1
# Script para ejecutar el servidor gRPC del túnel en PowerShell

# --- Configuración ---
$MY_AUTH_TOKEN = "e4d90a1b2c87f3e6a5b1d0c9e8f7a6b5d4c3b2a10f9e8d7c6b5a4f3e2d1c0b9a"
$CERT_DIR = Join-Path (Get-Location) "certs"
$SERVER_MAIN_GO = ".\cmd\server\main.go"  # Ruta al archivo main del servidor

# --- Variables de Entorno para el Servidor ---
$env:TUNNEL_AUTH_TOKEN = $MY_AUTH_TOKEN
$env:TUNNEL_SERVER_CERT = Join-Path $CERT_DIR "server.crt"
$env:TUNNEL_SERVER_KEY = Join-Path $CERT_DIR "server.key"
$env:TUNNEL_GRPC_PORT = ":50051"
$env:TUNNEL_EXTERNAL_PORT = ":8080"
$env:TUNNEL_INSECURE = "false"

# --- Ejecución ---
Write-Host ">>> Iniciando Servidor gRPC del Túnel..."
Write-Host "    Token: $env:TUNNEL_AUTH_TOKEN"
Write-Host "    Certificado: $env:TUNNEL_SERVER_CERT"
Write-Host "    Clave: $env:TUNNEL_SERVER_KEY"
Write-Host "    Escuchando gRPC en $env:TUNNEL_GRPC_PORT (para agente)"
Write-Host "    Escuchando Firebird en $env:TUNNEL_EXTERNAL_PORT (para clientes)"
Write-Host "--- Logs del Servidor ---"

# Ejecuta el servidor
go run $SERVER_MAIN_GO

if ($LASTEXITCODE -ne 0) {
    Write-Host ">>> Error al ejecutar el servidor." -ForegroundColor Red
    exit 1
}
