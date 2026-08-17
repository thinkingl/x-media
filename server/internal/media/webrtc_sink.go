package media

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/x-media/x-media-server/pkg/logger"
)

// WebRTCSink 将标准帧封装为 RTP 并经 WebRTC 推送给浏览器（WHEP 协议）。
//
// 阶段1能力：
//   - 仅支持 H264 视频源（浏览器 WebRTC 不支持 H265；H265 源 Configure 报错）
//   - WHEP 信令（HTTP POST SDP offer → SDP answer）
//   - 单/多 PeerConnection 客户端广播
//   - 关键帧前置 SPS/PPS，新客户端任意时刻加入可解码
type WebRTCSink struct {
	mu     sync.RWMutex
	id     string
	addr   string
	status StreamStatus
	ready  bool

	// 编码状态（Configure 时初始化）
	streams     []StreamInfo
	h264Enc     *rtph264.Encoder
	videoClock  int
	sps, pps    []byte // H264 参数集（关键帧前置）
	lastKeyframe []byte // 最近完整关键帧（含 SPS/PPS），新客户端重放
	lastKeyPTS  int64

	encodeMu sync.Mutex // 串行化 rtph264.Encoder（多客户端并发写时防竞态）

	// 客户端管理
	clientsMu sync.RWMutex
	clients   map[*webrtc.PeerConnection]*webrtcTrackPair
}

// webrtcTrackPair 单个 PeerConnection 的音视频发送轨道。
type webrtcTrackPair struct {
	videoTrack *webrtc.TrackLocalStaticRTP
	remoteAddr string

	// 发送队列：WriteFrame 投递 RTP 包，writer goroutine 逐包发送。
	// 提供背压 + 分片 pacing，避免关键帧分片瞬间突发打满接收端 UDP 缓冲。
	queue   chan *rtp.Packet
	closeCh chan struct{}
	once    sync.Once

	// 统计
	dropped atomic.Int64
}

// newWebRTCTrackPair 创建发送轨道 + 启动 writer goroutine。
func (s *WebRTCSink) newWebRTCTrackPair(track *webrtc.TrackLocalStaticRTP, addr string) *webrtcTrackPair {
	pair := &webrtcTrackPair{
		videoTrack: track,
		remoteAddr: addr,
		queue:      make(chan *rtp.Packet, 512),
		closeCh:    make(chan struct{}),
	}
	go pair.writeLoop()
	return pair
}

// writeLoop 消费发送队列并逐包写入 SRTP/UDP。关键帧分片间做 pacing。
func (p *webrtcTrackPair) writeLoop() {
	for {
		select {
		case <-p.closeCh:
			return
		case pkt := <-p.queue:
			if err := p.videoTrack.WriteRTP(pkt); err != nil {
				// 网络不可达：标记客户端失效（由上层移除）
				logger.Warnf("WebRTC track write failed: %v", err)
				return
			}
		}
	}
}

// enqueue 投递一个 RTP 包到发送队列。队列满则丢弃并计数（live 语义保新帧）。
func (p *webrtcTrackPair) enqueue(pkt *rtp.Packet) {
	select {
	case p.queue <- pkt:
	default:
		p.dropped.Add(1)
	}
}

// stop 关闭 writer goroutine。
func (p *webrtcTrackPair) stop() {
	p.once.Do(func() {
		close(p.closeCh)
	})
}

// NewWebRTCSink 创建 WebRTC sink。
func NewWebRTCSink(config *OutputConfig) (*WebRTCSink, error) {
	id := config.ID
	if id == "" {
		id = "webrtc_" + config.Addr
	}
	return &WebRTCSink{
		id:      id,
		addr:    config.Addr,
		status:  StreamStatusStopped,
		clients: make(map[*webrtc.PeerConnection]*webrtcTrackPair),
	}, nil
}

func (s *WebRTCSink) ID() string { return s.id }

func (s *WebRTCSink) Status() StreamStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *WebRTCSink) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == StreamStatusRunning {
		return nil
	}
	s.status = StreamStatusRunning
	logger.Infof("WebRTC sink started: %s", s.id)
	return nil
}

