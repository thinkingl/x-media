package media

import (
	"fmt"
	"sync"
	"time"


	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/x-media/x-media-server/pkg/logger"
)

type rtspPath struct {
	stream    *gortsplib.ServerStream
	publisher *gortsplib.ServerSession
	// onPlay 该路径新 reader PLAY 后的回调（按路径注册，避免多 sink 共用
	// server 时全局回调被覆盖、错误 sink 向不匹配 stream 发包导致崩溃）。
	onPlay func(stream *gortsplib.ServerStream)
}

type RTSPServerHandler struct {
	server *gortsplib.Server
	mutex  sync.RWMutex
	paths  map[string]*rtspPath
}

func NewRTSPServerHandler() *RTSPServerHandler {
	return &RTSPServerHandler{
		paths: make(map[string]*rtspPath),
	}
}

func (h *RTSPServerHandler) SetServer(s *gortsplib.Server) {
	h.server = s
}

func (h *RTSPServerHandler) OnConnOpen(_ *gortsplib.ServerHandlerOnConnOpenCtx) {
	logger.Infof("RTSP connection opened")
}

func (h *RTSPServerHandler) OnConnClose(ctx *gortsplib.ServerHandlerOnConnCloseCtx) {
	logger.Infof("RTSP connection closed: %v", ctx.Error)
}

func (h *RTSPServerHandler) OnSessionOpen(_ *gortsplib.ServerHandlerOnSessionOpenCtx) {
	logger.Infof("RTSP session opened")
}

func (h *RTSPServerHandler) OnSessionClose(ctx *gortsplib.ServerHandlerOnSessionCloseCtx) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	for path, p := range h.paths {
		if p.publisher == ctx.Session {
			p.stream.Close()
			delete(h.paths, path)
			logger.Infof("RTSP publisher disconnected: %s", path)
			return
		}
	}
}

func (h *RTSPServerHandler) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (
	*base.Response, *gortsplib.ServerStream, error,
) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	path := trimLeadingSlash(ctx.Path)
	logger.Infof("RTSP DESCRIBE: path=%s ua=%s q=%s", path, reqUA(ctx.Request), ctx.Query)
	p, ok := h.paths[path]
	if !ok {
		logger.Infof("RTSP DESCRIBE -> 404: %s", path)
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	logger.Infof("RTSP DESCRIBE -> 200: %s", path)
	return &base.Response{StatusCode: base.StatusOK}, p.stream, nil
}

