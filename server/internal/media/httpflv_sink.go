package media

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/x-media/x-media-server/pkg/logger"
)

// HTTPFLVSink 将标准帧封装为 FLV 并经 HTTP 流式提供（mode=http-flv）。
//
// 每个 HTTP 客户端连接从共享环形缓冲读取 FLV 字节流（含 header + sequence header）。
type HTTPFLVSink struct {
	mu sync.RWMutex

	id      string
	addr    string
	status  StreamStatus
	writer  *FLVWriter
	ring    *flvRing
	prefix  []byte // FLV header + sequence header，固定前缀
	ready   bool
	started bool

	// 配置项
	clientsLimit int
}

// NewHTTPFLVSink 创建 HTTP-FLV sink。
func NewHTTPFLVSink(config *OutputConfig) (*HTTPFLVSink, error) {
	if config.Addr == "" {
		return nil, ErrInvalidConfig
	}
	id := config.ID
	if id == "" {
		id = "flv_" + config.Addr
	}
	return &HTTPFLVSink{
		id:           id,
		addr:         config.Addr,
		status:       StreamStatusStopped,
		writer:       NewFLVWriter(),
		ring:         newFLVRing(2 * 1024 * 1024),
		clientsLimit: 100,
	}, nil
}

func (h *HTTPFLVSink) ID() string { return h.id }

func (h *HTTPFLVSink) Status() StreamStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.status
}

func (h *HTTPFLVSink) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status == StreamStatusRunning {
		return nil
	}
	h.status = StreamStatusRunning
	logger.Infof("HTTP-FLV sink started: %s, addr: %s", h.id, h.addr)
	return nil
}

// Configure 用 StreamInfo 初始化 FLV writer（时钟率）并生成 header/config tag。
func (h *HTTPFLVSink) Configure(streams []StreamInfo) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.configureLocked(streams)
}

func (h *HTTPFLVSink) configureLocked(streams []StreamInfo) error {
	w := NewFLVWriter()
	for _, s := range streams {
		if s.ClockRate > 0 {
			w.SetClockRate(s.ChannelID, s.ClockRate)
		}
	}
	configTags := w.BuildConfigTags(streams)

	// 固定前缀：FLV header + config tags，新客户端总能拿到有效起点。
	h.writer = w
	h.prefix = append(append([]byte{}, h.writer.Header()...), configTags...)
	h.ring.reset()
	h.ready = true
	logger.Infof("HTTP-FLV sink configured: %s, streams: %d", h.id, len(streams))
	return nil
}

// WriteFrame 将标准帧编码为 FLV tag 写入环形缓冲。
func (h *HTTPFLVSink) WriteFrame(f *Frame) error {
	h.mu.RLock()
	ready := h.ready
	status := h.status
	h.mu.RUnlock()

	if status != StreamStatusRunning || !ready {
		return nil
	}

	tag := h.writer.EncodeTag(f)
	if len(tag) == 0 {
		return nil
	}
	if f.Header.FrameType == FrameTypeVideo && f.Header.Flags&FlagKeyframe != 0 {
		h.ring.markGOP()
	}
	h.ring.write(tag)
	return nil
}

func (h *HTTPFLVSink) Notify(sig *Signal) error {
	return nil
}

func (h *HTTPFLVSink) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status == StreamStatusStopped {
		return nil
	}
	h.ready = false
	h.status = StreamStatusStopped
	logger.Infof("HTTP-FLV sink stopped: %s", h.id)
	return nil
}

// GetRoutePath 返回 HTTP 路由（兼容 server.go /live/:filename）。
func (h *HTTPFLVSink) GetRoutePath() string {
	return "/live/" + h.id + ".flv"
}

