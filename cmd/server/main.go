package main

import (
	"context"
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
	_ "google.golang.org/grpc/encoding/gzip" // Registra el compresor gzip
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/arturoeanton/go-grpc-tunnel/proto" // Cambia esto por la ruta correcta de tu paquete)
	"github.com/google/uuid"
)

var (
	firebirdListenPort = os.Getenv("TUNNEL_EXTERNAL_PORT") // Puerto donde escuchará para conexiones Firebird (cliente)
	grpcListenPort     = os.Getenv("TUNNEL_GRPC_PORT")
	authTokenEnv       = "TUNNEL_AUTH_TOKEN"  // Token de autenticación para el agente
	serverCertEnv      = "TUNNEL_SERVER_CERT" // Certificado TLS del servidor
	serverKeyEnv       = "TUNNEL_SERVER_KEY"  // Clave TLS del servidor
)

// tunnelServer implementa la interfaz TunnelServiceServer
type tunnelServer struct {
	pb.UnimplementedTunnelServiceServer
	mu            sync.Mutex
	agentStream   pb.TunnelService_ConnectControlServer // Stream de control con el agente
	activeTunnels map[string]*tunnelInfo                // Mapea connection_id a información del túnel
	authToken     string
}

// tunnelInfo almacena información sobre un túnel de datos activo
type tunnelInfo struct {
	firebirdConn net.Conn // Conexión TCP con el cliente Firebird remoto
	ctx          context.Context
	cancel       context.CancelFunc
	ready        chan struct{} // Señaliza cuando el agente confirma que el túnel TCP a FB está listo
}

// newTunnelServer crea una nueva instancia del servidor
func newTunnelServer(token string) *tunnelServer {
	return &tunnelServer{
		activeTunnels: make(map[string]*tunnelInfo),
		authToken:     token,
	}
}

// authenticate valida el token del agente gRPC
func (s *tunnelServer) authenticate(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "metadata is not provided")
	}

	values := md.Get("authorization") // Espera "Bearer <token>"
	if len(values) == 0 {
		return status.Error(codes.Unauthenticated, "authorization token is not provided")
	}

	// Simplificado: espera el token directamente o "Bearer <token>"
	token := values[0]
	expectedPrefix := "Bearer "
	if len(token) > len(expectedPrefix) && token[:len(expectedPrefix)] == expectedPrefix {
		token = token[len(expectedPrefix):]
	}

	if token != s.authToken {
		log.Printf("Authentication failed: invalid token received.")
		return status.Error(codes.Unauthenticated, "invalid token")
	}

	log.Println("Agent authenticated successfully.")
	return nil
}

// ConnectControl maneja la conexión de control del agente y los streams de datos
func (s *tunnelServer) ConnectControl(stream pb.TunnelService_ConnectControlServer) error {
	log.Println("Agent attempting to connect...")

	// 1. Autenticar al agente
	if err := s.authenticate(stream.Context()); err != nil {
		return err
	}

	// 2. Almacenar el stream del agente (solo uno permitido por simplicidad)
	s.mu.Lock()
	if s.agentStream != nil {
		s.mu.Unlock()
		log.Println("Agent connection rejected: another agent is already connected.")
		return status.Error(codes.AlreadyExists, "agent already connected")
	}
	s.agentStream = stream
	log.Println("Agent control stream established.")
	s.mu.Unlock()

	// Limpieza cuando el agente se desconecta
	defer func() {
		s.mu.Lock()
		log.Println("Agent control stream closed.")
		s.agentStream = nil
		// Cerrar todos los túneles asociados a este agente
		for connID, tunnel := range s.activeTunnels {
			log.Printf("Closing tunnel %s due to agent disconnect.", connID)
			tunnel.cancel() // Cancela el contexto del túnel
			if tunnel.firebirdConn != nil {
				tunnel.firebirdConn.Close()
			}
			delete(s.activeTunnels, connID)
		}
		s.mu.Unlock()
	}()

	// 3. Bucle para recibir mensajes del agente (TUNNEL_READY, DATA, CLOSE_TUNNEL, ERROR)
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			log.Println("Agent stream closed by client (EOF).")
			return nil // Cliente cerró limpiamente
		}
		if err != nil {
			log.Printf("Error receiving frame from agent: %v", err)
			// Determinar si el error es por cancelación del contexto o un error real
			if status.Code(err) == codes.Canceled || status.Code(err) == codes.Unavailable {
				log.Println("Agent stream likely canceled or unavailable.")
				return nil // Contexto cancelado o conexión perdida
			}
			return status.Errorf(codes.Internal, "failed to receive from agent: %v", err)
		}

		// Procesar frame recibido del agente
		s.handleAgentFrame(frame)
	}
}

