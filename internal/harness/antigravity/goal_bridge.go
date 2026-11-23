package antigravity

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"shioji/internal/domain"
)

const goalTool = "report_goal"

type goalBridge struct {
	http   *http.Server
	server mcpHTTPServer
	token  string
}

type mcpHTTPServer struct {
	Type    string      `json:"type"`
	Name    string      `json:"name"`
	URL     string      `json:"url"`
	Headers []mcpHeader `json:"headers"`
}
type mcpHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (r *runtime) startGoalBridge() (*goalBridge, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token := domain.NewID() + domain.NewID()
	bridge := &goalBridge{token: token, server: mcpHTTPServer{
		Type: "http", Name: "shioji_goal_" + token[:8], URL: "http://" + listener.Addr().String() + "/goal",
		Headers: []mcpHeader{{Name: "Authorization", Value: "Bearer " + token}},
	}}
	bridge.http = &http.Server{
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.handleGoalTool(bridge, w, req) }),
	}
	go func() { _ = bridge.http.Serve(listener) }()
	return bridge, nil
}

func (r *runtime) handleGoalTool(bridge *goalBridge, w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/goal" || subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), []byte("Bearer "+bridge.token)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if req.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var msg packet
	if json.NewDecoder(http.MaxBytesReader(w, req.Body, 8192)).Decode(&msg) != nil || msg.Version != "2.0" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(msg.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	var failure *rpcError
	switch msg.Method {
	case "initialize":
		var params struct{ ProtocolVersion string }
		_ = json.Unmarshal(msg.Params, &params)
		version := "2025-03-26"
		if params.ProtocolVersion == "2025-06-18" || params.ProtocolVersion == "2025-11-25" {
			version = params.ProtocolVersion
		}
		result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": struct{}{}},
			"serverInfo": map[string]string{"name": "Shioji Goal", "version": "1"}}
	case "ping":
		result = struct{}{}
	case "tools/list":
		result = map[string]any{"tools": []map[string]any{{
			"name": goalTool, "description": "Report verified progress on the current Shioji goal. Call at the end of the goal step.",
			"inputSchema": map[string]any{"type": "object", "required": []string{"key", "outcome"}, "additionalProperties": false,
				"properties": map[string]any{"key": map[string]string{"type": "string"},
					"outcome": map[string]any{"type": "string", "enum": []string{"continue", "blocked", "complete"}}}},
		}}}
	case "tools/call":
		var params struct {
			Name      string
			Arguments struct{ Key, Outcome string }
		}
		if json.Unmarshal(msg.Params, &params) != nil || params.Name != goalTool {
			failure = &rpcError{Code: -32602, Message: "Invalid goal report"}
			break
		}
		err := r.recordGoalReport(params.Arguments.Key, params.Arguments.Outcome)
		message := "Goal report recorded for this turn."
		if err != nil {
			message = err.Error()
		}
		result = map[string]any{"isError": err != nil, "content": []map[string]string{{"type": "text", "text": message}}}
	default:
		failure = &rpcError{Code: -32601, Message: "Method not found"}
	}
	reply := packet{Version: "2.0", ID: msg.ID, Error: failure}
	if failure == nil {
		reply.Result, _ = json.Marshal(result)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

func (r *runtime) recordGoalReport(key, outcome string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.active
	if t == nil || t.goal == nil || t.interrupted || subtle.ConstantTimeCompare([]byte(key), []byte(t.goalKey)) != 1 {
		return errors.New("Goal report is no longer eligible in this turn.")
	}
	result := map[string]domain.GoalState{"continue": domain.GoalActive, "complete": domain.GoalComplete, "blocked": domain.GoalBlocked}[outcome]
	if result == "" {
		return errors.New("Use continue, blocked, or complete.")
	}
	t.goal.Outcome = result
	return nil
}

func (b *goalBridge) close(ctx context.Context) error {
	if b == nil {
		return nil
	}
	return b.http.Shutdown(ctx)
}
