package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	pb "example/example_goproto/pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	lesterAddr   = "localhost:50051"
	franklinAddr = "localhost:50053"
	trevorAddr   = "localhost:50052"
	pollMs       = 200
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

func safeMsg(ack *pb.PaymentAck) string {
	if ack == nil {
		return "Sin respuesta"
	}
	return ack.Msg
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
				<-ticker.C
				st, err := char.GetStatus(ctx, &pb.GetStatusRequest{})
				if err != nil {
					log.Printf("[Michael] GetStatus error: %v", err)
					continue
				}

				log.Printf("[Michael] Estado: %s | stars=%d | %d/%d | loot=$%d | %s",
					st.State.String(), st.Stars, st.TurnsDone, st.TotalTurns, st.EarnedLoot, st.Detail)

				switch st.State {
				case pb.MissionState_STATE_SUCCESS:
					log.Printf("[Michael] Golpe COMPLETADO por %s. Loot total reportado=$%d",
						heister.String(), st.EarnedLoot)

					// 4) Avisar a Lester que termine de enviar estrellas
					_, _ = client.StopHeistNotifications(ctx, &pb.StopHeistNotificationsRequest{Character: heister})
					fmt.Println("[Michael] Lester: stop stars (éxito)")
					// ====== FASE 4: REPARTO DEL BOTÍN (solo si Fase 2 y 3 exitosas) ======
					// 4.1 pedir botín total al personaje del golpe
					lootResp, err := char.GetLootTotal(ctx, &pb.GetLootTotalRequest{})
					if err != nil {
						log.Printf("[Michael] Error GetLootTotal: %v", err)
						return
					}
					total := lootResp.TotalLoot
					if total <= 0 {
						log.Printf("[Michael] Botín total inválido: %d", total)
						return
					}

					// 4.2 calcular reparto
					share := total / 4
					rest := total % 4

					// helpers: stubs para pagarle a ambos personajes y a lester
					// ojo: quizá el que hizo el golpe fue FRANKLIN o TREVOR; el que NO hizo, también recibe su parte
					// prepara conexiones:
					fConn, _ := grpc.Dial("127.0.0.1:50053", grpc.WithInsecure())
					defer fConn.Close()
					tConn, _ := grpc.Dial("127.0.0.1:50054", grpc.WithInsecure())
					defer tConn.Close()

					frank := pb.NewCharacterServiceClient(fConn)
					trev := pb.NewCharacterServiceClient(tConn)

					// elegir a quién pagar qué
					// Por simplicidad: ambos personajes reciben "share"
					var ackF, ackT *pb.PaymentAck
					if heister == pb.Character_FRANKLIN {
						ackF, _ = frank.ReceivePayment(ctx, &pb.Payment{Amount: share})
						ackT, _ = trev.ReceivePayment(ctx, &pb.Payment{Amount: share})
					} else {
						ackT, _ = trev.ReceivePayment(ctx, &pb.Payment{Amount: share})
						ackF, _ = frank.ReceivePayment(ctx, &pb.Payment{Amount: share})
					}

					// Lester recibe share + rest
					ackL, _ := client.ReceivePayment(ctx, &pb.Payment{Amount: share + rest})

					// 4.3 Generar Reporte.txt (formato libre; el enunciado muestra un ejemplo)
					//    Debe incluir: resultado, botín base, botín extra (si hubo), botín total, pagos y respuestas. :contentReference[oaicite:6]{index=6}
					report := fmt.Sprintf(
						`=========================================================
== REPORTE FINAL DE LA MISION ==
=========================================================
Mision: Asalto al Banco #%d

Resultado Global: MISION COMPLETADA CON EXITO!

--- REPARTO DEL BOTIN ---
Botin Total: $%d
---------------------------------------------------------

Pago a Franklin: $%d
Respuesta de Franklin: "%s"

Pago a Trevor: $%d
Respuesta de Trevor: "%s"

Pago a Lester: $%d
Respuesta de Lester: "%s"

---------------------------------------------------------
Saldo Final de la Operacion: $%d
=========================================================
						`,
						time.Now().Unix()%10000,
						total,
						share, safeMsg(ackF),
						share, safeMsg(ackT),
						share+rest, safeMsg(ackL),
						total,
					)

					// escribe archivo
					if err := os.WriteFile("Reporte.txt", []byte(report), 0644); err != nil {
						log.Printf("[Michael] Error escribiendo Reporte.txt: %v", err)
					} else {
						log.Printf("[Michael] Reporte.txt generado")
					}

					return
				case pb.MissionState_STATE_FAILED:
					log.Printf("[Michael] Golpe FALLIDO por %s. Motivo: %s",
						heister.String(), st.Detail)
					_, _ = client.StopHeistNotifications(ctx, &pb.StopHeistNotificationsRequest{Character: heister})
					fmt.Println("[Michael] Lester: stop stars (fallo)")
					return
				}
			}

			break // Finaliza el bucle

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
