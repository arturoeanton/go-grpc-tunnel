#!/bin/bash

# Script para ejecutar el cliente/agente gRPC del túnel

# --- Configuración ---
# Cambia estas variables si es necesario
MY_AUTH_TOKEN="e4d90a1b2c87f3e6a5b1d0c9e8f7a6b5d4c3b2a10f9e8d7c6b5a4f3e2d1c0b9a" # Pega tu token aquí
CERT_DIR="$(pwd)/certs"
#CLIENT_BINARY="./bin/tunnelcli"
CLIENT_MAIN_GO="./cmd/client/main.go" # Ruta al archivo main del cliente

# Dirección donde contactar al servidor gRPC (el puerto del agente :50051)
GRPC_SERVER_AGENT_ADDR="127.0.0.1:50051"

# Dirección del servidor Firebird REAL (al que se conecta este agente)
# Cambiar si Firebird no corre en 127.0.0.1:3050 o si usas Docker con otro nombre/ip
FB_ADDRESS="127.0.0.1:30505"

# --- Variables de Entorno para el Cliente/Agente ---
export TUNNEL_AUTH_TOKEN=$MY_AUTH_TOKEN
export TUNNEL_SERVER_ADDR=$GRPC_SERVER_AGENT_ADDR
export FIREBIRD_LOCAL_ADDR=$FB_ADDRESS
export TUNNEL_CA_CERT="$CERT_DIR/server.crt"
export TUNNEL_INSECURE="false" # Poner "true" sólo si deshabilitas TLS
export DANGEROUS_SKIP_VERIFY="false" # Poner "true" habilita el skip verify de TLS

# --- Ejecución ---
echo ">>> Iniciando Cliente/Agente gRPC del Túnel..."
echo "    Conectando a Servidor gRPC: $TUNNEL_SERVER_ADDR"
echo "    Proxy hacia Firebird local: $FIREBIRD_LOCAL_ADDR"
echo "    Usando Token: $TUNNEL_AUTH_TOKEN"
echo "    Usando CA Cert: $TUNNEL_CA_CERT"
echo "--- Logs del Cliente/Agente ---"

# Ejecuta el binario del cliente
#$CLIENT_BINARY
go run $CLIENT_MAIN_GO
# Si el binario falla en ejecutarse, el script terminará con error
if [ $? -ne 0 ]; then
  echo ">>> Error al ejecutar el cliente/agente."
  exit 1
fi