package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
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
		id:     id,
		config: config,
		status: StreamStatusStopped,
		muxer:  NewHTTPFLVMuxer(""),
	}, nil
}

func (h *HTTPFLVOutput) ID() string           { return h.id }
func (h *HTTPFLVOutput) Status() StreamStatus { h.mu.RLock(); defer h.mu.RUnlock(); return h.status }

func (h *HTTPFLVOutput) GetRoutePath() string {
	return "/live/" + h.id + ".flv"
}

func (h *HTTPFLVOutput) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	h.muxer.AddClient(w)
	defer h.muxer.RemoveClient(w)

	h.muxer.EnsureFFmpeg()

	<-r.Context().Done()
}

func (h *HTTPFLVOutput) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status == StreamStatusRunning {
		return nil
	}
	h.ctx, h.cancel = context.WithCancel(ctx)
	h.status = StreamStatusRunning
	logger.Infof("HTTP-FLV output ready: %s", h.id)
	return nil
}

func (h *HTTPFLVOutput) StartWithFile(ctx context.Context, filePath string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status == StreamStatusRunning {
		return nil
	}
	h.ctx, h.cancel = context.WithCancel(ctx)
	h.status = StreamStatusRunning
	h.muxer.SetFileContext(h.ctx, filePath)
	logger.Infof("HTTP-FLV output ready: %s, file: %s", h.id, filePath)
	return nil
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

	return muxer.WritePacket(pkt)
}

type HTTPFLVMuxer struct {
	mu       sync.RWMutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	ctx      context.Context
	filePath string
	started  bool
	output   string
	clients  map[io.Writer]bool
}

func NewHTTPFLVMuxer(output string) *HTTPFLVMuxer {
	return &HTTPFLVMuxer{
		output:  output,
		clients: make(map[io.Writer]bool),
	}
}

func (m *HTTPFLVMuxer) SetFileContext(ctx context.Context, filePath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctx = ctx
	m.filePath = filePath
}

func (m *HTTPFLVMuxer) AddClient(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[w] = true
}

func (m *HTTPFLVMuxer) RemoveClient(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clients, w)
}

func (m *HTTPFLVMuxer) EnsureFFmpeg() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		return nil
	}

	if m.filePath == "" {
		return nil
	}

	args := []string{
		"-re",
		"-i", m.filePath,
		"-c:v", "copy",
		"-c:a", "copy",
		"-f", "flv",
		"pipe:1",
	}

	cmd := exec.CommandContext(m.ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	cmd.Stderr = nil
	m.cmd = cmd

	if err := cmd.Start(); err != nil {
		m.cmd = nil
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	go func() {
		err := cmd.Wait()
		if err != nil {
			logger.Debugf("ffmpeg exited for httpflv %s: %v", m.output, err)
		}
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				// Copy client list under lock, then release before writing
				m.mu.RLock()
				clients := make([]io.Writer, 0, len(m.clients))
				for w := range m.clients {
					clients = append(clients, w)
				}
				m.mu.RUnlock()
				for _, w := range clients {
					w.Write(data)
				}
			}
			if err != nil {
				break
			}
		}
	}()

	logger.Infof("HTTP-FLV ffmpeg started for %s", m.output)
	return nil
}

func (m *HTTPFLVMuxer) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
		m.cmd.Wait()
		m.cmd = nil
	}

	m.clients = make(map[io.Writer]bool)
	logger.Infof("HTTP-FLV muxer stopped for %s", m.output)
}

func (m *HTTPFLVMuxer) WritePacket(pkt *MediaPacket) error {
	m.mu.RLock()
	started := m.started
	stdin := m.stdin
	m.mu.RUnlock()

	if !started {
		return nil
	}

	if stdin == nil {
		return nil
	}

	_, err := stdin.Write(pkt.Data)
	return err
}
