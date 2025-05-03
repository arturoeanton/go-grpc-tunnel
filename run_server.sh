#!/bin/bash

# Script para ejecutar el servidor gRPC del túnel

# --- Configuración ---
# Cambia estas variables si es necesario
MY_AUTH_TOKEN="e4d90a1b2c87f3e6a5b1d0c9e8f7a6b5d4c3b2a10f9e8d7c6b5a4f3e2d1c0b9a" # Pega tu token aquí
CERT_DIR="$(pwd)/certs" # Directorio de certificados relativo a la raíz del proyecto
#SERVER_BINARY="./bin/tunnelsrv" # Ruta al binario del servidor
SERVER_MAIN_GO="./cmd/server/main.go" # Ruta al archivo main del cliente

# --- Variables de Entorno para el Servidor ---
export TUNNEL_AUTH_TOKEN=$MY_AUTH_TOKEN
export TUNNEL_SERVER_CERT="$CERT_DIR/server.crt"
export TUNNEL_SERVER_KEY="$CERT_DIR/server.key"
# Los puertos de escucha (:50051, :3051) están hardcodeados en el main.go del servidor
# Si los hiciste configurables por env vars, añádelos aquí.
export TUNNEL_GRPC_PORT=":50051"
export TUNNEL_EXTERNAL_PORT=":8080"
export TUNNEL_INSECURE="false" # Poner "true" sólo si deshabilitas TLS


# --- Ejecución ---
echo ">>> Iniciando Servidor gRPC del Túnel..."
echo "    Token: $TUNNEL_AUTH_TOKEN"
echo "    Certificado: $TUNNEL_SERVER_CERT"
echo "    Clave: $TUNNEL_SERVER_KEY"
echo "    Escuchando gRPC en $TUNNEL_GRPC_PORT (para agente)"
echo "    Escuchando Firebird en $TUNNEL_EXTERNAL_PORT (para clientes)"
echo "--- Logs del Servidor ---"

# Ejecuta el binario del servidor
#$SERVER_BINARY
go run $SERVER_MAIN_GO

# Si el binario falla en ejecutarse, el script terminará con error
if [ $? -ne 0 ]; then
  echo ">>> Error al ejecutar el servidor."
  exit 1
fi