// handleAgentFrame procesa los mensajes recibidos del agente
func (s *tunnelServer) handleAgentFrame(frame *pb.Frame) {
	s.mu.Lock()
	tunnel, exists := s.activeTunnels[frame.ConnectionId]
	s.mu.Unlock()

	if !exists && frame.Type != pb.FrameType_TUNNEL_READY { // TUNNEL_READY puede llegar antes de que el mapa esté listo si hay races
		log.Printf("Received frame for unknown or closed tunnel ID: %s, Type: %s", frame.ConnectionId, frame.Type)
		// Podríamos enviar un CLOSE_TUNNEL de vuelta si el agente no sabe que se cerró
		return
	}

	switch frame.Type {
	case pb.FrameType_TUNNEL_READY:
		log.Printf("Tunnel %s ready signal received from agent.", frame.ConnectionId)
		s.mu.Lock()
		tunnel, exists = s.activeTunnels[frame.ConnectionId] // Re-check con lock
		if exists && tunnel.ready != nil {
			close(tunnel.ready) // Señaliza que el túnel está listo
			tunnel.ready = nil  // Evita cerrar canal cerrado
		} else {
			log.Printf("Received TUNNEL_READY for unknown or already processed tunnel: %s", frame.ConnectionId)
		}
		s.mu.Unlock()

	case pb.FrameType_DATA:
		if tunnel == nil || tunnel.firebirdConn == nil {
			log.Printf("Received DATA for tunnel %s but Firebird connection is nil.", frame.ConnectionId)
			return
		}
		// Escribir datos al cliente Firebird remoto
		_, err := tunnel.firebirdConn.Write(frame.Payload)
		if err != nil {
			log.Printf("Error writing to Firebird client for tunnel %s: %v. Closing tunnel.", frame.ConnectionId, err)
			s.closeTunnel(frame.ConnectionId, "Error writing to remote client") // Cierra el túnel
		}

	case pb.FrameType_CLOSE_TUNNEL:
		log.Printf("Received CLOSE_TUNNEL for %s from agent. Reason: %s", frame.ConnectionId, frame.Metadata["error"])
		s.closeTunnel(frame.ConnectionId, "Closed by agent") // Cierra el túnel del lado del servidor

	case pb.FrameType_ERROR:
		log.Printf("Received ERROR for tunnel %s from agent: %s", frame.ConnectionId, frame.Metadata["message"])
		s.closeTunnel(frame.ConnectionId, "Error reported by agent") // Cierra el túnel

	default:
		log.Printf("Received unhandled frame type %s from agent for tunnel %s.", frame.Type, frame.ConnectionId)
	}
}

// sendFrameToAgent envía un frame al agente a través del stream de control
func (s *tunnelServer) sendFrameToAgent(frame *pb.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agentStream == nil {
		return fmt.Errorf("agent stream is not available")
	}
	err := s.agentStream.Send(frame)
	if err != nil {
		log.Printf("Error sending frame to agent (type %s, conn %s): %v", frame.Type, frame.ConnectionId, err)
		// Aquí podríamos intentar cerrar el túnel específico si aplica
		if frame.ConnectionId != "" {
			// Cuidado con deadlock si closeTunnel intenta enviar también
			go s.closeTunnel(frame.ConnectionId, "Failed to send to agent")
		}
	}
	return err
}

