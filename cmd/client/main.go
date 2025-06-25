package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip" // Registra el compresor gzip
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/arturoeanton/go-grpc-tunnel/proto" // Cambia esto por la ruta correcta de tu paquete)
)

var (
	serverAddrEnv   = "TUNNEL_SERVER_ADDR"  // Dirección del servidor gRPC (ej. localhost:50051)
	firebirdAddrEnv = "FIREBIRD_LOCAL_ADDR" // Dirección del Firebird local (ej. localhost:3050)
	authTokenEnv    = "TUNNEL_AUTH_TOKEN"   // Token de autenticación para el servidor
	caCertEnv       = "TUNNEL_CA_CERT"      // Ruta al certificado CA (opcional)
	insecureEnv     = "TUNNEL_INSECURE"     // Bandera para desactivar TLS (inseguro)
)

// tunnelAgent gestiona la conexión con el servidor y los túneles locales
type tunnelAgent struct {
	serverAddr   string
	firebirdAddr string
	authToken    string
	tlsConfig    *tls.Config
	insecure     bool

	conn          *grpc.ClientConn                      // Conexión gRPC persistente
	controlStream pb.TunnelService_ConnectControlClient // Stream de control/datos
	mu            sync.Mutex
	activeConns   map[string]*localTunnelInfo // Conexiones locales a Firebird (connID -> info)
	wg            sync.WaitGroup              // Para esperar a las goroutines de proxy
	cancelCtx     context.Context             // Contexto principal del agente
	cancelFunc    context.CancelFunc          // Función para cancelar el contexto
}

// localTunnelInfo almacena información sobre una conexión local a Firebird
type localTunnelInfo struct {
	fbConn net.Conn
	ctx    context.Context
	cancel context.CancelFunc
}

func main() {
	log.Println("Starting gRPC Tunnel Agent...")

	// Leer configuración
	serverAddr := os.Getenv(serverAddrEnv)
	firebirdAddr := os.Getenv(firebirdAddrEnv)
	authToken := os.Getenv(authTokenEnv)
	caCertPath := os.Getenv(caCertEnv)
	insecureFlag := os.Getenv(insecureEnv) == "true"

	if serverAddr == "" || firebirdAddr == "" || authToken == "" {
		log.Fatalf("Error: Environment variables %s, %s, and %s are required.", serverAddrEnv, firebirdAddrEnv, authTokenEnv)
	}

	// Configurar TLS
	var tlsConfig *tls.Config
	if !insecureFlag {
		var err error
		log.Println("Loading TLS configuration...")
		log.Printf("CA Certificate Path: %s", caCertPath)
		tlsConfig, err = loadClientTLSConfig(caCertPath)
		if err != nil {
			log.Fatalf("Failed to load TLS config: %v", err)
		}
		log.Println("TLS enabled.")
	} else {
		log.Println("Warning: Running in insecure mode (TLS disabled).")
	}

	// Crear contexto principal cancelable
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Asegura que se llame al salir

	agent := &tunnelAgent{
		serverAddr:   serverAddr,
		firebirdAddr: firebirdAddr,
		authToken:    authToken,
		tlsConfig:    tlsConfig,
		insecure:     insecureFlag,
		activeConns:  make(map[string]*localTunnelInfo),
		cancelCtx:    ctx,
		cancelFunc:   cancel,
	}

	// Bucle principal para mantener la conexión y reconectar
	agent.runControlLoop()

	log.Println("Agent shut down.")
}

// loadClientTLSConfig carga la configuración TLS del cliente
func loadClientTLSConfig(caCertPath string) (*tls.Config, error) {
	config := &tls.Config{}
	if caCertPath != "" {
		caCert, err := os.ReadFile(caCertPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate %s: %w", caCertPath, err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to add CA certificate to pool")
		}
		config.RootCAs = caCertPool
		log.Printf("Loaded CA certificate from %s", caCertPath)

		// --- INICIO: CÓDIGO AÑADIDO PARA SALTAR VERIFICACIÓN (¡PELIGROSO!) ---
		// Lee una variable de entorno EXTRA para decidir si saltar la verificación.
		// ¡NUNCA DEJAR ESTO FIJO EN 'true'!
		if os.Getenv("DANGEROUS_SKIP_VERIFY") == "true" {
			log.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
			log.Println("!!! WARNING: Skipping TLS server verification    !!!")
			log.Println("!!!          (InsecureSkipVerify = true)         !!!")
			log.Println("!!!          USE ONLY FOR TEMPORARY DEBUGGING    !!!")
			log.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
			// Aquí se modifica la configuración para saltar la verificación:
			config.InsecureSkipVerify = true
		}
		// --- FIN: CÓDIGO AÑADIDO ---

	} else {
		// Si no se proporciona CA, confiará en los CAs del sistema
		// Opcionalmente, podríamos requerir siempre un CA o añadir InsecureSkipVerify (NO RECOMENDADO)
		log.Println("Warning: No custom CA certificate provided. Using system CAs.")
		// config.InsecureSkipVerify = true // ¡NO USAR EN PRODUCCIÓN!
	}
	return config, nil
}

