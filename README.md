# go-grpc-tunnel

Un túnel TCP reverso simple construido con Go y gRPC. Diseñado inicialmente para exponer una instancia de base de datos Firebird que corre detrás de un NAT/Firewall, pero potencialmente adaptable para otros servicios TCP.

Este proyecto implementa un servidor público y un agente (cliente gRPC) que establece una conexión saliente hacia el servidor. El tráfico TCP dirigido al servidor se tuneliza de forma segura a través de la conexión gRPC hacia el agente, que luego lo reenvía al servicio TCP local final (Firebird).

## Características

* **Túnel TCP Reverso:** Permite acceder a servicios en redes privadas desde el exterior.
* **Comunicación Segura:** Utiliza gRPC sobre TLS para encriptar el tráfico entre el servidor y el agente del túnel.
* **Multiplexación:** Maneja múltiples conexiones de clientes TCP concurrentemente sobre una única conexión gRPC gracias al streaming bidireccional y un sistema de IDs de conexión.
* **Autenticación Basada en Token:** El agente debe autenticarse con el servidor usando un token secreto compartido.
* **Configurable:** Principalmente a través de variables de entorno gestionadas por scripts `sh`.
* **Tecnologías:** Construido con Go, gRPC, Protocol Buffers.

## Arquitectura

El sistema consta de dos componentes principales:

1.  **Servidor (`cmd/server`):**
    * Escucha conexiones gRPC entrantes del Agente en un puerto (ej. `:50051`) usando TLS.
    * Autentica al Agente usando un token.
    * Escucha conexiones TCP entrantes de los clientes finales (ej. Clientes Firebird) en un puerto público (ej. `:8080`).
    * Gestiona el reenvío de datos entre los clientes TCP y el stream gRPC del Agente conectado.

2.  **Agente (`cmd/client`):**
    * Se ejecuta en la misma red que el servicio TCP final (ej. Servidor Firebird).
    * Establece una conexión gRPC saliente hacia el Servidor usando TLS y autenticándose con un token.
    * Recibe instrucciones del Servidor para establecer conexiones locales al servicio final (ej. Firebird en `localhost:3050`).
    * Reenvía datos entre las conexiones locales y el stream gRPC.

**Flujo de Datos (Ejemplo Firebird):**

 ```text
[Cliente Firebird App] <---TCP---> [Servidor Túnel (IP_Publica:8080)] <---gRPC/TLS (Internet)---> [Agente Túnel (Red Local)] <---TCP---> [Servidor Firebird Real (localhost:3050)]
   ```

## Prerrequisitos

* **Go:** Versión 1.18 o superior recomendada.
* **openssl:** Necesario para generar los certificados TLS autofirmados. (Instálalo con el gestor de paquetes de tu sistema si no lo tienes).
* **Git:** Para clonar el repositorio.

## Instalación

1.  Clona el repositorio (reemplaza `<repository-url>` con la URL real):
    ```bash
    git clone <repository-url>
    cd go-grpc-tunnel
    ```
2.  (Opcional) Descarga las dependencias Go:
    ```bash
    go mod tidy
    ```

## Configuración

Antes de ejecutar, necesitas generar certificados TLS y configurar los scripts de ejecución.

### 1. Generar Certificados TLS

Se utilizan certificados autofirmados para la comunicación gRPC/TLS.

1.  Crea un directorio para los certificados si no existe:
    ```bash
    mkdir certs
    ```
2.  Navega al directorio `certs`:
    ```bash
    cd certs
    ```
3.  Genera la clave privada del servidor (`server.key`) y el certificado autofirmado (`server.crt`), asegurándote de incluir `127.0.0.1` y `localhost` como Subject Alternative Names (SAN) para que funcione localmente:
    ```bash
    openssl req -x509 -newkey rsa:2048 -keyout server.key -out server.crt \
    -sha256 -days 365 -nodes \
    -subj "/CN=localhost" \
    -addext "subjectAltName = IP:127.0.0.1,DNS:localhost"
    ```
