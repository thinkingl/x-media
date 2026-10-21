package media

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/pion/rtp"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

// RTSPInput 将远程 RTSP 流解包为标准帧（数据面）并响应信令（控制面）。
//
// 拉流流程：Describe → SetupAll → Play → OnPacketRTP 逐包 depacketize →
// 组装 access unit → Frame。支持 H.264 视频与 AAC 音频。
type RTSPInput struct {
	mu sync.RWMutex

	id       string
	url      string
	status   StreamStatus
	handler  FrameHandler
	handlers []FrameHandler

	client    *gortsplib.Client
	streams   []StreamInfo
	decoders  map[uint8]*rtph264.Decoder
	transport string // tcp/udp，空则自动
	udpBuf    int    // UDP 读缓冲大小（字节）
	pktCount  uint64 // 收到的 RTP 包计数（诊断用）
	cancel    context.CancelFunc
	done      chan struct{}
	// reconnectBackoff 断流后的重连退避间隔（测试可注入）。
	reconnectBackoff time.Duration
}

func NewRTSPInput(config *InputConfig) (*RTSPInput, error) {
	if config.URL == "" {
		return nil, ErrInvalidConfig
	}
	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}
	return &RTSPInput{
		id:               id,
		url:              config.URL,
		status:           StreamStatusStopped,
		decoders:         make(map[uint8]*rtph264.Decoder),
		transport:        config.Transport,
		udpBuf:           config.UDPReadBufferSize,
		reconnectBackoff: 3 * time.Second,
	}, nil
}

func (r *RTSPInput) ID() string { return r.id }

func (r *RTSPInput) Status() StreamStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// Start 连接 RTSP 服务器并开始拉流。
func (r *RTSPInput) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.status == StreamStatusRunning {
		r.mu.Unlock()
		return nil
	}
	// readLoop 仍在运行（断流重连中，status=Error）：不得重复启动第二个
	// readLoop —— 两个循环共享 r.client/decoders，会造成双会话、帧重复
	// 分发甚至 PacketPTS 跨 client 的 nil 解引用。重连成功后由循环自行
	// 置回 Running。
	if r.done != nil {
		select {
		case <-r.done: // 上一个 readLoop 已退出，允许重新 Start
		default:
			r.mu.Unlock()
			return nil
		}
	}

	u, err := base.ParseURL(r.url)
	if err != nil {
		r.status = StreamStatusError
		r.mu.Unlock()
		return fmt.Errorf("parse rtsp url: %w", err)
	}

	client, desc, streams, err := r.connectOnce(u)
	if err != nil {
		r.status = StreamStatusError
		r.mu.Unlock()
		return err
	}

	ctx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})
	r.client = client
	r.streams = streams
	r.status = StreamStatusRunning
	r.mu.Unlock()

	go r.readLoop(ctx, client, desc, u)

	logger.Infof("RTSP input started: %s, url: %s, streams: %d", r.id, r.url, len(streams))
	return nil
}

// connectOnce 建立一次 RTSP 会话：TCP 预检 + client 启动 + Describe。
// 供 Start 与断流后的重连共用。
func (r *RTSPInput) connectOnce(u *base.URL) (*gortsplib.Client, *description.Session, []StreamInfo, error) {
	// 预检 TCP 连通性，避免 Describe 无超时挂起。
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("rtsp connect %s: %w", u.Host, err)
	}
	conn.Close()

	client := &gortsplib.Client{
		Scheme:      u.Scheme,
		Host:        u.Host,
		ReadTimeout: 10 * time.Second,
	}
	switch r.transport {
	case "tcp":
		proto := gortsplib.ProtocolTCP
		client.Protocol = &proto
	case "udp":
		proto := gortsplib.ProtocolUDP
		client.Protocol = &proto
		client.UDPReadBufferSize = r.udpBuf
	}
	if err := client.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("rtsp client start: %w", err)
	}

	desc, _, err := client.Describe(u)
	if err != nil {
		client.Close()
		return nil, nil, nil, fmt.Errorf("rtsp describe: %w", err)
	}

	streams, err := buildStreamsFromDescription(desc)
	if err != nil {
		client.Close()
		return nil, nil, nil, fmt.Errorf("rtsp media unsupported: %w", err)
	}
	return client, desc, streams, nil
}