// runControlLoop gestiona la conexión principal y el bucle de recepción
func (a *tunnelAgent) runControlLoop() {
	reconnectDelay := 1 * time.Second
	maxReconnectDelay := 30 * time.Second

	for {
		select {
		case <-a.cancelCtx.Done():
			log.Println("Agent context canceled, stopping control loop.")
			return
		default:
			// Intentar conectar
			err := a.connectAndServe()
			if a.cancelCtx.Err() != nil {
				// Si el contexto fue cancelado mientras conectábamos/servíamos, salimos.
				log.Println("Context canceled during connect/serve.")
				return
			}

			if err != nil {
				log.Printf("Connection or serve error: %v. Reconnecting in %v...", err, reconnectDelay)
				// Esperar antes de reconectar, con backoff exponencial
				time.Sleep(reconnectDelay)
				reconnectDelay *= 2
				if reconnectDelay > maxReconnectDelay {
					reconnectDelay = maxReconnectDelay
				}
			} else {
				// Si connectAndServe retorna nil (cierre limpio), esperamos un poco antes de intentar reconectar
				log.Println("Control stream closed cleanly. Reconnecting soon...")
				reconnectDelay = 1 * time.Second // Reiniciar delay
				time.Sleep(reconnectDelay)
			}
		}
	}
}

// connectAndServe establece la conexión gRPC y maneja el stream de control
func (a *tunnelAgent) connectAndServe() error {
	log.Printf("Attempting to connect to server at %s...", a.serverAddr)

	// Configurar opciones de Dial gRPC
	opts := []grpc.DialOption{grpc.WithBlock()} // Espera a que la conexión se establezca

	if a.insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		creds := credentials.NewTLS(a.tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	}

	opts = append(opts, grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name))) // Usar gzip para compresión

	// Conectar al servidor gRPC
	connCtx, connCancel := context.WithTimeout(a.cancelCtx, 15*time.Second) // Timeout para conectar
	conn, err := grpc.DialContext(connCtx, a.serverAddr, opts...)
	connCancel() // Liberar recursos del context de conexión
	if err != nil {
		return fmt.Errorf("failed to dial server: %w", err)
	}
	defer conn.Close() // Asegura el cierre de la conexión gRPC al salir de esta función
	a.conn = conn
	log.Println("Connected to gRPC server.")

	// Crear cliente gRPC
	client := pb.NewTunnelServiceClient(conn)

	// Crear contexto para el stream con metadatos de autenticación
	md := metadata.New(map[string]string{"authorization": "Bearer " + a.authToken})
	streamCtx := metadata.NewOutgoingContext(a.cancelCtx, md) // Usar el contexto principal del agente

	// Iniciar el stream de control bidireccional
	stream, err := client.ConnectControl(streamCtx)
	if err != nil {
		// Podría ser un error de autenticación rechazado por el servidor
		return fmt.Errorf("failed to start control stream: %w", err)
	}
	a.controlStream = stream
	log.Println("Control stream established with server.")

	// Bucle para recibir instrucciones del servidor
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			log.Println("Server closed the control stream (EOF).")
			a.cleanupAgentState() // Limpiar túneles activos
			return nil            // Conexión cerrada limpiamente por el servidor
		}
		if err != nil {
			// Manejar errores específicos de gRPC
			st, ok := status.FromError(err)
			if ok {
				log.Printf("gRPC error receiving frame: Code=%s, Msg=%s", st.Code(), st.Message())
				if st.Code() == codes.Canceled || st.Code() == codes.Unavailable {
					// Contexto cancelado (ej. shutdown del agente) o conexión perdida
					a.cleanupAgentState()
					return fmt.Errorf("stream context canceled or unavailable: %w", err)
				}
			} else {
				log.Printf("Non-gRPC error receiving frame: %v", err)
			}
			a.cleanupAgentState()
			return fmt.Errorf("error receiving frame from server: %w", err)
		}

		// Procesar el frame recibido del servidor
		a.handleServerFrame(frame)
	}
}