func (h *RTSPServerHandler) OnAnnounce(ctx *gortsplib.ServerHandlerOnAnnounceCtx) (
	*base.Response, error,
) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	path := trimLeadingSlash(ctx.Path)
	if p, ok := h.paths[path]; ok {
		p.stream.Close()
		if p.publisher != nil {
			p.publisher.Close()
		}
		delete(h.paths, path)
		logger.Infof("RTSP: replaced existing publisher on %s", path)
	}

	stream := &gortsplib.ServerStream{
		Server: h.server,
		Desc:   ctx.Description,
	}
	if err := stream.Initialize(); err != nil {
		return &base.Response{StatusCode: base.StatusInternalServerError}, err
	}

	h.paths[path] = &rtspPath{
		stream:    stream,
		publisher: ctx.Session,
	}

	logger.Infof("RTSP publisher connected: %s, streams: %d", path, len(ctx.Description.Medias))
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (h *RTSPServerHandler) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (
	*base.Response, *gortsplib.ServerStream, error,
) {
	path := trimLeadingSlash(ctx.Path)
	logger.Infof("RTSP SETUP: path=%s ua=%s transport=%s", path, reqUA(ctx.Request), transportSummary(ctx.Transport))

	if ctx.Session.State() == gortsplib.ServerSessionStatePreRecord {
		return &base.Response{StatusCode: base.StatusOK}, nil, nil
	}

	h.mutex.RLock()
	defer h.mutex.RUnlock()

	p, ok := h.paths[path]
	if !ok {
		logger.Infof("RTSP SETUP -> 404: %s", path)
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	logger.Infof("RTSP SETUP -> 200: %s", path)
	return &base.Response{StatusCode: base.StatusOK}, p.stream, nil
}

func (h *RTSPServerHandler) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (
	*base.Response, error,
) {
	path := trimLeadingSlash(ctx.Path)
	logger.Infof("RTSP PLAY: path=%s ua=%s", path, reqUA(ctx.Request))

	// 新 reader 建立：异步重放完整 GOP。同步发送会因 reader 未激活被 gortsplib 丢弃；
	// 延迟等待 PLAY 响应发出、reader 激活后再发送，保证 ffmpeg/VLC 探测窗口内收到关键帧。
	h.mutex.RLock()
	p, ok := h.paths[path]
	h.mutex.RUnlock()
	if ok && p.stream != nil && p.onPlay != nil {
		fn := p.onPlay
		st := p.stream
		go func() {
			time.Sleep(200 * time.Millisecond)
			fn(st)
		}()
	}

	return &base.Response{StatusCode: base.StatusOK}, nil
}

// reqUA 返回请求的 User-Agent（区分 VLC/ffmpeg 等客户端）。
func reqUA(req *base.Request) string {
	if req == nil {
		return "?"
	}
	if v, ok := req.Header["User-Agent"]; ok && len(v) > 0 {
		return v[0]
	}
	return "?"
}

// transportSummary 简述 RTSP transport 协商结果。
func transportSummary(t *gortsplib.SessionTransport) string {
	if t == nil {
		return "unknown"
	}
	return t.Protocol.String()
}

func (h *RTSPServerHandler) OnRecord(ctx *gortsplib.ServerHandlerOnRecordCtx) (
	*base.Response, error,
) {
	path := trimLeadingSlash(ctx.Path)

	ctx.Session.OnPacketRTPAny(func(medi *description.Media, _ format.Format, pkt *rtp.Packet) {
		h.mutex.RLock()
		defer h.mutex.RUnlock()

		if p, ok := h.paths[path]; ok {
			p.stream.WritePacketRTP(medi, pkt)
		}
	})

	logger.Infof("RTSP publisher started recording: %s", path)
	return &base.Response{StatusCode: base.StatusOK}, nil
}

type RTSPServerManager struct {
	mu      sync.RWMutex
	servers map[string]*gortsplib.Server
	handler map[string]*RTSPServerHandler

	// UDP/multicast transport 配置（可选；不配置则仅支持 TCP）
	udpRTPAddr    string
	udpRTCPAddr   string
	multicastIP   string
	multicastRTP  int
	multicastRTCP int
}

var globalRTSPManager = &RTSPServerManager{
	servers: make(map[string]*gortsplib.Server),
	handler: make(map[string]*RTSPServerHandler),
}

// ConfigureUDP 配置 RTSP 的 UDP/multicast transport 支持。
func (m *RTSPServerManager) ConfigureUDP(rtpAddr, rtcpAddr, multicastIP string, multicastRTP, multicastRTCP int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.udpRTPAddr = rtpAddr
	m.udpRTCPAddr = rtcpAddr
	m.multicastIP = multicastIP
	m.multicastRTP = multicastRTP
	m.multicastRTCP = multicastRTCP
}

// ConfigureRTSPUDP 全局配置 RTSP UDP/multicast transport（供 cmd/main 调用）。
func ConfigureRTSPUDP(rtpAddr, rtcpAddr, multicastIP string, multicastRTP, multicastRTCP int) {
	globalRTSPManager.ConfigureUDP(rtpAddr, rtcpAddr, multicastIP, multicastRTP, multicastRTCP)
}

func (m *RTSPServerManager) GetOrCreate(addr string) (*RTSPServerHandler, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if h, ok := m.handler[addr]; ok {
		return h, nil
	}

	h := NewRTSPServerHandler()
	srv := &gortsplib.Server{
		Handler:     h,
		RTSPAddress: addr,
		UDPRTPAddress:     m.udpRTPAddr,
		UDPRTCPAddress:    m.udpRTCPAddr,
		MulticastIPRange:  m.multicastIP,
		MulticastRTPPort:  m.multicastRTP,
		MulticastRTCPPort: m.multicastRTCP,
	}
	h.SetServer(srv)

	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("failed to start RTSP server on %s: %w", addr, err)
	}

	m.servers[addr] = srv
	m.handler[addr] = h
	logger.Infof("RTSP server started on %s (udp=%s/%s)", addr, m.udpRTPAddr, m.udpRTCPAddr)
	return h, nil
}

func (m *RTSPServerManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for addr, srv := range m.servers {
		srv.Close()
		logger.Infof("RTSP server stopped on %s", addr)
	}
	m.servers = make(map[string]*gortsplib.Server)
	m.handler = make(map[string]*RTSPServerHandler)
}

func trimLeadingSlash(s string) string {
	if len(s) > 0 && s[0] == '/' {
		return s[1:]
	}
	return s
}