// closeTunnel cierra un túnel específico (conexión TCP y notifica al agente)
func (s *tunnelServer) closeTunnel(connID string, reason string) {
	s.mu.Lock()
	tunnel, exists := s.activeTunnels[connID]
	if !exists {
		s.mu.Unlock()
		return // Ya cerrado o nunca existió
	}
	delete(s.activeTunnels, connID)
	s.mu.Unlock()

	log.Printf("Closing tunnel %s. Reason: %s", connID, reason)

	// Cancelar el contexto del túnel (detiene goroutines asociadas)
	if tunnel.cancel != nil {
		tunnel.cancel()
	}

	// Cerrar la conexión TCP con el cliente Firebird remoto
	if tunnel.firebirdConn != nil {
		tunnel.firebirdConn.Close()
	}

	// Notificar al agente para que cierre su lado (si el agente no fue quien inició el cierre)
	// Evita enviar CLOSE si la razón fue "Closed by agent" o similar
	if reason != "Closed by agent" && reason != "Failed to send to agent" {
		err := s.sendFrameToAgent(&pb.Frame{
			Type:         pb.FrameType_CLOSE_TUNNEL,
			ConnectionId: connID,
			Metadata:     map[string]string{"reason": reason},
		})
		if err != nil {
			log.Printf("Failed to send CLOSE_TUNNEL notification to agent for %s: %v", connID, err)
		}
	}
}

// startFirebirdListener escucha conexiones entrantes de clientes Firebird remotos
func (s *tunnelServer) startFirebirdListener(listenAddr string) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()
	log.Printf("Listening for Firebird clients on %s", listenAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept Firebird connection: %v", err)
			continue
		}
		log.Printf("Accepted Firebird connection from %s", conn.RemoteAddr())
		go s.handleFirebirdConnection(conn)
	}
}

// handleFirebirdConnection maneja una nueva conexión de un cliente Firebird remoto
func (s *tunnelServer) handleFirebirdConnection(fbConn net.Conn) {
	connID := uuid.New().String() // Genera ID único para el túnel
	log.Printf("Handling new Firebird connection, assigned Tunnel ID: %s", connID)

	// Crear contexto para este túnel específico
	tunnelCtx, cancel := context.WithCancel(context.Background())

	tunnel := &tunnelInfo{
		firebirdConn: fbConn,
		ctx:          tunnelCtx,
		cancel:       cancel,
		ready:        make(chan struct{}), // Canal para esperar la confirmación del agente
	}

	s.mu.Lock()
	// Verificar si el agente está conectado ANTES de agregar el túnel
	if s.agentStream == nil {
		s.mu.Unlock()
		log.Printf("Rejecting Firebird connection %s: Agent not connected.", connID)
		fbConn.Close()
		return
	}
	s.activeTunnels[connID] = tunnel
	s.mu.Unlock()

	// Asegurar limpieza si algo falla aquí o la goroutine termina
	defer s.closeTunnel(connID, "Handler exit")

	// 1. Notificar al agente para que inicie el túnel TCP hacia Firebird DB
	log.Printf("Requesting agent to start data tunnel for %s", connID)
	err := s.sendFrameToAgent(&pb.Frame{
		Type:         pb.FrameType_START_DATA_TUNNEL,
		ConnectionId: connID,
		// Podríamos pasar metadata adicional si fuera necesario (ej. IP origen)
	})
	if err != nil {
		log.Printf("Failed to send START_DATA_TUNNEL to agent for %s: %v", connID, err)
		// closeTunnel se llamará en el defer
		return
	}

	// 2. Esperar a que el agente confirme que la conexión a Firebird DB está lista
	log.Printf("Waiting for TUNNEL_READY signal for %s...", connID)
	select {
	case <-tunnel.ready:
		log.Printf("Tunnel %s ready. Starting data proxy.", connID)
	case <-time.After(15 * time.Second): // Timeout esperando al agente
		log.Printf("Timeout waiting for TUNNEL_READY for %s. Closing.", connID)
		return // closeTunnel se llamará en el defer
	case <-tunnelCtx.Done(): // Contexto cancelado (ej. agente se desconectó mientras esperábamos)
		log.Printf("Tunnel context canceled while waiting for TUNNEL_READY for %s.", connID)
		return // closeTunnel se llamará en el defer
	case <-s.agentStream.Context().Done(): // El stream del agente se cerró
		log.Printf("Agent stream context done while waiting for TUNNEL_READY for %s.", connID)
		return // closeTunnel se llamará en el defer
	}

	// 3. Iniciar proxy bidireccional entre fbConn y el stream gRPC (a través del agente)
	log.Printf("Starting bidirectional proxy for tunnel %s", connID)
	wg := sync.WaitGroup{}
	wg.Add(1) // Solo necesitamos una goroutine aquí: leer del cliente FB y enviar al agente

	// Goroutine: Leer del cliente Firebird -> Enviar al Agente vía gRPC
	go func() {
		defer wg.Done()
		defer log.Printf("Stopped reading from Firebird client for tunnel %s", connID)
		buffer := make([]byte, 32*1024) // Buffer de lectura
		for {
			select {
			case <-tunnelCtx.Done(): // Detectar cancelación
				return
			default:
				n, err := fbConn.Read(buffer)
				if err != nil {
					if err != io.EOF && !isNetworkCloseError(err) {
						log.Printf("Error reading from Firebird client for tunnel %s: %v", connID, err)
					} else {
						log.Printf("Firebird client %s closed connection (EOF or closed).", connID)
					}
					s.closeTunnel(connID, "Read error/EOF from Firebird client")
					return // Termina la goroutine
				}
				if n > 0 {
					// Enviar los datos al agente
					sendErr := s.sendFrameToAgent(&pb.Frame{
						Type:         pb.FrameType_DATA,
						ConnectionId: connID,
						Payload:      buffer[:n],
					})
					if sendErr != nil {
						log.Printf("Error sending DATA frame to agent for tunnel %s: %v", connID, sendErr)
						// El error de envío probablemente significa que el agente se cayó
						// closeTunnel se encargará de limpiar todo
						s.closeTunnel(connID, "Failed to send data to agent")
						return // Termina la goroutine
					}
				}
			}
		}
	}()

	// Esperar a que la goroutine de lectura termine (lo que implica que el túnel debe cerrarse)
	wg.Wait()
	log.Printf("Proxy goroutine finished for tunnel %s.", connID)
	// closeTunnel se llama en el defer principal de handleFirebirdConnection
}

