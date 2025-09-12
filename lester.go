package main

import (
	"context"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	pb "example/example_goproto/pb"

	"github.com/streadway/amqp"
	"google.golang.org/grpc"
)

const (
	grpcAddr = ":50051" // lester gRPC
	amqpURL  = "amqp://guest:guest@localhost:5672/"
	exchange = "stars.exchange"
	turnMs   = 100 // 1 "turno" = 100ms; ajusta si quieres
)

type lesterServer struct {
	pb.UnimplementedLesterServer
	mu                   sync.Mutex
	rejects              map[string]int
	cooldown             map[string]time.Time
	running              map[pb.Character]context.CancelFunc
	amqp                 *amqp.Connection
	lastOffer            *pb.Oferta // guarda la última oferta enviada a Michael
	rechazosConsecutivos int
}

func newLesterServer() *lesterServer {
	return &lesterServer{
		running: make(map[pb.Character]context.CancelFunc),
	}
}

func (s *lesterServer) StartHeistNotifications(ctx context.Context, req *pb.StartHeistNotificationsRequest) (*pb.Ack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// asegurar conexión AMQP
	if s.amqp == nil || s.amqp.IsClosed() {
		conn, err := amqp.Dial(amqpURL)
		if err != nil {
			return &pb.Ack{Ok: false, Msg: "Error AMQP: " + err.Error()}, nil
		}
		s.amqp = conn
	}

	// declarar exchange si no existe
	ch, err := s.amqp.Channel()
	if err != nil {
		return &pb.Ack{Ok: false, Msg: "Error abrir canal: " + err.Error()}, nil
	}
	defer ch.Close()
	if err := ch.ExchangeDeclare(exchange, "direct", true, false, false, false, nil); err != nil {
		return &pb.Ack{Ok: false, Msg: "Error declare exchange: " + err.Error()}, nil
	}

	log.Printf("[Lester] Preparado para enviar estrellas a %s (riesgo=%d)", routingKey(req.Character), req.PoliceRisk)

	return &pb.Ack{Ok: true, Msg: "Notificaciones preparadas"}, nil
}

func (s *lesterServer) StopHeistNotifications(ctx context.Context, r *pb.StopHeistNotificationsRequest) (*pb.Ack, error) {
	s.mu.Lock()
	s.mu.Unlock()
	return &pb.Ack{Ok: true, Msg: "stop ok"}, nil
}

func routingKey(c pb.Character) string {
	if c == pb.Character_FRANKLIN {
		return "stars.franklin"
	}
	return "stars.trevor"
}

func (s *lesterServer) PedirOferta(ctx context.Context, req *pb.PedirOfertaSolicitud) (*pb.PedirOfertaRespuesta, error) {
	clientID := req.GetClientId()

	// Respeta la "espera de 10s" tras 3 rechazos consecutivos
	s.mu.Lock()
	if until, ok := s.cooldown[clientID]; ok {
		now := time.Now()
		if now.Before(until) {
			wait := until.Sub(now)
			s.mu.Unlock()
			time.Sleep(wait)
			s.mu.Lock()
		}
		delete(s.cooldown, clientID)
	}
	s.mu.Unlock()

	// 90% de probabilidad de tener una oferta
	if rand.Float64() > 0.9 {
		return &pb.PedirOfertaRespuesta{
			Available: false,
			Message:   "No hay oferta disponible ahora. Intenta de nuevo pronto.",
		}, nil
	}

	// Genera una oferta aleatoria simple para la POC local
	offer := &pb.Oferta{
		Loot:            int32(100000 + rand.Intn(900000)), // $100k - $1M
		SuccessFranklin: int32(rand.Intn(101)),             // 0 - 100
		SuccessTrevor:   int32(rand.Intn(101)),             // 0 - 100
		PoliceRisk:      int32(rand.Intn(101)),             // 0 - 100
	}

	return &pb.PedirOfertaRespuesta{
		Available: true,
		Offer:     offer,
		Message:   "Oferta lista 😎",
	}, nil
}

func (s *lesterServer) NotificarDecision(ctx context.Context, req *pb.SolicitudDecision) (*pb.ConfirmacionDecision, error) {
	clientID := req.GetClientId()

	// anti-panic
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Lester] panic en NotificarDecision: %v", r)
		}
	}()

	s.mu.Lock()
	defer s.mu.Unlock()

	if req.GetDecision() == pb.RespuestaDecision_REJECT {
		s.rejects[clientID]++
		count := s.rejects[clientID]
		log.Printf("[Lester] Rechazo de %s (#%d)", clientID, count)

		if count >= 3 {
			s.cooldown[clientID] = time.Now().Add(10 * time.Second)
			s.rejects[clientID] = 0 // resetea el contador tras aplicar castigo
			return &pb.ConfirmacionDecision{
				Message:            "Tres rechazos consecutivos. Espera 10s antes de una nueva oferta.",
				ConsecutiveRejects: 0,
			}, nil
		}

		return &pb.ConfirmacionDecision{
			Message:            "Rechazo registrado.",
			ConsecutiveRejects: int32(count),
		}, nil
	}

	// Aceptada
	s.rejects[clientID] = 0
	log.Printf("[Lester] %s aceptó la oferta. ✍️", clientID)
	return &pb.ConfirmacionDecision{
		Message:            "Oferta aceptada. ¡Que comience el plan!",
		ConsecutiveRejects: 0,
	}, nil
}

func main() {
	rand.Seed(time.Now().UnixNano())

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("no se pudo abrir puerto: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterLesterServer(grpcServer, newLesterServer())

	log.Println("Lester server escuchando en :50051")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("falló Serve: %v", err)
	}
}