// buildStreamsFromDescription 从 SDP 提取 StreamInfo。
func buildStreamsFromDescription(desc *description.Session) ([]StreamInfo, error) {
	var streams []StreamInfo
	channelID := uint8(0)

	for _, medi := range desc.Medias {
		for _, f := range medi.Formats {
			switch tf := f.(type) {
			case *format.H264:
				streams = append(streams, StreamInfo{
					ChannelID:   channelID,
					Kind:        "video",
					CodecID:     CodecH264,
					CodecName:   "H264",
					ClockRate:   tf.ClockRate(),
					CodecConfig: appendAnnexBNAL(nil, tf.SPS),
				})
				cfg := appendAnnexBNAL(nil, tf.SPS)
				cfg = appendAnnexBNAL(cfg, tf.PPS)
				streams[len(streams)-1].CodecConfig = cfg
				channelID++
			case *format.Generic:
				// AAC via generic (MPEG4-GENERIC)
				config := aacConfigFromFMTP(tf.FMTP())
				if len(config) < 2 {
					continue
				}
				streams = append(streams, StreamInfo{
					ChannelID:   channelID,
					Kind:        "audio",
					CodecID:     CodecAAC,
					CodecName:   "AAC",
					ClockRate:   tf.ClockRate(),
					CodecConfig: config,
				})
				channelID++
			case *format.MPEG4Audio:
				// MPEG4-GENERIC AAC（gortsplib v5 对标准 IPC/本项目 sink 的
				// SDP 解析结果）。此前只认 format.Generic（死代码）导致
				// 音频流既不通告也无法解包。
				if tf.Config == nil {
					continue
				}
				cfg, err := tf.Config.Marshal()
				if err != nil {
					logger.Warnf("RTSP input aac config marshal: %v", err)
					continue
				}
				streams = append(streams, StreamInfo{
					ChannelID:   channelID,
					Kind:        "audio",
					CodecID:     CodecAAC,
					CodecName:   "AAC",
					ClockRate:   int(tf.Config.SampleRate),
					CodecConfig: cfg,
				})
				channelID++
			}
		}
	}

	if len(streams) == 0 {
		return nil, fmt.Errorf("no supported codecs in RTSP stream")
	}
	return streams, nil
}

// aacConfigFromFMTP 从 fmtp config 字段提取 AudioSpecificConfig。
func aacConfigFromFMTP(fmtp map[string]string) []byte {
	cfgStr := fmtp["config"]
	if len(cfgStr) < 4 {
		return nil
	}
	var b [2]byte
	for i := 0; i < 2; i++ {
		v := 0
		for j := 0; j < 2; j++ {
			c := cfgStr[i*2+j]
			d := 0
			if c >= '0' && c <= '9' {
				d = int(c - '0')
			} else if c >= 'a' && c <= 'f' {
				d = int(c-'a') + 10
			} else if c >= 'A' && c <= 'F' {
				d = int(c-'A') + 10
			}
			v = v*16 + d
		}
		b[i] = byte(v)
	}
	return b[:]
}

// readLoop 拉流主循环：会话运行 → 断开检测 → 退避重连。
//
// 远端断开时 gortsplib 的 client.Wait() 返回断开原因；此前只等 ctx.Done，
// 断流后 status 永远 running、无帧、不重试。现在检测到断开后置 error，
// 按 reconnectBackoff 退避重连（连接失败同样退避重试）。
func (r *RTSPInput) readLoop(ctx context.Context, client *gortsplib.Client, desc *description.Session, u *base.URL) {
	defer close(r.done)

	failures := 0
	for {
		stopped := r.runSession(ctx, client, desc)
		if stopped {
			return // Stop（ctx 取消）
		}
		failures++
		r.setError()
		if failures == 1 || failures%10 == 0 {
			logger.Warnf("RTSP input session ended (%d consecutive) [%s], retrying in %s", failures, r.id, r.reconnectBackoff)
		}
		if !sleepWithContext(ctx, r.reconnectBackoff) {
			return
		}
		for {
			c2, desc2, _, err := r.connectOnce(u)
			if err == nil {
				client, desc = c2, desc2
				// 更新 client 字段：handleVideoPacket 用其换算 PTS
				r.mu.Lock()
				r.client = c2
				r.mu.Unlock()
				break
			}
			failures++
			if failures == 1 || failures%10 == 0 {
				logger.Warnf("RTSP input reconnect failed (%d consecutive) [%s]: %v", failures, r.id, err)
			}
			if !sleepWithContext(ctx, r.reconnectBackoff) {
				return
			}
		}
	}
}