// isNetworkCloseError verifica si un error es típico de una conexión cerrada
func isNetworkCloseError(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		return opErr.Err.Error() == "use of closed network connection"
	}
	// Podríamos añadir más chequeos específicos de OS si fuera necesario
	return err == io.EOF || err == net.ErrClosed
}

func main() {
	log.Println("Starting gRPC Tunnel Server...")

	// Leer configuración de variables de entorno
	authToken := os.Getenv(authTokenEnv)
	if authToken == "" {
		log.Fatalf("Error: Environment variable %s is required.", authTokenEnv)
	}
	serverCert := os.Getenv(serverCertEnv)
	serverKey := os.Getenv(serverKeyEnv)
	if serverCert == "" || serverKey == "" {
		log.Fatalf("Error: Environment variables %s and %s are required for TLS.", serverCertEnv, serverKeyEnv)
	}

	var grpcServer *grpc.Server
	if os.Getenv("TUNNEL_INSECURE") == "true" {
		grpcServer = grpc.NewServer() // Sin TLS
		log.Println("gRPC server running in insecure mode (no TLS).")
	} else {
		// Configurar TLS para gRPC
		creds, err := credentials.NewServerTLSFromFile(serverCert, serverKey)
		if err != nil {
			log.Fatalf("Failed to load TLS credentials: %v", err)
		}
		// Crear servidor gRPC
		grpcServer = grpc.NewServer(grpc.Creds(creds)) // TLS Desactivado */
		log.Println("gRPC server running with TLS.")
	}

	// Crear e registrar la implementación del servicio
	tunnelSrv := newTunnelServer(authToken)
	pb.RegisterTunnelServiceServer(grpcServer, tunnelSrv)

	// Iniciar listener gRPC en goroutine
	go func() {
		grpcListener, err := net.Listen("tcp", grpcListenPort)
		if err != nil {
			log.Fatalf("Failed to listen for gRPC on %s: %v", grpcListenPort, err)
		}
		defer grpcListener.Close()
		log.Printf("gRPC server listening on %s ", grpcListenPort)
		if err := grpcServer.Serve(grpcListener); err != nil {
			log.Fatalf("Failed to serve gRPC: %v", err)
		}
	}()

	// Iniciar listener para clientes Firebird (bloqueante)
	tunnelSrv.startFirebirdListener(firebirdListenPort)

	// Esperar señal de parada (opcional, para cierre elegante)
	// ...
	log.Println("Server shutting down.")

}