// Configure 用 StreamInfo 初始化编码器。仅支持 H264 视频；H265 源报错。
func (s *WebRTCSink) Configure(streams []StreamInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var hasVideo bool
	for _, st := range streams {
		if st.Kind != "video" {
			continue
		}
		hasVideo = true
		if st.CodecID != CodecH264 {
			return fmt.Errorf("WebRTC sink only supports H264 video, got codec %s", st.CodecName)
		}
		// 缓存参数集
		sps, pps := splitCodecConfigVideo(st.CodecConfig)
		if len(sps) == 0 || len(pps) == 0 {
			return fmt.Errorf("video stream missing SPS/PPS in codec config")
		}
		s.sps, s.pps = sps, pps
		s.videoClock = st.ClockRate
		if s.videoClock <= 0 {
			s.videoClock = 90000
		}
		// 初始化 H264 RTP 编码器
		if s.h264Enc == nil {
			enc := &rtph264.Encoder{
				PayloadType:       96,
				PacketizationMode: 1,
			}
			if err := enc.Init(); err != nil {
				return fmt.Errorf("init H264 encoder: %w", err)
			}
			s.h264Enc = enc
		}
	}
	if !hasVideo {
		return fmt.Errorf("WebRTC sink requires a video stream")
	}
	s.streams = streams
	s.ready = true
	logger.Infof("WebRTC sink configured: %s, clock=%d", s.id, s.videoClock)
	return nil
}

// WriteFrame 将标准帧封装为 RTP 并广播给所有 WebRTC 客户端。
func (s *WebRTCSink) WriteFrame(f *Frame) error {
	s.mu.RLock()
	ready := s.ready
	enc := s.h264Enc
	clock := s.videoClock
	sps, pps := s.sps, s.pps
	s.mu.RUnlock()

	if !ready || enc == nil {
		return nil
	}
	if f.Header.FrameType != FrameTypeVideo || f.Header.Codec != CodecH264 {
		return nil // 阶段1仅视频
	}

	// 关键帧：前置 SPS/PPS，缓存供新客户端重放
	if f.Header.Flags&FlagKeyframe != 0 {
		paramNals := make([][]byte, 0, 2+len(splitAnnexB(f.Payload)))
		if len(sps) > 0 {
			paramNals = append(paramNals, sps)
		}
		if len(pps) > 0 {
			paramNals = append(paramNals, pps)
		}
		nalUnits := splitAnnexB(f.Payload)
		nalUnits = append(paramNals, nalUnits...)
		s.mu.Lock()
		s.lastKeyframe = joinNals(nalUnits)
		s.lastKeyPTS = f.Header.PTS
		s.mu.Unlock()
		f.Payload = s.lastKeyframe
	}

	// 封装 RTP（并发写需串行，pion encoder 非并发安全）
	s.encodeMu.Lock()
	pkts, err := enc.Encode(splitAnnexB(f.Payload))
	s.encodeMu.Unlock()
	if err != nil {
		logger.Errorf("WebRTC H264 encode: %v", err)
		return nil
	}
	pts := uint32(To90k(f.Header.PTS, clock))
	spread := packetSpreadInterval(len(pkts)) // 关键帧分片 pacing

	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	for _, c := range s.clients {
		if c.videoTrack == nil {
			continue
		}
		// 投递到每客户端发送队列（writer goroutine 逐包发送 + pacing）。
		// 队列投递本身是瞬时动作，pacing 在 writeLoop 中按分片间隔摊开。
		for _, p := range pkts {
			p.Timestamp = pts
			c.enqueue(p)
			if spread > 0 {
				time.Sleep(spread) // 分片间节流，避免突发打满接收缓冲
			}
		}
	}
	return nil
}

// Notify 接收 source 的异步事件。
func (s *WebRTCSink) Notify(sig *Signal) error {
	return nil
}

func (s *WebRTCSink) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == StreamStatusStopped {
		return nil
	}
	s.ready = false
	s.status = StreamStatusStopped

	// 关闭所有客户端 PeerConnection + 发送 goroutine
	s.clientsMu.Lock()
	for pc, c := range s.clients {
		c.stop()
		_ = pc.Close()
	}
	s.clients = make(map[*webrtc.PeerConnection]*webrtcTrackPair)
	s.clientsMu.Unlock()

	logger.Infof("WebRTC sink stopped: %s", s.id)
	return nil
}

// Clients 返回当前连接的 WebRTC 拉流客户端信息（实现 ClientInfoProvider）。
func (s *WebRTCSink) Clients() []ClientInfo {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	if len(s.clients) == 0 {
		return nil
	}
	out := make([]ClientInfo, 0, len(s.clients))
	for _, c := range s.clients {
		addr := c.remoteAddr
		if addr == "" {
			addr = "webrtc-peer"
		}
		out = append(out, ClientInfo{
			Address:   addr,
			UserAgent: "webrtc",
			Transport: "webrtc",
		})
	}
	return out
}

// registerClient 注册新客户端 PeerConnection。
func (s *WebRTCSink) registerClient(pc *webrtc.PeerConnection, pair *webrtcTrackPair) {
	s.clientsMu.Lock()
	s.clients[pc] = pair
	s.clientsMu.Unlock()
}

// unregisterClient 移除客户端 PeerConnection。
func (s *WebRTCSink) unregisterClient(pc *webrtc.PeerConnection) {
	s.clientsMu.Lock()
	delete(s.clients, pc)
	s.clientsMu.Unlock()
}