// sleepWithContext 退避等待；ctx 取消返回 false。
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// runSession 运行一次 RTSP 会话（decoder 初始化 → SetupAll → 回调注册 →
// Play → 等待会话结束）。返回 true 表示 ctx 取消（正常停止），
// false 表示会话断开或建立失败（触发外层重连循环）。
func (r *RTSPInput) runSession(ctx context.Context, client *gortsplib.Client, desc *description.Session) bool {

	channelID := uint8(0)
	videoChannel := uint8(0)
	var videoMedia *description.Media
	var videoFmt *format.H264
	var audioMedia *description.Media
	var audioFmtMPEG4 *format.MPEG4Audio
	var audioChannel uint8

	for _, medi := range desc.Medias {
		for _, f := range medi.Formats {
			switch tf := f.(type) {
			case *format.H264:
				videoMedia = medi
				videoFmt = tf
				videoChannel = channelID
				dec, err := tf.CreateDecoder()
				if err != nil {
					logger.Errorf("RTSP input h264 decoder: %v", err)
					r.setError()
					return false
				}
				r.mu.Lock()
				r.decoders[channelID] = dec
				r.mu.Unlock()
				// channelID 仅在匹配分支递增，与 buildStreamsFromDescription
				// 保持一致（此前无条件递增，SDP 含不匹配格式时通道错位）。
				channelID++
			case *format.MPEG4Audio:
				audioMedia = medi
				audioFmtMPEG4 = tf
				audioChannel = channelID
				channelID++
			case *format.Generic:
				audioMedia = medi
				audioChannel = channelID
				channelID++
			}
		}
	}

	logger.Infof("RTSP input before SetupAll [%s] medias=%d baseURL=%s", r.id, len(desc.Medias), desc.BaseURL.String())
	if err := client.SetupAll(desc.BaseURL, desc.Medias); err != nil {
		logger.Errorf("RTSP input setup: %v", err)
		r.setError()
		return false
	}
	logger.Infof("RTSP input setup OK [%s] medias=%d", r.id, len(desc.Medias))

	// Setup 后注册数据回调（OnPacketRTP 依赖已 setup 的 media）。
	if videoMedia != nil {
		medi := videoMedia
		ch := videoChannel
		client.OnPacketRTP(medi, format.Format(videoFmt), func(pkt *rtp.Packet) {
			r.handleVideoPacket(medi, videoFmt, pkt, ch)
		})
	}
	if audioMedia != nil && audioFmtMPEG4 != nil {
		medi := audioMedia
		af := audioFmtMPEG4
		ch := audioChannel
		client.OnPacketRTP(medi, format.Format(af), func(pkt *rtp.Packet) {
			r.handleAudioPacket(medi, af, pkt, ch)
		})
	}

	if _, err := client.Play(nil); err != nil {
		logger.Errorf("RTSP input play: %v", err)
		r.setError()
		return false
	}
	logger.Infof("RTSP input play OK [%s]", r.id)

	// Play 成功：状态置回 running（重连场景），随后等待会话结束
	r.mu.Lock()
	r.status = StreamStatusRunning
	r.mu.Unlock()

	// 等待 context 取消（Stop）或连接断开（远端关闭/超时/keepalive 失败）
	waitDone := make(chan error, 1)
	go func() { waitDone <- client.Wait() }()
	select {
	case <-ctx.Done():
		client.Close()
		return true
	case err := <-waitDone:
		logger.Warnf("RTSP input disconnected [%s]: %v", r.id, err)
		return false
	}
}

