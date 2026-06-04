package ccr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP Stdio Server
func RunMCPServer(broker *Broker) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			sendError(nil, -32700, "Parse error")
			continue
		}

		if req.Method != "" {
			handleMCPMethod(broker, req)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("mcp scanner error: %v", err)
	}
}

func sendResponse(id json.RawMessage, result any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	b, _ := json.Marshal(resp)
	fmt.Println(string(b))
}

func sendError(id json.RawMessage, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	b, _ := json.Marshal(resp)
	fmt.Println(string(b))
}

func handleMCPMethod(broker *Broker, req JSONRPCRequest) {
	switch req.Method {
	case "initialize":
		sendResponse(req.ID, map[string]any{
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
		sendResponse(req.ID, map[string]any{
			"tools": []map[string]any{
				{
					"name":        "nucleus_ccr_subscribe",
					"description": "Subscribe to wake events for this agent role.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"role"},
						"properties": map[string]any{
							"role": map[string]any{
								"type":        "string",
								"description": "Canonical agent role",
							},
							"timeout_seconds": map[string]any{
								"type":    "integer",
								"default": 270,
							},
							"since_marker": map[string]any{
								"type": "string",
							},
						},
					},
				},
				{
					"name":        "eidetic_ccr_subscribe",
					"description": "Subscribe to wake events for this agent role (alias).",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"role"},
						"properties": map[string]any{
							"role": map[string]any{
								"type":        "string",
								"description": "Canonical agent role",
							},
							"timeout_seconds": map[string]any{
								"type":    "integer",
								"default": 270,
							},
							"since_marker": map[string]any{
								"type": "string",
							},
						},
					},
				},
				{
					"name":        "heartbeat",
					"description": "Send subscription health heartbeat.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"subscription_id"},
						"properties": map[string]any{
							"subscription_id": map[string]any{
								"type": "string",
							},
						},
					},
				},
				{
					"name":        "test_wake",
					"description": "Trigger a wake event for testing.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"role"},
						"properties": map[string]any{
							"role": map[string]any{
								"type": "string",
							},
						},
					},
				},
			},
		})
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(req.ID, -32602, "Invalid params")
			return
		}

		if params.Name == "nucleus_ccr_subscribe" || params.Name == "eidetic_ccr_subscribe" {
			role, _ := params.Arguments["role"].(string)
			if !isCanonicalRole(role) {
				sendResponse(req.ID, map[string]any{
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
				select {
				case ev := <-s.Ch:
					// Received wake event
					b, _ := json.Marshal(ev)
					sendResponse(id, map[string]any{
						"subscription_id": s.ID,
						"content": []map[string]any{{
							"type": "text",
							"text": string(b),
						}},
					})
				case <-time.After(time.Duration(to) * time.Second):
					// Timeout
					sendResponse(id, map[string]any{
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
				sendResponse(req.ID, map[string]any{
					"isError": true,
					"content": []map[string]any{{
						"type": "text",
						"text": "subscription not found or stale",
					}},
				})
				return
			}
			sendResponse(req.ID, map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": "heartbeat ack",
				}},
			})
		} else if params.Name == "test_wake" {
			role, _ := params.Arguments["role"].(string)
			broker.WakeActive(role, "test", "test payload")
			sendResponse(req.ID, map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": "wake triggered",
				}},
			})
		} else {
			sendError(req.ID, -32601, "Tool not found")
		}
	default:
		sendError(req.ID, -32601, "Method not found")
	}
}

func isCanonicalRole(role string) bool {
	// Simple stub for now
	valid := map[string]bool{
		"main":                  true,
		"peer":                  true,
		"op_assistant":          true,
		"cc_tb":                 true,
		"agy":                   true,
		"test_hold":             true,
		"claude_code_test_hold": true,
	}
	return valid[role]
}
