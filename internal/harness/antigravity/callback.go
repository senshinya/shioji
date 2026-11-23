package antigravity

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

type callbackBridge struct {
	server     *http.Server
	url, token string
}

func (r *runtime) startCallbackBridge() (*callbackBridge, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := &callbackBridge{url: "http://" + listener.Addr().String() + "/message", token: domain.NewID() + domain.NewID()}
	b.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.receiveCallback(b, w, req)
	}), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second}
	go func() { _ = b.server.Serve(listener) }()
	return b, nil
}

func (r *runtime) receiveCallback(b *callbackBridge, w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/message" || subtle.ConstantTimeCompare([]byte(req.Header.Get("Authorization")), []byte("Bearer "+b.token)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if req.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct{ ID, Text string }
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF ||
		input.ID == "" || len(input.ID) > 128 || strings.TrimSpace(input.Text) == "" || len(input.Text) > 32<<10 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	owned, source := r.session.ID, r.session.ThreadID
	r.mu.Unlock()
	if owned == "" || source == "" || r.options.OnMessage == nil {
		w.WriteHeader(http.StatusConflict)
		return
	}
	digest := sha256.Sum256([]byte(owned + "\x00" + source + "\x00" + input.ID))
	message := harness.IncomingMessage{OperationID: "agy-callback:" + hex.EncodeToString(digest[:]), SessionID: owned, SourceRef: source, Text: input.Text}
	if r.options.OnMessage(req.Context(), message) != nil {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (b *callbackBridge) close() error {
	if b == nil {
		return nil
	}
	return b.server.Close()
}