// handleServerFrame procesa los frames recibidos del servidor gRPC
func (a *tunnelAgent) handleServerFrame(frame *pb.Frame) {
	switch frame.Type {
	case pb.FrameType_START_DATA_TUNNEL:
		log.Printf("Received request to start data tunnel for ID: %s", frame.ConnectionId)
		go a.startLocalFirebirdTunnel(frame.ConnectionId) // Iniciar en goroutine

	case pb.FrameType_DATA:
		a.mu.Lock()
		tunnelInfo, exists := a.activeConns[frame.ConnectionId]
		a.mu.Unlock()

		if exists && tunnelInfo.fbConn != nil {
			// Escribir payload en la conexión Firebird local
			_, err := tunnelInfo.fbConn.Write(frame.Payload)
			if err != nil {
				log.Printf("Error writing to local Firebird for tunnel %s: %v. Closing tunnel.", frame.ConnectionId, err)
				a.closeLocalTunnel(frame.ConnectionId, fmt.Sprintf("Write error to local FB: %v", err))
			}
		} else {
			log.Printf("Received DATA for unknown or closed tunnel ID: %s. Sending CLOSE.", frame.ConnectionId)
			// Notificar al servidor que este túnel no existe/está cerrado aquí
			a.sendFrameToServer(pb.NewCloseFrame(frame.ConnectionId, "Tunnel not found or already closed"))
		}

	case pb.FrameType_CLOSE_TUNNEL:
		reason := frame.GetOptimizedCloseReason()
		if reason == "" {
			reason = frame.Metadata["reason"] // fallback for compatibility
		}
		log.Printf("Received CLOSE_TUNNEL for %s from server. Reason: %s", frame.ConnectionId, reason)
		a.closeLocalTunnel(frame.ConnectionId, "Closed by server")

	default:
		log.Printf("Received unhandled frame type %s from server for tunnel %s.", frame.Type, frame.ConnectionId)
	}
}

// startLocalFirebirdTunnel inicia la conexión TCP a Firebird y el proxy
func (a *tunnelAgent) startLocalFirebirdTunnel(connID string) {
	// Crear contexto específico para este túnel, derivado del principal
	tunnelCtx, cancel := context.WithCancel(a.cancelCtx)

	// Asegurar limpieza al salir de esta función
	defer func() {
		cancel() // Cancela el contexto del túnel
		log.Printf("Finished handling tunnel %s", connID)
		a.mu.Lock()
		delete(a.activeConns, connID)
		a.mu.Unlock()
	}()

	// 1. Conectar a Firebird local
	log.Printf("Tunnel %s: Connecting to local Firebird at %s...", connID, a.firebirdAddr)
	dialer := net.Dialer{Timeout: 10 * time.Second}
	fbConn, err := dialer.DialContext(tunnelCtx, "tcp", a.firebirdAddr)
	if err != nil {
		log.Printf("Tunnel %s: Failed to connect to local Firebird: %v", connID, err)
		// Notificar al servidor sobre el error
		a.sendFrameToServer(pb.NewErrorFrame(connID, fmt.Sprintf("Failed to connect to local Firebird: %v", err)))
		a.sendFrameToServer(pb.NewCloseFrame(connID, "Failed to connect to local Firebird"))
		return
	}
	log.Printf("Tunnel %s: Connected to local Firebird.", connID)
	defer fbConn.Close() // Asegura que se cierre la conexión local

	// Almacenar información del túnel local
	localInfo := &localTunnelInfo{
		fbConn: fbConn,
		ctx:    tunnelCtx,
		cancel: cancel,
	}
	a.mu.Lock()
	// Verificar si el agente todavía está conectado y si el túnel no fue cerrado mientras conectábamos
	if a.controlStream == nil || a.cancelCtx.Err() != nil {
		a.mu.Unlock()
		log.Printf("Tunnel %s: Agent disconnected or shutting down before tunnel ready. Aborting.", connID)
		return // Salir limpiamente
	}
	a.activeConns[connID] = localInfo
	a.mu.Unlock()

	// 2. Notificar al servidor que el túnel está listo
	log.Printf("Tunnel %s: Sending TUNNEL_READY to server.", connID)
	err = a.sendFrameToServer(pb.NewTunnelReadyFrame(connID))
	if err != nil {
		log.Printf("Tunnel %s: Failed to send TUNNEL_READY: %v. Closing tunnel.", connID, err)
		// No necesitamos notificar al servidor con CLOSE_TUNNEL aquí porque el servidor
		// probablemente ya detectó el error de envío y cerrará el túnel.
		return
	}

	// 3. Iniciar proxy bidireccional: Leer de Firebird local -> Enviar al Servidor gRPC
	log.Printf("Tunnel %s: Starting proxy: Local Firebird -> gRPC Server", connID)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer log.Printf("Tunnel %s: Stopped reading from local Firebird.", connID)
		buffer := make([]byte, 32*1024)
		for {
			select {
			case <-tunnelCtx.Done(): // Detectar cancelación del túnel
				return
			default:
				n, readErr := fbConn.Read(buffer)
				if readErr != nil {
					reason := "Read error/EOF from local Firebird"
					if readErr != io.EOF && !isNetworkCloseError(readErr) {
						log.Printf("Tunnel %s: Error reading from local Firebird: %v", connID, readErr)
						reason = fmt.Sprintf("Read error from local FB: %v", readErr)
					} else {
						log.Printf("Tunnel %s: Local Firebird connection closed (EOF or closed).", connID)
					}
					// Notificar al servidor que cerramos de este lado
					a.sendFrameToServer(pb.NewCloseFrame(connID, reason))
					cancel() // Cancela el contexto de este túnel para detener la otra goroutine (si hubiera)
					return   // Termina esta goroutine
				}
				if n > 0 {
					// Enviar datos al servidor gRPC
					sendErr := a.sendFrameToServer(pb.NewDataFrame(connID, buffer[:n]))
					if sendErr != nil {
						log.Printf("Tunnel %s: Error sending DATA frame to server: %v", connID, sendErr)
						// Si falla el envío, probablemente el stream principal cayó.
						// La cancelación del contexto principal se encargará de la limpieza.
						cancel() // Intenta cancelar este túnel específico
						return   // Termina esta goroutine
					}
				}
			}
		}
	}()

	// Esperar a que el contexto del túnel sea cancelado (indica cierre)
	<-tunnelCtx.Done()
	log.Printf("Tunnel %s: Context canceled. Proxy stopping.", connID)
	// La limpieza (cerrar fbConn, borrar de activeConns) se hace en el defer
}

