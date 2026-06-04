package ccr

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"os"
	"time"
)

func RunUnixServer(path string, broker *Broker) {
	if path == "" {
		path = os.Getenv("CCR_UNIX_SOCKET_PATH")
		if path == "" {
			path = "/tmp/eidetic-ccr.sock"
		}
	}

	// Remove socket if it already exists
	os.Remove(path)

	l, err := net.Listen("unix", path)
	if err != nil {
		log.Fatalf("ccr serve: unix socket listen: %v", err)
	}
	defer l.Close()

	if err := os.Chmod(path, 0600); err != nil {
		log.Fatalf("ccr serve: chmod 0600 unix socket: %v", err)
	}

	log.Printf("ccr serve: unix socket listening at %s", path)

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Printf("ccr serve: unix accept error: %v", err)
			continue
		}
		go handleUnixConnection(conn, broker)
	}
}

func handleUnixConnection(conn net.Conn, broker *Broker) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Bytes()
		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			sendErrorToConn(conn, nil, -32700, "Parse error")
			continue
		}
		if req.Method != "" {
			handleUnixMethod(conn, broker, req)
		}
	}
}

func handleUnixMethod(conn net.Conn, broker *Broker, req JSONRPCRequest) {
	// Re-use logic from handleMCPMethod but send to conn
	switch req.Method {
	case "initialize":
		sendResponseToConn(conn, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]any{
				"name":    "eidetic-ccr",
				"version": "0.1",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		})
	case "notifications/initialized":
		// Do nothing
	case "tools/list":
		// Redacted for brevity but returns the same 3 tools
		sendResponseToConn(conn, req.ID, map[string]any{
			"tools": []map[string]any{
				{
					"name":        "nucleus_ccr_subscribe",
					"description": "Subscribe to wake events for this agent role.",
				},
				{
					"name":        "eidetic_ccr_subscribe",
					"description": "Subscribe to wake events for this agent role (alias).",
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
			sendErrorToConn(conn, req.ID, -32602, "Invalid params")
			return
		}

		if params.Name == "nucleus_ccr_subscribe" || params.Name == "eidetic_ccr_subscribe" {
			role, _ := params.Arguments["role"].(string)
			if !isCanonicalRole(role) {
				sendResponseToConn(conn, req.ID, map[string]any{
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
					// Received wake event
					b, _ := json.Marshal(ev)
					sendResponseToConn(conn, id, map[string]any{
						"subscription_id": s.ID,
						"content": []map[string]any{{
							"type": "text",
							"text": string(b),
						}},
					})
				case <-time.After(time.Duration(to) * time.Second):
					// Timeout
					sendResponseToConn(conn, id, map[string]any{
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
				sendResponseToConn(conn, req.ID, map[string]any{
					"isError": true,
					"content": []map[string]any{{
						"type": "text",
						"text": "subscription not found or stale",
					}},
				})
				return
			}
			sendResponseToConn(conn, req.ID, map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": "heartbeat ack",
				}},
			})
		} else if params.Name == "test_wake" {
			role, _ := params.Arguments["role"].(string)
			broker.WakeActive(role, "test", "test payload")
			sendResponseToConn(conn, req.ID, map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": "wake triggered",
				}},
			})
		} else {
			sendErrorToConn(conn, req.ID, -32601, "Tool not found")
		}
	default:
		sendErrorToConn(conn, req.ID, -32601, "Method not found")
	}
}

func sendResponseToConn(conn net.Conn, id json.RawMessage, result any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	b, _ := json.Marshal(resp)
	b = append(b, '\n')
	conn.Write(b)
}

func sendErrorToConn(conn net.Conn, id json.RawMessage, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	b, _ := json.Marshal(resp)
	b = append(b, '\n')
	conn.Write(b)
}
