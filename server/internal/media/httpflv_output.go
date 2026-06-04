package media

import (
	"context"
	"io"
	"net/http"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

type HTTPFLVOutput struct {
	mu        sync.RWMutex
	id        string
	config    *OutputConfig
	status    StreamStatus
	cancel    context.CancelFunc
	ctx       context.Context
	server    *http.Server
	clients   map[io.Writer]bool
	clientsMu sync.RWMutex
	muxer     *HTTPFLVMuxer
}

func NewHTTPFLVOutput(config *OutputConfig) (*HTTPFLVOutput, error) {
	if config.Addr == "" {
		return nil, ErrInvalidConfig
	}
	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}
	return &HTTPFLVOutput{
		id:      id,
		config:  config,
		status:  StreamStatusStopped,
		clients: make(map[io.Writer]bool),
		muxer:   NewHTTPFLVMuxer(""),
	}, nil
}

func (h *HTTPFLVOutput) ID() string           { return h.id }
func (h *HTTPFLVOutput) Status() StreamStatus { h.mu.RLock(); defer h.mu.RUnlock(); return h.status }

func (h *HTTPFLVOutput) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status == StreamStatusRunning {
		return nil
	}

	h.ctx, h.cancel = context.WithCancel(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+h.id+".flv", h.serveFLV)
	mux.HandleFunc("/live/"+h.id+".flv", h.serveFLV)

	h.server = &http.Server{
		Addr:    h.config.Addr,
		Handler: mux,
	}

	go func() {
		if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorf("HTTP-FLV server start failed: %v", err)
		}
	}()

	h.status = StreamStatusRunning
	logger.Infof("HTTP-FLV output ready: %s, addr: %s", h.id, h.config.Addr)
	return nil
}

func (h *HTTPFLVOutput) serveFLV(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Connection", "close")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flvHeader := []byte{
		0x46, 0x4C, 0x56, 0x01,
		0x01,
		0x00, 0x00, 0x00, 0x09,
		0x00, 0x00, 0x00, 0x00,
	}
	w.Write(flvHeader)
	flusher.Flush()

	h.clientsMu.Lock()
	h.clients[w] = true
	h.clientsMu.Unlock()

	defer func() {
		h.clientsMu.Lock()
		delete(h.clients, w)
		h.clientsMu.Unlock()
	}()

	<-r.Context().Done()
}

func (h *HTTPFLVOutput) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status == StreamStatusStopped {
		return nil
	}
	if h.cancel != nil {
		h.cancel()
	}
	h.muxer.Stop()
	if h.server != nil {
		h.server.Close()
		h.server = nil
	}
	h.clientsMu.Lock()
	h.clients = make(map[io.Writer]bool)
	h.clientsMu.Unlock()
	h.status = StreamStatusStopped
	logger.Infof("HTTP-FLV output stopped: %s", h.id)
	return nil
}

func (h *HTTPFLVOutput) WritePacket(pkt *MediaPacket) error {
	h.mu.RLock()
	status := h.status
	muxer := h.muxer
	h.mu.RUnlock()

	if status != StreamStatusRunning {
		return ErrStreamNotRunning
	}

	if !muxer.started {
		h.mu.Lock()
		if !muxer.started {
			if err := muxer.Start(h.ctx, pkt.CodecID); err != nil {
				h.mu.Unlock()
				return err
			}
		}
		h.mu.Unlock()
	}
	return muxer.WritePacket(pkt)
}
