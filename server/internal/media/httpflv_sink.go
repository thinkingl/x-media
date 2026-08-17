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

	// flvClients 当前连接的 HTTP-FLV 拉流客户端（key = *flvReader）。
	flvClients map[*flvReader]*flvClient
}

// flvClient 一个 HTTP-FLV 拉流客户端的信息。
type flvClient struct {
	address     string
	userAgent   string
	connectedAt time.Time
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
		flvClients:   make(map[*flvReader]*flvClient),
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
	metaTag := w.BuildOnMetaDataTag(streams)

	// 固定前缀：FLV header + onMetaData + config tags，新客户端总能拿到有效起点。
	// onMetaData 让 ffmpeg/VLC 的 flv demuxer 正确建立 dts 时间轴，避免缺失时
	// 的启发式错乱（重复帧/卡顿）。
	h.writer = w
	h.prefix = append(append([]byte{}, h.writer.Header()...), metaTag...)
	h.prefix = append(h.prefix, configTags...)
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

// Clients 返回当前连接的 HTTP-FLV 拉流客户端信息（实现 ClientInfoProvider）。
func (h *HTTPFLVSink) Clients() []ClientInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.flvClients) == 0 {
		return nil
	}
	out := make([]ClientInfo, 0, len(h.flvClients))
	for _, c := range h.flvClients {
		out = append(out, ClientInfo{
			Address:     c.address,
			UserAgent:   c.userAgent,
			Transport:   "http-flv",
			ConnectedAt: c.connectedAt,
		})
	}
	return out
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

	// 记录客户端信息（IP、UA、连接时间），断开时移除
	addr := "?"
	if r.RemoteAddr != "" {
		addr = r.RemoteAddr
	}
	h.mu.Lock()
	h.flvClients[reader] = &flvClient{
		address:     addr,
		userAgent:   r.UserAgent(),
		connectedAt: time.Now(),
	}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.flvClients, reader)
		h.mu.Unlock()
	}()

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
	// ring 满后 gopPos 指向的关键帧可能已被覆盖（head 前移，gopPos 处的字节
	// 可能已变成其他 tag 的中间），此时若仍从 gopPos 读会导致 tag 错位/花屏。
	// 稳妥起见：仅当 ring 未满且 gopPos 有效时从 GOP 起点开始，否则从 head。
	start := r.head
	if !r.full && r.gopPos != r.tail &&
		((r.head <= r.tail && r.gopPos >= r.head && r.gopPos < r.tail) ||
			(r.head > r.tail && (r.gopPos >= r.head || r.gopPos < r.tail))) {
		start = r.gopPos
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
	// 只读取"未读"数据：pos 到 tail 之间的字节，pos == tail 即无新数据。
	// 不能用 r.ring.full 作为读取条件——ring 写满后 full 恒为 true，
	// 会从 pos 无限重读整个缓冲导致帧重复（帧率失控根因）。
	for n < len(p) {
		if r.pos == r.ring.tail {
			break
		}
		p[n] = r.ring.buf[r.pos]
		r.pos = (r.pos + 1) % len(r.ring.buf)
		n++
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// 让 time 导入不因当前未使用而报错（保留供超时逻辑）。
var _ = time.Second