// closeLocalTunnel cierra una conexión local a Firebird y cancela su contexto
func (a *tunnelAgent) closeLocalTunnel(connID string, reason string) {
	a.mu.Lock()
	tunnelInfo, exists := a.activeConns[connID]
	if exists {
		delete(a.activeConns, connID) // Borrar del mapa primero
	}
	a.mu.Unlock()

	if exists {
		log.Printf("Closing local tunnel %s. Reason: %s", connID, reason)
		if tunnelInfo.cancel != nil {
			tunnelInfo.cancel() // Cancela el contexto (detiene goroutines)
		}
		if tunnelInfo.fbConn != nil {
			tunnelInfo.fbConn.Close() // Cierra la conexión TCP
		}
		// Notificar al servidor SI el cierre no fue iniciado por el servidor
		if reason != "Closed by server" {
			err := a.sendFrameToServer(pb.NewCloseFrame(connID, reason))
			if err != nil {
				log.Printf("Failed to send CLOSE_TUNNEL notification to server for %s: %v", connID, err)
			}
		}
	} else {
		// log.Printf("Attempted to close already closed or unknown tunnel: %s", connID)
	}
}

// sendFrameToServer envía un frame al servidor a través del stream de control
func (a *tunnelAgent) sendFrameToServer(frame *pb.Frame) error {
	a.mu.Lock()
	stream := a.controlStream // Copia segura bajo lock
	a.mu.Unlock()

	if stream == nil {
		return fmt.Errorf("control stream is not available")
	}

	err := stream.Send(frame)
	if err != nil {
		log.Printf("Error sending frame to server (type %s, conn %s): %v", frame.Type, frame.ConnectionId, err)
		// Si falla el envío, podríamos asumir que la conexión principal ha caído.
		// El bucle principal de reconexión se encargará.
		// Podríamos cancelar el agente aquí si el error es irrecuperable.
		// a.cancelFunc()
	}
	return err
}

// cleanupAgentState cierra todas las conexiones locales activas cuando el agente se desconecta/detiene
func (a *tunnelAgent) cleanupAgentState() {
	log.Println("Cleaning up agent state: closing active local tunnels...")
	a.mu.Lock()
	// Copiar las keys para evitar modificar el mapa mientras iteramos
	connIDs := make([]string, 0, len(a.activeConns))
	for k := range a.activeConns {
		connIDs = append(connIDs, k)
	}
	a.mu.Unlock() // Liberar lock antes de llamar a closeLocalTunnel

	for _, connID := range connIDs {
		// No notificar al servidor, ya que él inició el cierre o la conexión se perdió
		a.closeLocalTunnel(connID, "Agent disconnecting or connection lost")
	}

	// Esperar a que todas las goroutines de proxy terminen
	a.wg.Wait()
	log.Println("All local tunnels closed.")

	// Resetear estado
	a.mu.Lock()
	a.controlStream = nil
	a.conn = nil // La conexión gRPC se cierra en el defer de connectAndServe
	a.mu.Unlock()
}

// isNetworkCloseError (misma función que en el servidor)
func isNetworkCloseError(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		return opErr.Err.Error() == "use of closed network connection"
	}
	return err == io.EOF || err == net.ErrClosed
}