4.  Crea el certificado "CA" que usará el cliente (agente). Como es autofirmado, es una copia del certificado del servidor:
    ```bash
    cp server.crt ca.crt
    ```
5.  Regresa al directorio raíz del proyecto:
    ```bash
    cd ..
    ```

### 2. Configurar Scripts (`run_*.sh`)

Los scripts `run_server.sh` y `run_client.sh` utilizan variables de entorno para configurar y ejecutar los componentes. Revisa y ajusta las variables dentro de estos archivos según sea necesario:

* **`run_server.sh`**:
    * `MY_AUTH_TOKEN`: El token secreto que el agente debe usar para autenticarse.
    * `CERT_DIR`: Ruta al directorio de certificados (normalmente `./certs`).
    * `SERVER_MAIN_GO`: Ruta al archivo Go principal del servidor.
    * `TUNNEL_GRPC_PORT`: Puerto donde el servidor escucha conexiones gRPC del agente (ej. `:50051`). **Debe coincidir** con `GRPC_SERVER_AGENT_ADDR` en `run_client.sh`.
    * `TUNNEL_EXTERNAL_PORT`: Puerto donde el servidor escucha conexiones TCP de clientes finales (ej. `:8080`). **Este es el puerto al que se conectará tu cliente Firebird**.
    * `TUNNEL_INSECURE`: Debería ser `"false"` para usar TLS.
* **`run_client.sh`**:
    * `MY_AUTH_TOKEN`: **Debe ser el mismo token** que en `run_server.sh`.
    * `CERT_DIR`: Ruta al directorio de certificados.
    * `CLIENT_MAIN_GO`: Ruta al archivo Go principal del cliente/agente.
    * `GRPC_SERVER_AGENT_ADDR`: Dirección y puerto gRPC del servidor (ej. `127.0.0.1:50051`). **Debe coincidir** con `TUNNEL_GRPC_PORT` en `run_server.sh`.
    * `FB_ADDRESS`: Dirección y puerto del servidor Firebird REAL al que el agente se conectará localmente (ej. `127.0.0.1:3050`). Ajusta si Firebird corre en otro lugar o puerto.
    * `TUNNEL_INSECURE`: Debería ser `"false"` para usar TLS.
    * `DANGEROUS_SKIP_VERIFY`: **¡MUY IMPORTANTE!** Debe ser `"false"`. Ponerlo en `"true"` deshabilita la verificación del certificado TLS del servidor, lo cual es **extremadamente inseguro** y solo debe usarse para depuración muy temporal.

## Ejecución del Túnel

1.  Asegúrate de haber generado los certificados (ver Configuración).
2.  Asegúrate de haber configurado los scripts `sh` (ver Configuración).
3.  Abre dos terminales en el directorio raíz del proyecto.
4.  **Terminal 1: Iniciar el Servidor**
    ```bash
    chmod +x run_server.sh
    ./run_server.sh
    ```
    Deberías ver logs indicando que el servidor está escuchando en los puertos gRPC y externo.
5.  **Terminal 2: Iniciar el Agente**
    ```bash
    chmod +x run_client.sh
    ./run_client.sh
    ```
    Deberías ver logs indicando que el agente se está conectando al servidor gRPC y está listo. El servidor también debería loguear la conexión del agente.
6.  **Conectar el Cliente Final (Ej. Cliente Firebird)**
    * Configura tu aplicación cliente (DBeaver, FlameRobin, etc.) para conectarse a la dirección IP/DNS del **Servidor del Túnel** y al puerto **`TUNNEL_EXTERNAL_PORT`** (ej. `127.0.0.1:8080` si corres todo localmente).
    * La conexión debería establecerse, y el tráfico será redirigido a través del túnel hacia el servidor Firebird real especificado en `FB_ADDRESS`.

## Protocolo gRPC (`proto/tunnel.proto`)