func (r *RTSPInput) handleVideoPacket(medi *description.Media, f *format.H264, pkt *rtp.Packet, channelID uint8) {
	r.mu.RLock()
	dec := r.decoders[channelID]
	handler := r.handler
	handlers := make([]FrameHandler, len(r.handlers))
	copy(handlers, r.handlers)
	r.mu.RUnlock()
	if dec == nil {
		return
	}
	if r.pktCount%50 == 0 {
		logger.Debugf("RTSP input packet [%s] ch=%d count=%d seq=%d ts=%d", r.id, channelID, r.pktCount, pkt.SequenceNumber, pkt.Timestamp)
	}
	r.pktCount++

	// 必须在 Decode 之前调用：timeDecoder 只在 PTSEqualsDTS 的包上初始化
	// （IDR/SPS/PPS，或 FU-A 起始分片）。大关键帧的 AU 在最后一个 FU-A 分片
	// 才组装完成，旧实现此时才取 PTS，起始分片已被跳过，timeDecoder 永不
	// 初始化 → 所有帧 PTS=0。提前到每个 RTP 包调用，IDR 起始分片即可完成
	// 初始化；初始化前的帧（中途接入的 GOP）丢弃，避免把 PTS=0 传给下游。
	pts, ptsOK := r.client.PacketPTS(medi, pkt)

	nalus, err := dec.Decode(pkt)
	if err != nil {
		if err != rtph264.ErrMorePacketsNeeded {
			logger.Debugf("RTSP input h264 decode: %v", err)
		}
		return
	}

	// 组装 access unit（AnnexB）
	data := make([]byte, 0, 64)
	isKey := false
	for _, nal := range nalus {
		if len(nal) > 0 {
			nt := nal[0] & 0x1F
			if nt == 5 {
				isKey = true
			}
		}
		data = appendAnnexBNAL(data, nal)
	}
	if len(data) == 0 {
		return
	}
	if !ptsOK {
		logger.Debugf("RTSP input drop frame before PTS init [%s] ch=%d ts=%d", r.id, channelID, pkt.Timestamp)
		return
	}

	frame := &Frame{
		Header: FrameHeader{
			Magic:     FrameMagic,
			Version:   FrameVersion,
			ChannelID: channelID,
			FrameType: FrameTypeVideo,
			Codec:     CodecH264,
			Flags:     frameFlags(isKey, false),
			PTS:       pts,
			DTS:       pts,
		},
		Payload: data,
	}

	if handler != nil {
		handler(frame)
	}
	for _, h := range handlers {
		if h != nil { // 已 remove
			h(frame)
		}
	}
}

// parseMPEG4GenericAUs 解析 AAC-hbr（MPEG4-GENERIC）RTP 负载的 AU 头区。
//
// 布局与主流客户端一致（ffmpeg libavformat/rtpdec_mpeg4.c 的 rtp_parse_mp4_au：
// AV_RB16 读 AU-headers-length、跳过 2 字节后从 bit16 起位打包读 Size/Index）。
// 返回各 AU 的字节尺寸与头区总字节数；畸形负载返回 ok=false。
func parseMPEG4GenericAUs(p []byte, sizeLength, indexLength int) (sizes []int, headerBytes int, ok bool) {
	bitsPerAUHeader := sizeLength + indexLength
	if bitsPerAUHeader <= 0 || len(p) < 2 {
		return nil, 0, false
	}
	auHeadersBits := int(p[0])<<8 | int(p[1]) // ffmpeg: AV_RB16
	if auHeadersBits == 0 || auHeadersBits%bitsPerAUHeader != 0 {
		return nil, 0, false
	}
	auCount := auHeadersBits / bitsPerAUHeader
	headerBytes = 2 + (auHeadersBits+7)/8 // 2 字节长度域 + AU-headers（补齐到字节）
	if len(p) < headerBytes {
		return nil, 0, false
	}

	sizes = make([]int, 0, auCount)
	bitOffset := 16 // 长度域占 2 字节，AU-header 从 bit16 起
	total := 0
	for i := 0; i < auCount; i++ {
		if bitOffset+bitsPerAUHeader > headerBytes*8 {
			return nil, 0, false
		}
		size := readBitsAt(p, bitOffset, sizeLength)
		bitOffset += bitsPerAUHeader
		if size <= 0 {
			return nil, 0, false
		}
		sizes = append(sizes, size)
		total += size
	}
	if headerBytes+total > len(p) {
		return nil, 0, false
	}
	return sizes, headerBytes, true
}

