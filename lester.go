package main

import (
	"context"
	"fmt"
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
	turnMs   = 200 // 1 "turno" = 100ms; ajusta si quieres
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
		rejects:  make(map[string]int),
		cooldown: make(map[string]time.Time),
		running:  make(map[pb.Character]context.CancelFunc),
	}
}

func (s *lesterServer) StartHeistNotifications(ctx context.Context, req *pb.StartHeistNotificationsRequest) (*pb.Ack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.running[req.Character]; exists {
		return &pb.Ack{Ok: true, Msg: "Ya estaba enviando estrellas"}, nil
	}

	// asegurar AMQP
	if s.amqp == nil || s.amqp.IsClosed() {
		conn, err := amqp.Dial(amqpURL)
		if err != nil {
			return &pb.Ack{Ok: false, Msg: "Error AMQP: " + err.Error()}, nil
		}
		s.amqp = conn
	}

	ch, err := s.amqp.Channel()
	if err != nil {
		return &pb.Ack{Ok: false, Msg: "Error abrir canal: " + err.Error()}, nil
	}
	if err := ch.ExchangeDeclare(exchange, "direct", true, false, false, false, nil); err != nil {
		return &pb.Ack{Ok: false, Msg: "Error declare exchange: " + err.Error()}, nil
	}

	rk := routingKey(req.Character)
	ctxRun, cancel := context.WithCancel(context.Background())
	s.running[req.Character] = cancel

	go func(risk int32) {
		defer ch.Close()
		stars := 0

		// Calculamos el intervalo en segundos
		intervalSec := 100 - int(risk)
		if intervalSec <= 0 {
			log.Printf("[Lester] No se agregarán estrellas para %s (riesgo máximo)", rk)
			return
		}

		interval := time.Duration(float64(intervalSec) * 0.2 * float64(time.Second))
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Primera publicación inmediata
		body := fmt.Sprintf("%d", stars)
		err := ch.Publish(exchange, rk, false, false, amqp.Publishing{
			ContentType: "text/plain",
			Body:        []byte(body),
		})
		if err != nil {
			log.Printf("[Lester] publish error: %v", err)
			return
		}
		log.Printf("[Lester] -> %s: %d estrellas", rk, stars)
		stars++

		for {
			select {
			case <-ctxRun.Done():
				log.Printf("[Lester] Stop estrellas para %s", rk)
				return
			case <-ticker.C:
				// publica nueva cuenta de estrellas
				body := fmt.Sprintf("%d", stars)
				err := ch.Publish(exchange, rk, false, false, amqp.Publishing{
					ContentType: "text/plain",
					Body:        []byte(body),
				})
				if err != nil {
					log.Printf("[Lester] publish error: %v", err)
					return
				}
				log.Printf("[Lester] -> %s: %d estrellas", rk, stars)
				stars++
			}
		}
	}(req.PoliceRisk)

	return &pb.Ack{Ok: true, Msg: "Notificaciones de estrellas iniciadas"}, nil
}

func (s *lesterServer) StopHeistNotifications(ctx context.Context, req *pb.StopHeistNotificationsRequest) (*pb.Ack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cancel, ok := s.running[req.Character]
	if ok {
		cancel()
		delete(s.running, req.Character)
		return &pb.Ack{Ok: true, Msg: "Notificaciones detenidas"}, nil
	}
	return &pb.Ack{Ok: true, Msg: "No había notificaciones activas"}, nil
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

	if s.rejects[clientID] >= 3 {
		s.cooldown[clientID] = time.Now().Add(10 * time.Second)
		s.rejects[clientID] = 0 // reiniciamos después del cooldown
	}

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

func (s *lesterServer) ReceivePayment(ctx context.Context, p *pb.Payment) (*pb.PaymentAck, error) {
	// Lester no conoce el total aquí, solo valida que le llegó algo > 0.
	// Si quieres, puedes guardar el último total reportado por Michael para chequear el resto.
	if p.Amount < 0 {
		return &pb.PaymentAck{Ok: false, Msg: "Monto inválido"}, nil
	}
	if p.Amount == 0 {
		return &pb.PaymentAck{Ok: true, Msg: "Recibido $0. Un placer hacer negocios."}, nil
	}
	return &pb.PaymentAck{Ok: true, Msg: "Un placer hacer negocios."}, nil
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