// ServeHTTP 提供 FLV 流。
func (h *HTTPFLVSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	h.mu.RLock()
	ready := h.ready
	h.mu.RUnlock()
	if !ready {
		http.Error(w, "stream not ready", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Connection", "close")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	reader := h.ring.newReader()
	defer reader.close()

	// 先发固定前缀（FLV header + sequence header）
	h.mu.RLock()
	prefix := h.prefix
	h.mu.RUnlock()
	if len(prefix) > 0 {
		if _, err := w.Write(prefix); err != nil {
			return
		}
	}
	// 再发 ring 中最近缓存的数据（从当前 head 起）
	h.ring.replayTo(reader, w)
	flusher.Flush()

	// 持续推送新数据
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-reader.notify():
		}
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				flusher.Flush()
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return
			}
			if n == 0 {
				break
			}
		}
	}
}

// ---- 环形缓冲 ----

// flvRing 并发安全的 FLV 字节环形缓冲。
type flvRing struct {
	mu     sync.Mutex
	buf    []byte
	head   int // 读位置（最旧）
	tail   int // 写位置（最新）
	full   bool
	gopPos int // 最近关键帧 tag 的起始位置（供新客户端从 GOP 起点拉流）
	subs   map[*flvReader]chan struct{}
}

func newFLVRing(size int) *flvRing {
	return &flvRing{
		buf:   make([]byte, size),
		subs:  make(map[*flvReader]chan struct{}),
		gopPos: 0,
	}
}

func (r *flvRing) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.head = 0
	r.tail = 0
	r.full = false
	r.gopPos = 0
}

// markGOP 记录当前位置为 GOP 起点（写关键帧 tag 前调用）。
func (r *flvRing) markGOP() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gopPos = r.tail
}

func (r *flvRing) write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range p {
		r.buf[r.tail] = b
		r.tail = (r.tail + 1) % len(r.buf)
		if r.full {
			r.head = (r.head + 1) % len(r.buf) // 覆盖最旧
		} else if r.tail == r.head {
			r.full = true
		}
	}
	r.notifyLocked()
}

func (r *flvRing) notifyLocked() {
	for _, ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (r *flvRing) newReader() *flvReader {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 新客户端从最近 GOP 起点开始读（可解码起点），紧跟 prefix 发送。
	start := r.gopPos
	if !r.full && r.tail == r.gopPos {
		start = r.head
	}
	rd := &flvReader{
		ring:     r,
		pos:      start,
		replayed: false,
		ch:       make(chan struct{}, 16),
	}
	r.subs[rd] = rd.ch
	return rd
}

func (r *flvRing) removeReader(rd *flvReader) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, rd)
}

// replayTo 将 reader 起点到当前写位置的数据写给 w（新客户端立即拿到 GOP 起点的数据）。
func (r *flvRing) replayTo(rd *flvReader, w io.Writer) {
	r.mu.Lock()
	if rd.replayed {
		r.mu.Unlock()
		return
	}
	start := rd.pos
	n := (r.tail - start + len(r.buf)) % len(r.buf)
	buf := make([]byte, n)
	i := start
	for j := 0; j < n; j++ {
		buf[j] = r.buf[i]
		i = (i + 1) % len(r.buf)
	}
	rd.pos = r.tail
	rd.replayed = true
	r.mu.Unlock()

	if len(buf) > 0 {
		_, _ = w.Write(buf)
	}
}

func (r *flvRing) availableLocked() int {
	if r.full {
		return len(r.buf)
	}
	return (r.tail - r.head + len(r.buf)) % len(r.buf)
}

// flvReader 单个客户端读游标。
type flvReader struct {
	ring     *flvRing
	pos      int
	replayed bool
	ch       chan struct{}
}

func (r *flvReader) close() {
	r.ring.removeReader(r)
}

func (r *flvReader) notify() <-chan struct{} { return r.ch }

func (r *flvReader) Read(p []byte) (int, error) {
	r.ring.mu.Lock()
	defer r.ring.mu.Unlock()
	n := 0
	for n < len(p) {
		if r.ring.full || r.pos != r.ring.tail {
			p[n] = r.ring.buf[r.pos]
			r.pos = (r.pos + 1) % len(r.ring.buf)
			n++
			continue
		}
		break
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// 让 time 导入不因当前未使用而报错（保留供超时逻辑）。
var _ = time.Second
