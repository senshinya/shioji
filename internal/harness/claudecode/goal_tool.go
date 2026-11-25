package claudecode

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"shioji/internal/domain"
)

type goalServer struct {
	server *http.Server
	config map[string]any
}

func (r *runtime) openGoalServer() (*goalServer, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token := "Bearer " + domain.NewID() + domain.NewID()
	host := &goalServer{config: map[string]any{
		"type": "http", "url": "http://" + listener.Addr().String() + "/goal", "headers": map[string]string{"Authorization": token},
	}}
	host.server = &http.Server{ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/goal" || subtle.ConstantTimeCompare([]byte(request.Header.Get("Authorization")), []byte(token)) != 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if request.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			r.goalRPC(w, request)
		}),
	}
	go func() { _ = host.server.Serve(listener) }()
	return host, nil
}

func (r *runtime) goalRPC(w http.ResponseWriter, request *http.Request) {
	var rpc struct {
		Version string `json:"jsonrpc"`
		ID      json.RawMessage
		Method  string
		Params  struct {
			ProtocolVersion, Name string
			Arguments             struct{ Key, Outcome string }
		}
	}
	if json.NewDecoder(http.MaxBytesReader(w, request.Body, 8192)).Decode(&rpc) != nil || rpc.Version != "2.0" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(rpc.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	reply := map[string]any{"jsonrpc": "2.0", "id": rpc.ID}
	var result any
	switch rpc.Method {
	case "initialize":
		version := rpc.Params.ProtocolVersion
		if version != "2025-06-18" && version != "2025-11-25" {
			version = "2025-03-26"
		}
		result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": struct{}{}},
			"serverInfo": map[string]string{"name": "Shioji Goal", "version": "1"}}
	case "ping":
		result = struct{}{}
	case "tools/list":
		result = map[string]any{"tools": []map[string]any{{
			"name": "report_goal", "description": "Report verified progress for the current Shioji goal.",
			"inputSchema": map[string]any{"type": "object", "required": []string{"key", "outcome"},
				"additionalProperties": false, "properties": map[string]any{
					"key":     map[string]string{"type": "string"},
					"outcome": map[string]any{"type": "string", "enum": []string{"continue", "blocked", "complete"}},
				}},
		}}}
	case "tools/call":
		ok := rpc.Params.Name == "report_goal" && r.recordGoal(rpc.Params.Arguments.Key, rpc.Params.Arguments.Outcome)
		message := "This goal report is no longer eligible."
		if ok {
			message = "Goal report recorded."
		}
		result = map[string]any{"isError": !ok, "content": []map[string]string{{"type": "text", "text": message}}}
	default:
		reply["error"] = map[string]any{"code": -32601, "message": "Method not found"}
	}
	if result != nil {
		reply["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

func (r *runtime) recordGoal(key, outcome string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.turn
	if t == nil || t.goal == nil || t.stopping || key == "" ||
		subtle.ConstantTimeCompare([]byte(key), []byte(t.goalKey)) != 1 {
		return false
	}
	switch outcome {
	case "continue":
		t.goal.Outcome = domain.GoalActive
	case "blocked":
		t.goal.Outcome = domain.GoalBlocked
	case "complete":
		t.goal.Outcome = domain.GoalComplete
	default:
		return false
	}
	return true
}