*(Asegúrate de tener un archivo `.proto` en esa ruta o ajusta la ruta/nombre)*

La comunicación entre el servidor y el agente se define usando Protocol Buffers.

* **Servicio:** `Tunnel`
* **Método:** `ConnectControl (stream Frame) returns (stream Frame)`
    * Un stream bidireccional donde el cliente (agente) inicia la conexión.
    * Se utiliza para control (autenticación inicial implícita por conexión) y para multiplexar múltiples túneles de datos.
* **Mensaje:** `Frame`
    * `Type`: Enum (ej. `START_DATA_TUNNEL`, `DATA`, `CLOSE_DATA_TUNNEL`) para indicar el propósito del frame.
    * `ConnectionId`: String o Int64 para identificar a qué conexión de cliente final pertenece este frame de datos (permite multiplexación).
    * `Payload`: `bytes` que contienen el chunk de datos TCP reales que se están tunelizando.
    * `Error`: String opcional para comunicar errores.

## Seguridad

* **TLS:** La comunicación gRPC entre el servidor y el agente está encriptada usando TLS. Este README asume el uso de certificados autofirmados, lo cual es seguro si se generan y manejan correctamente (especialmente asegurando que el agente confíe en el CA correcto, que aquí es `server.crt`).
    * **Advertencia:** **Nunca** dejes `DANGEROUS_SKIP_VERIFY="true"` en `run_client.sh` en un entorno real. Deshabilita la validación del certificado y expone la conexión a ataques Man-in-the-Middle. Si la conexión falla sin esto, arregla la configuración del certificado.
* **Token:** Se usa un token simple (`TUNNEL_AUTH_TOKEN`) para autenticar al agente ante el servidor. Asegúrate de que sea secreto y suficientemente complejo.
* **Alcance del Cifrado:** Es importante entender que **este túnel NO proporciona cifrado de extremo a extremo** desde la aplicación cliente original hasta el servidor Firebird final.
    * Cliente App <-> Servidor Túnel: **NO CIFRADO** (TCP simple).
    * Servidor Túnel <-> Agente Túnel: **CIFRADO** (gRPC/TLS).
    * Agente Túnel <-> Servidor Firebird: **NO CIFRADO** (TCP simple).
    El cifrado protege el segmento que viaja a través de la red potencialmente insegura entre el servidor y el agente.

## Posibles Mejoras y Futuro Trabajo

* **Compresión gRPC:** Añadir compresión (ej. gzip) al stream gRPC para reducir el uso de ancho de banda (requiere benchmarking para ver el impacto real en CPU/latencia).
* **Health Checks gRPC:** Implementar el servicio de Health Checking estándar de gRPC en el servidor.
* **Reflexión gRPC:** Habilitar la reflexión en el servidor para facilitar el uso de herramientas como `grpcurl` y `grpcui`.
* **Interceptors:** Añadir interceptores para logging estructurado, métricas (Prometheus), tracing distribuido (OpenTelemetry).
* **Gestión de Agentes:** Permitir y gestionar múltiples agentes concurrentes.
* **Configuración por Archivo:** Usar archivos de configuración (ej. YAML, TOML) además/en lugar de variables de entorno.
* **TLS en Extremos:** Añadir opción para que el servidor escuche conexiones de clientes finales con TLS y/o que el agente conecte al servicio final (Firebird) usando TLS (si el servicio lo soporta).


## Levantar firebird en docker para probar los scripts 

    ```bash
    docker run -d --name firebird25ss -p 30505:3050 -d -e ISC_PASSWORD=masterkey jacobalberty/firebird
    docker exec -it firebird25ss /bin/bash
    cd /usr/local/firebird/bin/
    ./isql -u SYSDBA -p masterkey /firebird/data/mi_base.fdb
    CREATE DATABASE '/firebird/data/mi_base.fdb' USER 'SYSDBA' PASSWORD 'masterkey';
    exit;
    quit;
    ```

  ```
Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned
   ```