// handleAudioPacket 解包 MPEG4-GENERIC RTP 为 AAC 帧并分发。
//
// 帧格式：2 字节 AU-headers-length + 字节对齐的 AU-header 区 +
// payload（逐 AU 拼接），见 parseMPEG4GenericAUs。
// 本项目 RTSPSink 与常见 IPC 均为一包一 AU；此处支持一包多 AU。
func (r *RTSPInput) handleAudioPacket(medi *description.Media, f *format.MPEG4Audio, pkt *rtp.Packet, channelID uint8) {
	r.mu.RLock()
	handler := r.handler
	handlers := make([]FrameHandler, len(r.handlers))
	copy(handlers, r.handlers)
	r.mu.RUnlock()

	sizeLength := f.SizeLength
	if sizeLength <= 0 {
		sizeLength = 13
	}
	indexLength := f.IndexLength
	if indexLength <= 0 {
		indexLength = 3
	}
	p := pkt.Payload
	sizes, headerBytes, ok := parseMPEG4GenericAUs(p, sizeLength, indexLength)
	if !ok {
		return
	}

	// 逐 AU 提取 payload
	pts, _ := r.client.PacketPTS(medi, pkt)
	offset := headerBytes
	for _, size := range sizes {
		payload := make([]byte, size)
		copy(payload, p[offset:offset+size])
		offset += size

		frame := &Frame{
			Header: FrameHeader{
				Magic:     FrameMagic,
				Version:   FrameVersion,
				ChannelID: channelID,
				FrameType: FrameTypeAudio,
				Codec:     CodecAAC,
				PTS:       pts,
				DTS:       pts,
			},
			Payload: payload,
		}
		if handler != nil {
			handler(frame)
		}
		for _, h := range handlers {
			if h != nil { // 已 remove
				h(frame)
			}
		}
		// 同一包内多 AU 视为连续 AAC 帧（每帧 1024 采样）
		pts += aacSamplesPerFrame
	}
}

// readBitsAt 从 payload 的 bit 偏移处读取 n bit（MSB first）。
func readBitsAt(p []byte, bitOffset, n int) int {
	v := 0
	for i := 0; i < n; i++ {
		pos := bitOffset + i
		if pos/8 >= len(p) {
			return 0
		}
		v = v<<1 | int((p[pos/8]>>(7-pos%8))&1)
	}
	return v
}

func (r *RTSPInput) setError() {
	r.mu.Lock()
	r.status = StreamStatusError
	r.mu.Unlock()
}

func (r *RTSPInput) Stop() error {
	r.mu.Lock()
	if r.status == StreamStatusStopped {
		r.mu.Unlock()
		return nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	done := r.done
	r.mu.Unlock()

	if done != nil {
		<-done
	}

	r.mu.Lock()
	if r.client != nil {
		r.client.Close()
		r.client = nil
	}
	r.status = StreamStatusStopped
	r.mu.Unlock()
	logger.Infof("RTSP input stopped: %s", r.id)
	return nil
}

func (r *RTSPInput) Streams() ([]StreamInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.streams, nil
}

func (r *RTSPInput) Subscribe(_ context.Context, req *SubscribeRequest) (*SubscribeResponse, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return &SubscribeResponse{Streams: r.streams}, nil
}

func (r *RTSPInput) Unsubscribe(_ context.Context, channels []uint8) error {
	return nil
}

func (r *RTSPInput) Signal(ctx context.Context, sig *Signal) (*Signal, error) {
	switch sig.Type {
	case SignalStart, SignalResume, SignalPause, SignalStop, SignalSeek:
		return NewReply(sig, nil)
	case SignalGetStreamInfo:
		return NewReply(sig, &SubscribeResponse{Streams: r.streams})
	default:
		return NewReply(sig, nil)
	}
}

func (r *RTSPInput) SetFrameHandler(h FrameHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handler = h
}

func (r *RTSPInput) AddFrameHandler(h FrameHandler) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = append(r.handlers, h)
	idx := len(r.handlers) - 1
	// FrameHandler 不可比较：按下标移除（置 nil，分发时跳过）
	removed := false // 仅在 r.mu 写锁下访问
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if !removed {
			removed = true
			r.handlers[idx] = nil
		}
	}
}
