package ccr

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// v0.1 stub-auth: allow all local connections for local-only WebSocket
		return true
	},
}

func RunWebSocketServer(broker *Broker, port string) {
	if port == "" {
		port = "8888" // default port
	}

	http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		// v0.1 stub-auth: log connection but don't strictly enforce token yet
		authHeader := r.Header.Get("Authorization")
		log.Printf("ccr ws: connection attempt with auth: %s", authHeader)

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ccr ws: upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("ccr ws: read error or disconnect: %v", err)
				break
			}

			handleWSMessage(conn, broker, msg)
		}
	})

	log.Printf("ccr ws: listening on 127.0.0.1:%s", port)
	if err := http.ListenAndServe("127.0.0.1:"+port, nil); err != nil {
		log.Printf("ccr ws: server failed: %v", err)
	}
}

func handleWSMessage(conn *websocket.Conn, broker *Broker, msg []byte) {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}

	if err := json.Unmarshal(msg, &req); err != nil {
		sendErrorToWS(conn, nil, -32700, "Parse error")
		return
	}

	if req.JSONRPC != "2.0" {
		sendErrorToWS(conn, req.ID, -32600, "Invalid Request")
		return
	}

	switch req.Method {
	case "initialize":
		sendResponseToWS(conn, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"serverInfo": map[string]any{
				"name":    "eidetic-ccr",
				"version": "0.1.0",
			},
		})
	case "notifications/initialized":
		// Ignore
	case "tools/list":
		sendResponseToWS(conn, req.ID, map[string]any{
			"tools": []map[string]any{
				{
					"name":        "nucleus_ccr_subscribe",
					"description": "Subscribe to autonomous wake events.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"role"},
						"properties": map[string]any{
							"role": map[string]any{
								"type": "string",
							},
							"timeout_seconds": map[string]any{
								"type": "number",
							},
						},
					},
				},
				{
					"name":        "eidetic_ccr_subscribe",
					"description": "Alias for nucleus_ccr_subscribe.",
				},
				{
					"name":        "heartbeat",
					"description": "Send subscription health heartbeat.",
				},
				{
					"name":        "test_wake",
					"description": "Trigger a wake event for testing.",
				},
			},
		})
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendErrorToWS(conn, req.ID, -32602, "Invalid params")
			return
		}

		if params.Name == "nucleus_ccr_subscribe" || params.Name == "eidetic_ccr_subscribe" {
			role, _ := params.Arguments["role"].(string)
			if !isCanonicalRole(role) {
				sendResponseToWS(conn, req.ID, map[string]any{
					"isError": true,
					"content": []map[string]any{{
						"type": "text",
						"text": "HARD_BLOCK_FLOOR: non-canonical role name rejected at subscribe time",
					}},
				})
				return
			}
			timeout := 270
			if t, ok := params.Arguments["timeout_seconds"].(float64); ok {
				timeout = int(t)
			}

			sub := broker.Subscribe(role)

			go func(id json.RawMessage, to int, s *Subscription) {
				defer broker.Unsubscribe(s.ID)
				select {
				case ev := <-s.Ch:
					b, _ := json.Marshal(ev)
					sendResponseToWS(conn, id, map[string]any{
						"subscription_id": s.ID,
						"content": []map[string]any{{
							"type": "text",
							"text": string(b),
						}},
					})
				case <-time.After(time.Duration(to) * time.Second):
					sendResponseToWS(conn, id, map[string]any{
						"subscription_id": s.ID,
						"content": []map[string]any{{
							"type": "text",
							"text": "timeout without wake event",
						}},
					})
				}
			}(req.ID, timeout, sub)
		} else if params.Name == "heartbeat" {
			subID, _ := params.Arguments["subscription_id"].(string)
			ok := broker.Heartbeat(subID)
			if !ok {
				sendResponseToWS(conn, req.ID, map[string]any{
					"isError": true,
					"content": []map[string]any{{
						"type": "text",
						"text": "subscription not found or stale",
					}},
				})
				return
			}
			sendResponseToWS(conn, req.ID, map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": "heartbeat ack",
				}},
			})
		} else if params.Name == "test_wake" {
			role, _ := params.Arguments["role"].(string)
			broker.WakeActive(role, "test", "test payload")
			sendResponseToWS(conn, req.ID, map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": "wake triggered",
				}},
			})
		} else {
			sendErrorToWS(conn, req.ID, -32601, "Tool not found")
		}
	default:
		sendErrorToWS(conn, req.ID, -32601, "Method not found")
	}
}

func sendResponseToWS(conn *websocket.Conn, id json.RawMessage, result map[string]any) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
	b, _ := json.Marshal(resp)
	conn.WriteMessage(websocket.TextMessage, b)
}

func sendErrorToWS(conn *websocket.Conn, id json.RawMessage, code int, message string) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	if id != nil {
		resp["id"] = id
	}
	b, _ := json.Marshal(resp)
	conn.WriteMessage(websocket.TextMessage, b)
}
