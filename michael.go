package main

import (
	"context"
	"fmt"
	"log"
	"time"

	pb "example/example_goproto/pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	lesterAddr   = "localhost:50051"
	franklinAddr = "localhost:50053"
	trevorAddr   = "localhost:50052"
	pollMs       = 300
)

func aceptaSegunReglas(o *pb.Oferta) bool {
	// Criterio de aceptación (Fase 1):
	// - al menos uno (Franklin o Trevor) > 50%
	// - riesgo policial < 80%
	okProb := o.GetSuccessFranklin() > 50 || o.GetSuccessTrevor() > 50
	okRiesgo := o.GetPoliceRisk() < 80
	return okProb && okRiesgo
}

func startDistraction(client pb.ServicioDistraccionesClient, character string, probabilidadDeExito int32) (*pb.ResultadoDistraccion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.EmpezarDistraccion(ctx, &pb.SolicitudDistraccion{
		ClientId:            "michael",
		Character:           character,
		ProbabilidadDeExito: probabilidadDeExito,
	})

	if err != nil {
		return nil, err
	}

	return result, nil
}

func main() {
	// Conexión insegura local para pruebas
	conn, err := grpc.Dial("localhost:50051",
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("no se pudo conectar: %v", err)
	}
	defer conn.Close()

	client := pb.NewLesterClient(conn)
	clientID := "michael-1"
	ctx := context.Background()
	var heister pb.Character
	var heisterAddr string

	for {
		// 1) Pedir oferta a Lester
		resp, err := client.PedirOferta(context.Background(), &pb.PedirOfertaSolicitud{
			ClientId: clientID,
		})
		if err != nil {
			log.Printf("error PedirOferta: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		if !resp.GetAvailable() {
			log.Printf("[Michael] Lester sin oferta: %s", resp.GetMessage())
			time.Sleep(1 * time.Second)
			continue
		}

		o := resp.GetOffer()
		fmt.Printf("[Michael] Oferta recibida → Loot: $%d | pF=%d%% | pT=%d%% | Riesgo=%d%%\n",
			o.GetLoot(), o.GetSuccessFranklin(), o.GetSuccessTrevor(), o.GetPoliceRisk())

		riesgo := o.GetPoliceRisk()
		lootbase := o.GetLoot()

		// 2) Evaluar y decidir
		if aceptaSegunReglas(o) {
			_, err = client.NotificarDecision(context.Background(), &pb.SolicitudDecision{
				ClientId: clientID,
				Decision: pb.RespuestaDecision_ACCEPT,
				Offer:    o,
			})
			if err != nil {
				log.Printf("error NotificarDecision(ACCEPT): %v", err)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			fmt.Println("[Michael] ✅ Oferta aceptada según reglas. Fase 1 completada.")

			// 3) Fase 2: Asignar distracción (se elige Franklin o Trevor)
			var character string
			var Probabilidad int32
			if o.GetSuccessFranklin() > o.GetSuccessTrevor() {
				character = "Franklin" // O puedes elegir "Trevor" según la probabilidad
				Probabilidad = o.GetSuccessFranklin()
			} else {
				character = "Trevor"
				Probabilidad = o.GetSuccessTrevor()
			}

			// Conectar con el servidor de Franklin o Trevor
			var distractionConn *grpc.ClientConn
			if character == "Franklin" {
				distractionConn, err = grpc.Dial("localhost:50053", grpc.WithTransportCredentials(insecure.NewCredentials()))
			} else {
				distractionConn, err = grpc.Dial("localhost:50052", grpc.WithTransportCredentials(insecure.NewCredentials()))
			}

			if err != nil {
				log.Fatalf("no se pudo conectar con %s: %v", character, err)
			}
			defer distractionConn.Close()

			distractionClient := pb.NewServicioDistraccionesClient(distractionConn)
			result, err := startDistraction(distractionClient, character, Probabilidad)
			if err != nil {
				log.Printf("Error starting distraction for %s: %v", character, err)
				continue
			}

			// 4) Mostrar el resultado de la distracción
			if result.GetSuccess() {
				fmt.Printf("Distraction by %s succeeded!\n", character)
			} else {
				fmt.Printf("Distraction by %s failed due to: %s\n", character, result.GetReason())
			}

			// Golpe
			if character == "Franklin" && result.GetSuccess() {
				heister = pb.Character_TREVOR
				heisterAddr = trevorAddr
				Probabilidad = o.GetSuccessTrevor()
			} else if character == "Trevor" && result.GetSuccess() {
				heister = pb.Character_FRANKLIN
				heisterAddr = franklinAddr
				Probabilidad = o.GetSuccessFranklin()
			} else {
				break // Si la distracción falló, no se realiza el golpe
			}
			// Conectar con el servidor del personaje que hará el golpe
			cconn, err := grpc.Dial(heisterAddr, grpc.WithInsecure())
			if err != nil {
				log.Fatal(err)
			}
			defer cconn.Close()
			char := pb.NewCharacterServiceClient(cconn)

			// Avisar a lester quien hara el golpe
			_, err = client.StartHeistNotifications(ctx, &pb.StartHeistNotificationsRequest{
				Character:  heister,
				PoliceRisk: riesgo,
			})
			if err != nil {
				log.Fatal("StartHeistNotifications:", err)
			}
			log.Printf("[Michael] Lester notificado: inicia estrellas para %s", heister)

			// 2) Avisar al personaje parámetros (prob éxito, riesgo, loot)
			_, err = char.StartHeist(ctx, &pb.StartHeistRequest{
				Character:   heister,
				SuccessProb: int32(Probabilidad),
				PoliceRisk:  int32(riesgo),
				BaseLoot:    int64(lootbase),
			})
			if err != nil {
				log.Fatal("StartHeist:", err)
			}
			log.Printf("[Michael] %s informado: prob=%d%%, riesgo=%d%%, loot=$%d",
				heister.String(), Probabilidad, riesgo, o.GetLoot())

			// 3) Polling de estado
			ticker := time.NewTicker(time.Duration(pollMs) * time.Millisecond)
			defer ticker.Stop()

			for {
				log.Printf("[Michael] Estado: %s | stars=%d | %d/%d | loot=$%d | %s",
					st.State.String(), st.Stars, st.TurnsDone, st.TotalTurns, st.EarnedLoot, st.Detail)

				switch st.State {
				case pb.MissionState_STATE_SUCCESS:
					log.Printf("[Michael] Golpe COMPLETADO por %s. Loot total reportado=$%d",
						heister.String(), st.EarnedLoot)

					// 4) Avisar a Lester que termine de enviar estrellas
					_, _ = client.StopHeistNotifications(ctx, &pb.StopHeistNotificationsRequest{Character: heister})
					fmt.Println("[Michael] Lester: stop stars (éxito)")
					return
				case pb.MissionState_STATE_FAILED:
					log.Printf("[Michael] Golpe FALLIDO por %s. Motivo: %s",
						heister.String(), st.Detail)
					_, _ = client.StopHeistNotifications(ctx, &pb.StopHeistNotificationsRequest{Character: heister})
					fmt.Println("[Michael] Lester: stop stars (fallo)")
					return
				}
			}

			break // Finaliza el bucle después de la distracción

		} else {
			ack, err := client.NotificarDecision(context.Background(), &pb.SolicitudDecision{
				ClientId: clientID,
				Decision: pb.RespuestaDecision_REJECT,
				Offer:    o,
			})
			if err != nil {
				log.Printf("error NotificarDecision(REJECT): %v", err)
			} else {
				log.Printf("[Michael] ❌ Rechazada. %s (rechazos seguidos: %d)",
					ack.GetMessage(), ack.GetConsecutiveRejects())
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
}
