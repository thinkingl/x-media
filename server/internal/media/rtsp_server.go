package media

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/x-media/x-media-server/pkg/logger"
)

// RTSP 拉流传输协议策略（OutputConfig.Transport，RTSP server 模式）。
// auto 由 gortsplib 按全局配置自动协商（UDP 监听开启则支持 UDP，否则仅 TCP）。
const (
	RTSPTransportAuto       = "auto" // 自动适配（默认，空字符串同义）
	RTSPTransportTCP        = "tcp"
	RTSPTransportUDP        = "udp"
	RTSPTransportUDPMulticast = "udp-multicast"
)

type rtspPath struct {
	stream    *gortsplib.ServerStream
	publisher *gortsplib.ServerSession
	// onPlay 该路径新 reader PLAY 后的回调（按路径注册，避免多 sink 共用
	// server 时全局回调被覆盖、错误 sink 向不匹配 stream 发包导致崩溃）。
	onPlay func(stream *gortsplib.ServerStream)
	// readers 当前拉流客户端（key = *ServerSession），供前端展示与排障。
	readers map[*gortsplib.ServerSession]*rtspReader
	// transportPolicy 该路径强制要求的拉流传输协议（""=自动适配）。
	transportPolicy string
}

// rtspReader 一个 RTSP 拉流客户端。
type rtspReader struct {
	session     *gortsplib.ServerSession
	address     string
	userAgent   string
	transport   string
	connectedAt time.Time
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
		// 移除该 session 的 reader 记录（若存在）
		if p.readers != nil {
			if _, ok := p.readers[ctx.Session]; ok {
				delete(p.readers, ctx.Session)
				logger.Infof("RTSP reader disconnected: %s", path)
			}
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
	p, ok := h.paths[path]
	h.mutex.RUnlock()
	if !ok {
		logger.Infof("RTSP SETUP -> 404: %s", path)
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	// 传输协议策略校验：该路径若强制要求某协议，且客户端协商出的协议不匹配，
	// 返回 461 Unsupported Transport 拒绝 SETUP（客户端可回退重试其它协议）。
	if p.transportPolicy != "" && p.transportPolicy != RTSPTransportAuto &&
		!rtspTransportMatches(p.transportPolicy, ctx.Transport) {
		logger.Infof("RTSP SETUP -> 461: path=%s policy=%s got=%s",
			path, p.transportPolicy, transportSummary(ctx.Transport))
		return &base.Response{StatusCode: base.StatusUnsupportedTransport}, nil, nil
	}

	logger.Infof("RTSP SETUP -> 200: %s", path)
	return &base.Response{StatusCode: base.StatusOK}, p.stream, nil
}

// rtspTransportMatches 判断协商出的 transport 是否符合路径的传输协议策略。
func rtspTransportMatches(policy string, tr *gortsplib.SessionTransport) bool {
	if tr == nil {
		return false
	}
	switch policy {
	case RTSPTransportTCP:
		return tr.Protocol == gortsplib.ProtocolTCP
	case RTSPTransportUDP:
		return tr.Protocol == gortsplib.ProtocolUDP
	case RTSPTransportUDPMulticast:
		return tr.Protocol == gortsplib.ProtocolUDPMulticast
	}
	return true
}

func (h *RTSPServerHandler) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (
	*base.Response, error,
) {
	path := trimLeadingSlash(ctx.Path)
	logger.Infof("RTSP PLAY: path=%s ua=%s", path, reqUA(ctx.Request))

	// 记录拉流客户端信息（IP、UA、transport、连接时间），供前端展示与排障。
	addr := "?"
	if ctx.Conn != nil && ctx.Conn.NetConn() != nil {
		addr = ctx.Conn.NetConn().RemoteAddr().String()
	}
	h.mutex.Lock()
	if p, ok := h.paths[path]; ok {
		if p.readers == nil {
			p.readers = make(map[*gortsplib.ServerSession]*rtspReader)
		}
		p.readers[ctx.Session] = &rtspReader{
			session:     ctx.Session,
			address:     addr,
			userAgent:   reqUA(ctx.Request),
			transport:   transportSummary(ctx.Session.Transport()),
			connectedAt: time.Now(),
		}
	}
	h.mutex.Unlock()

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

	// writeQueueSize 每 reader 的 RTP 写队列容量（包数），0 使用 gortsplib 默认值。
	writeQueueSize int
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

// SetWriteQueueSize 设置每个 reader 的 RTP 写队列容量（包数）。
// 测试或高突发场景下可调大，避免 gortsplib 默认 256 包队列溢出丢包。
func (m *RTSPServerManager) SetWriteQueueSize(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeQueueSize = n
}

func (m *RTSPServerManager) GetOrCreate(addr string) (*RTSPServerHandler, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if h, ok := m.handler[addr]; ok {
		return h, nil
	}

	// UDP transport 端口：优先用全局配置；未配置时动态探测一对空闲相邻端口
	// （RTP 偶数 + RTCP=RTP+1），使每个不同 addr 的 RTSP server 独享一对 UDP
	// 端口，多个 server 并发不冲突，也免去"配了 udp 策略但全局没配 UDP"的坑。
	rtpAddr := m.udpRTPAddr
	rtcpAddr := m.udpRTCPAddr
	if rtpAddr == "" {
		r, rc, err := findFreeUDPPair()
		if err != nil {
			logger.Warnf("RTSP server %s: dynamic UDP port pair unavailable, UDP disabled: %v", addr, err)
		} else {
			rtpAddr, rtcpAddr = r, rc
		}
	}

	h := NewRTSPServerHandler()
	srv := &gortsplib.Server{
		Handler:           h,
		RTSPAddress:       addr,
		UDPRTPAddress:     rtpAddr,
		UDPRTCPAddress:    rtcpAddr,
		MulticastIPRange:  m.multicastIP,
		MulticastRTPPort:  m.multicastRTP,
		MulticastRTCPPort: m.multicastRTCP,
		WriteQueueSize:    m.writeQueueSize,
	}
	h.SetServer(srv)

	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("failed to start RTSP server on %s: %w", addr, err)
	}

	m.servers[addr] = srv
	m.handler[addr] = h
	logger.Infof("RTSP server started on %s (udp=%s/%s)", addr, rtpAddr, rtcpAddr)
	return h, nil
}

// findFreeUDPPair 探测一对连续的空闲 UDP 端口（RTP 偶数、RTCP = RTP+1），
// 供 RTSP server 动态绑定 UDP transport。
func findFreeUDPPair() (rtpAddr, rtcpAddr string, err error) {
	for i := 0; i < 20; i++ {
		ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: 0})
		if err != nil {
			continue
		}
		port := ln.LocalAddr().(*net.UDPAddr).Port
		ln.Close()
		if port == 65535 {
			continue
		}
		if port%2 != 0 {
			port--
		}
		// 验证该偶数端口及 +1 端口确实空闲（避免探测-关闭间被抢占）
		c1, err1 := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: port})
		c2, err2 := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(0, 0, 0, 0), Port: port + 1})
		if err1 == nil && err2 == nil {
			c1.Close()
			c2.Close()
			return net.JoinHostPort("0.0.0.0", strconv.Itoa(port)),
				net.JoinHostPort("0.0.0.0", strconv.Itoa(port+1)), nil
		}
		if c1 != nil {
			c1.Close()
		}
		if c2 != nil {
			c2.Close()
		}
	}
	return "", "", fmt.Errorf("no free consecutive UDP port pair found")
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
