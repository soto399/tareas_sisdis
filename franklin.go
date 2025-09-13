package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"time"

	pb "example/example_goproto/pb"

	"github.com/streadway/amqp"
	"google.golang.org/grpc"
)

const (
	grpcAddr = ":50053"
	amqpURL  = "amqp://guest:guest@localhost:5672/"
	exchange = "stars.exchange"
	queue    = "stars.franklin.q"
	rk       = "stars.franklin"
	turnMs   = 200
)

type server struct {
	pb.UnimplementedServicioDistraccionesServer
	pb.UnimplementedCharacterServiceServer

	mu         sync.Mutex
	state      pb.MissionState
	stars      int
	turnsDone  int
	totalTurns int
	earnedLoot int64
	detail     string
	running    context.CancelFunc
	startOnce  sync.Once

	// params
	successProb int
	baseLoot    int64

	// bonus
	chopActive bool
}

func (s *server) StartHeist(ctx context.Context, req *pb.StartHeistRequest) (*pb.Ack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state == pb.MissionState_STATE_IN_PROGRESS {
		return &pb.Ack{Ok: true, Msg: "Ya en progreso"}, nil
	}

	s.state = pb.MissionState_STATE_IN_PROGRESS
	s.stars = 0
	s.turnsDone = 0
	s.totalTurns = 200 - int(req.SuccessProb)
	if s.totalTurns < 1 {
		s.totalTurns = 1
	}
	s.earnedLoot = 0
	s.detail = "Golpe en curso"
	s.successProb = int(req.SuccessProb)
	s.baseLoot = req.BaseLoot
	s.chopActive = false

	ctxRun, cancel := context.WithCancel(context.Background())
	s.running = cancel

	go s.run(ctxRun)

	return &pb.Ack{Ok: true, Msg: "Franklin: golpe iniciado"}, nil
}

func (s *server) run(ctx context.Context) {
	// AMQP setup
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		s.fail("AMQP dial error")
		return
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		s.fail("AMQP channel error")
		return
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(exchange, "direct", true, false, false, false, nil); err != nil {
		s.fail("AMQP exchange declare error")
		return
	}

	q, err := ch.QueueDeclare(queue, true, false, false, false, nil)
	if err != nil {
		s.fail("AMQP queue declare error")
		return
	}
	if err := ch.QueueBind(q.Name, rk, exchange, false, nil); err != nil {
		s.fail("AMQP bind error")
		return
	}

	msgs, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	if err != nil {
		s.fail("AMQP consume error")
		return
	}

	// lector de estrellas
	go func() {
		for m := range msgs {
			val, _ := strconv.Atoi(string(m.Body))
			s.mu.Lock()
			s.stars = val
			// habilidad: Chop activa a 3 estrellas
			if s.stars >= 3 {
				s.chopActive = true
			}
			s.mu.Unlock()
		}
	}()

	// loop de trabajo por turnos
	limit := 5 // Franklin falla a 5 estrellas
	ticker := time.NewTicker(time.Duration(turnMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.state != pb.MissionState_STATE_IN_PROGRESS {
				s.mu.Unlock()
				return
			}
			// chequeo de fracaso
			if s.stars >= limit {
				s.state = pb.MissionState_STATE_FAILED
				s.detail = "Demasiadas estrellas"
				s.mu.Unlock()
				return
			}
			// completa un turno
			s.turnsDone++
			if s.chopActive {
				s.earnedLoot += 1000
			}
			if s.turnsDone >= s.totalTurns {
				s.state = pb.MissionState_STATE_SUCCESS
				s.detail = "Golpe completado"
				s.earnedLoot += s.baseLoot
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
		}
	}
}

func (s *server) GetStatus(ctx context.Context, _ *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.GetStatusResponse{
		State:      s.state,
		Stars:      int32(s.stars),
		TurnsDone:  int32(s.turnsDone),
		TotalTurns: int32(s.totalTurns),
		EarnedLoot: s.earnedLoot,
		Detail:     s.detail,
	}, nil
}

func (s *server) fail(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pb.MissionState_STATE_FAILED
	s.detail = reason
}

func (s *server) EmpezarDistraccion(ctx context.Context, req *pb.SolicitudDistraccion) (*pb.ResultadoDistraccion, error) {
	log.Printf("Iniciando distracción para el personaje: Franklin\n")

	// Calculamos la cantidad de turnos necesarios
	turnos := 200 - int(req.ProbabilidadDeExito)
	log.Printf("Total de turnos para Franklin: %d\n", turnos)

	var success bool = true
	var reason string = "Distracción completada con éxito"

	// Mitad de los turnos para evaluar el riesgo del 10%
	mitad := turnos / 2

	for i := 1; i <= turnos; i++ {
		// Si estamos en la mitad, chequeamos el evento aleatorio
		if i == mitad {
			if rand.Intn(100) < 10 { // 10% de probabilidad
				success = false
				reason = "Chop comenzo a ladrar y distrajo a Franklin, distracción fallida"
				log.Printf("Turno %d: %s\n", i, reason)
				break // Terminamos la misión
			}
			log.Printf("Turno %d: Chop esta tranquilo, continuamos...\n", i)
		}

		log.Printf("Turno %d completado exitosamente", i)
	}

	// Si falló antes de completar todos los turnos, mostramos razón
	if !success {
		log.Printf("La distracción de %s ha fallado.\n", req.Character)
	} else {
		log.Printf("La distracción de %s ha sido exitosa.\n", req.Character)
	}

	result := &pb.ResultadoDistraccion{
		Success: success,
		Reason:  reason,
	}

	return result, nil
}

// GetLootTotal: devuelve earned_loot si STATE_SUCCESS, si no 0
func (s *server) GetLootTotal(ctx context.Context, _ *pb.GetLootTotalRequest) (*pb.GetLootTotalResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == pb.MissionState_STATE_SUCCESS {
		return &pb.GetLootTotalResponse{TotalLoot: s.earnedLoot}, nil
	}
	return &pb.GetLootTotalResponse{TotalLoot: 0}, nil
}

// ReceivePayment: verifica que amount == total/4 (su parte equitativa)
func (s *server) ReceivePayment(ctx context.Context, p *pb.Payment) (*pb.PaymentAck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Para verificación, usamos el loot final que entregó este personaje (s.earnedLoot)
	if s.earnedLoot <= 0 || s.state != pb.MissionState_STATE_SUCCESS {
		return &pb.PaymentAck{Ok: false, Msg: "No corresponde pago (sin éxito)"}, nil
	}
	share := s.earnedLoot / 4
	if p.Amount == share {
		return &pb.PaymentAck{Ok: true, Msg: "Excelente! El pago es correcto."}, nil
	}
	return &pb.PaymentAck{Ok: false, Msg: fmt.Sprintf("Pago incorrecto. Esperaba $%d y recibí $%d", share, p.Amount)}, nil
}

func main() {
	rand.Seed(time.Now().UnixNano())

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterServicioDistraccionesServer(s, &server{})
	pb.RegisterCharacterServiceServer(s, &server{state: pb.MissionState_STATE_IDLE})

	log.Println("Franklin's server ready on :50053")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
