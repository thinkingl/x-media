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
		id:        id,
		url:       config.URL,
		status:    StreamStatusStopped,
		decoders:  make(map[uint8]*rtph264.Decoder),
		transport: config.Transport,
		udpBuf:    config.UDPReadBufferSize,
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

	u, err := base.ParseURL(r.url)
	if err != nil {
		r.status = StreamStatusError
		r.mu.Unlock()
		return fmt.Errorf("parse rtsp url: %w", err)
	}

	// 预检 TCP 连通性，避免 Describe 无超时挂起。
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		r.status = StreamStatusError
		r.mu.Unlock()
		return fmt.Errorf("rtsp connect %s: %w", u.Host, err)
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
		r.status = StreamStatusError
		r.mu.Unlock()
		return fmt.Errorf("rtsp client start: %w", err)
	}

	desc, _, err := client.Describe(u)
	if err != nil {
		r.status = StreamStatusError
		r.mu.Unlock()
		return fmt.Errorf("rtsp describe: %w", err)
	}

	streams, err := buildStreamsFromDescription(desc)
	if err != nil {
		r.status = StreamStatusError
		r.mu.Unlock()
		return fmt.Errorf("rtsp media unsupported: %w", err)
	}

	ctx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})
	r.client = client
	r.streams = streams
	r.status = StreamStatusRunning
	r.mu.Unlock()

	go r.readLoop(ctx, client, desc, streams, u)

	logger.Infof("RTSP input started: %s, url: %s, streams: %d", r.id, r.url, len(streams))
	return nil
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

// readLoop 拉流并解包。
func (r *RTSPInput) readLoop(ctx context.Context, client *gortsplib.Client, desc *description.Session, streams []StreamInfo, u *base.URL) {
	defer close(r.done)

	channelID := uint8(0)
	videoChannel := uint8(0)
	var videoMedia *description.Media
	var videoFmt *format.H264
	var audioMedia *description.Media
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
					return
				}
				r.mu.Lock()
				r.decoders[channelID] = dec
				r.mu.Unlock()
			case *format.Generic:
				audioMedia = medi
				audioChannel = channelID
			}
			channelID++
		}
	}

	logger.Infof("RTSP input before SetupAll [%s] medias=%d baseURL=%s", r.id, len(desc.Medias), desc.BaseURL.String())
	if err := client.SetupAll(desc.BaseURL, desc.Medias); err != nil {
		logger.Errorf("RTSP input setup: %v", err)
		r.setError()
		return
	}
	logger.Infof("RTSP input setup OK [%s] medias=%d", r.id, len(desc.Medias))

	// Setup 后注册数据回调（OnPacketRTP 依赖已 setup 的 media）。
	if videoMedia != nil {
		medi := videoMedia
		ch := videoChannel
		client.OnPacketRTP(medi, format.Format(videoFmt), func(pkt *rtp.Packet) {
			r.handleVideoPacket(medi, videoFmt, pkt, ch)
		})
		_ = audioMedia
		_ = audioChannel
	}

	if _, err := client.Play(nil); err != nil {
		logger.Errorf("RTSP input play: %v", err)
		r.setError()
		return
	}
	logger.Infof("RTSP input play OK [%s]", r.id)

	// 阻塞直到 context 取消
	<-ctx.Done()
	client.Close()
}

func (r *RTSPInput) handleVideoPacket(medi *description.Media, f *format.H264, pkt *rtp.Packet, channelID uint8) {
	r.mu.RLock()
	dec := r.decoders[channelID]
	handler := r.handler
	handlers := r.handlers
	r.mu.RUnlock()
	if dec == nil {
		return
	}
	if r.pktCount%50 == 0 {
		logger.Infof("RTSP input packet [%s] ch=%d count=%d seq=%d ts=%d", r.id, channelID, r.pktCount, pkt.SequenceNumber, pkt.Timestamp)
	}
	r.pktCount++

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

	pts, _ := r.client.PacketPTS(medi, pkt)
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
		h(frame)
	}
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

func (r *RTSPInput) AddFrameHandler(h FrameHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = append(r.handlers, h)
}
