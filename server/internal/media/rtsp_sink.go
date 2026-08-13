package media

import (
	"context"
	"fmt"
	"sync"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph265"
	"github.com/pion/rtp"

	"github.com/x-media/x-media-server/pkg/logger"
)

// gopFrame 单个 GOP 帧缓存（供新 reader 重放完整 GOP）。
type gopFrame struct {
	payload []byte
	pts     int64
}

// RTSPSink 将标准帧封装为 RTP 并在本地 RTSP server 上提供拉流（mode=server）。
//
// Configure 阶段用 StreamInfo（含 CodecConfig/SPS/PPS/AAC config/ClockRate）建立
// gortsplib ServerStream；WriteFrame 做 RTP 封装并广播给所有 RTSP 读者。
type RTSPSink struct {
	mu sync.RWMutex

	encodeMu sync.Mutex // 串行化 rtph264.Encoder 调用（重放与正常流并发时防竞态）

	id     string
	addr   string
	status StreamStatus

	handler *RTSPServerHandler
	stream  *gortsplib.ServerStream
	h264Enc *rtph264.Encoder
	h265Enc *rtph265.Encoder
	h265    bool         // 当前视频是否为 HEVC
	vMedia  *description.Media
	aMedia  *description.Media
	videoClockRate int
	audioClockRate int
	audioSampleRate int
	ready          bool
	videoFrameCount int

	sps           []byte     // 缓存的 SPS（供新 reader PLAY 时初始化）
	pps           []byte
	vps           []byte     // HEVC VPS（h265 时使用）
	lastVideoPTS  int64      // 最近视频帧 PTS（供参数集 RTP 时间戳）
	lastKeyframe  []byte     // 最近完整关键帧（含 SPS/PPS/IDR），新 reader 重放
	lastKeyframePTS int64    // 关键帧原始 PTS
	gopFrames     []gopFrame // 最近一个完整 GOP 的帧缓存（从关键帧开始），供新 reader 重放
}

func NewRTSPSink(config *OutputConfig) (*RTSPSink, error) {
	if config.Mode == "" || config.Mode != "server" {
		return nil, ErrInvalidConfig
	}
	id := config.ID
	if id == "" {
		id = "rtsp_" + config.Addr
	}
	return &RTSPSink{
		id:     id,
		addr:   config.Addr,
		status: StreamStatusStopped,
	}, nil
}

func (r *RTSPSink) ID() string { return r.id }

func (r *RTSPSink) Status() StreamStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *RTSPSink) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusRunning {
		return nil
	}
	h, err := globalRTSPManager.GetOrCreate(r.addr)
	if err != nil {
		r.status = StreamStatusError
		return fmt.Errorf("start RTSP server: %w", err)
	}
	r.handler = h
	r.status = StreamStatusRunning
	logger.Infof("RTSP sink started: %s, addr: %s", r.id, r.addr)
	return nil
}

// Configure 用 StreamInfo 建立 SDP 与 ServerStream。
func (r *RTSPSink) Configure(streams []StreamInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configureLocked(streams)
}

func (r *RTSPSink) configureLocked(streams []StreamInfo) error {
	var medias []*description.Media

	for _, s := range streams {
		switch s.Kind {
		case "video":
			switch s.CodecID {
			case CodecH265:
				vps, sps, pps := splitCodecConfigHevc(s.CodecConfig)
				if len(vps) == 0 || len(sps) == 0 || len(pps) == 0 {
					return fmt.Errorf("video stream %d missing VPS/SPS/PPS in HEVC codec config", s.ChannelID)
				}
				vMedia := &description.Media{
					Type: description.MediaTypeVideo,
					Formats: []format.Format{
						&format.H265{
							PayloadTyp: 96,
							VPS:        vps,
							SPS:        sps,
							PPS:        pps,
						},
					},
				}
				medias = append(medias, vMedia)
				r.vMedia = vMedia
				r.videoClockRate = s.ClockRate
				if r.videoClockRate <= 0 {
					r.videoClockRate = 90000
				}
				r.h265 = true
				if err := r.initEncoder(); err != nil {
					return err
				}
				r.vps = vps
				r.sps = sps
				r.pps = pps
			default: // H.264 及默认
				sps, pps := splitCodecConfigVideo(s.CodecConfig)
				if len(sps) == 0 || len(pps) == 0 {
					return fmt.Errorf("video stream %d missing SPS/PPS in codec config", s.ChannelID)
				}
				vMedia := &description.Media{
					Type: description.MediaTypeVideo,
					Formats: []format.Format{
						&format.H264{
							PayloadTyp:        96,
							PacketizationMode: 1,
							SPS:               sps,
							PPS:               pps,
						},
					},
				}
				medias = append(medias, vMedia)
				r.vMedia = vMedia
				r.videoClockRate = s.ClockRate
				if r.videoClockRate <= 0 {
					r.videoClockRate = 90000
				}
				r.h265 = false
				if err := r.initEncoder(); err != nil {
					return err
				}
				r.vps = nil
				r.sps = sps
				r.pps = pps
			}
		case "audio":
			aMedia, clockRate, sampleRate, err := buildAudioMedia(s)
			if err != nil {
				logger.Warnf("RTSP sink audio config skipped: %v", err)
				continue
			}
			medias = append(medias, aMedia)
			r.aMedia = aMedia
			r.audioClockRate = clockRate
			r.audioSampleRate = sampleRate
		}
	}

	if len(medias) == 0 {
		return fmt.Errorf("no usable media in stream info")
	}

	desc := &description.Session{Medias: medias}
	stream := &gortsplib.ServerStream{
		Server: r.handler.server,
		Desc:   desc,
	}
	if err := stream.Initialize(); err != nil {
		return fmt.Errorf("init RTSP stream: %w", err)
	}

	// 替换旧流
	if r.stream != nil {
		r.stream.Close()
	}
	r.stream = stream

	path := "live/" + r.id
	r.handler.mutex.Lock()
	r.handler.paths[path] = &rtspPath{stream: stream}
	r.handler.mutex.Unlock()

	// 新 reader PLAY 后异步重放完整 GOP，保证其探测窗口内拿到从关键帧开始的完整数据。
	r.handler.SetOnReaderPlay(func(s *gortsplib.ServerStream) {
		r.sendParamsToStream(s)
	})

	r.ready = true
	logger.Infof("RTSP sink configured: %s, path: %s, medias: %d", r.id, path, len(medias))
	return nil
}

// sendParamsToStream 向指定 stream 发送 SPS/PPS 参数集。
// 参数集 RTP 包极小，不会污染解码流；ffmpeg/VLC 收到后能初始化解码器，
// 后续等下一个自然关键帧（≤GOP 间隔）即可完整解码。
// 在新 reader PLAY 后调用。
func (r *RTSPSink) sendParamsToStream(stream *gortsplib.ServerStream) {
	if stream == nil {
		return
	}
	r.mu.RLock()
	h265 := r.h265
	var enc any
	if h265 {
		enc = r.h265Enc
	} else {
		enc = r.h264Enc
	}
	vMedia := r.vMedia
	clock := r.videoClockRate
	sps := r.sps
	pps := r.pps
	vps := r.vps
	basePTS := r.lastVideoPTS
	r.mu.RUnlock()

	if enc == nil || vMedia == nil {
		return
	}
	if clock <= 0 {
		clock = 90000
	}

	// 参数集使用最近视频帧 PTS 作为时间戳基准（仅初始化用途，实际帧 PTS 由正常流提供）。
	pts := uint32(To90k(basePTS, clock))
	r.encodeMu.Lock()
	defer r.encodeMu.Unlock()

	var params [][]byte
	if h265 {
		if len(vps) == 0 || len(sps) == 0 || len(pps) == 0 {
			return
		}
		params = [][]byte{vps, sps, pps}
	} else {
		if len(sps) == 0 || len(pps) == 0 {
			return
		}
		params = [][]byte{sps, pps}
	}

	var pkts []*rtp.Packet
	var err error
	if h265 {
		pkts, err = enc.(*rtph265.Encoder).Encode(params)
	} else {
		pkts, err = enc.(*rtph264.Encoder).Encode(params)
	}
	if err != nil {
		logger.Warnf("RTSP sink send params encode: %v", err)
		return
	}
	for _, p := range pkts {
		p.Timestamp = pts
		stream.WritePacketRTP(vMedia, p)
	}
	logger.Infof("RTSP sink sent params to new reader [%s] h265=%v pts=%d", r.id, h265, pts)
}

func (r *RTSPSink) initEncoder() error {
	if r.h265 {
		if r.h265Enc != nil {
			return nil
		}
		enc := &rtph265.Encoder{PayloadType: 96}
		if err := enc.Init(); err != nil {
			return fmt.Errorf("init H265 encoder: %w", err)
		}
		r.h265Enc = enc
		return nil
	}
	if r.h264Enc != nil {
		return nil
	}
	enc := &rtph264.Encoder{
		PayloadType:        96,
		PacketizationMode: 1,
	}
	if err := enc.Init(); err != nil {
		return fmt.Errorf("init H264 encoder: %w", err)
	}
	r.h264Enc = enc
	return nil
}

// WriteFrame 将标准帧封装为 RTP 并广播。
func (r *RTSPSink) WriteFrame(f *Frame) error {
	r.mu.RLock()
	status := r.status
	ready := r.ready
	stream := r.stream
	r.mu.RUnlock()

	if status != StreamStatusRunning || !ready || stream == nil {
		return nil
	}

	r.videoFrameCount++
	if r.videoFrameCount%300 == 0 {
		logger.Infof("RTSP sink WriteFrame #%d type=%d pts=%d size=%d", r.videoFrameCount, f.Header.FrameType, f.Header.PTS, len(f.Payload))
	}

	switch f.Header.FrameType {
	case FrameTypeVideo:
		return r.writeVideo(f, stream)
	case FrameTypeAudio:
		return r.writeAudio(f, stream)
	}
	return nil
}

func (r *RTSPSink) writeVideo(f *Frame, stream *gortsplib.ServerStream) error {
	r.mu.RLock()
	h265 := r.h265
	var enc any
	if h265 {
		enc = r.h265Enc
	} else {
		enc = r.h264Enc
	}
	vMedia := r.vMedia
	clock := r.videoClockRate
	r.mu.RUnlock()

	if enc == nil || vMedia == nil {
		return nil
	}

	nalUnits := splitAnnexB(f.Payload)
	if len(nalUnits) == 0 {
		return nil
	}

	// 累积 GOP 缓存：关键帧开启新 GOP，其余帧追加。供新 reader 重放完整 GOP。
	r.mu.Lock()
	r.lastVideoPTS = f.Header.PTS
	if f.Header.Flags&FlagKeyframe != 0 {
		r.gopFrames = []gopFrame{{payload: f.Payload, pts: f.Header.PTS}}
		r.lastKeyframe = f.Payload
		r.lastKeyframePTS = f.Header.PTS
	} else if len(r.gopFrames) > 0 {
		// 限制 GOP 缓存大小（约 2.5s GOP，几十帧）
		if len(r.gopFrames) < 300 {
			r.gopFrames = append(r.gopFrames, gopFrame{payload: f.Payload, pts: f.Header.PTS})
		}
	}
	r.mu.Unlock()

	pts := uint32(To90k(f.Header.PTS, clock))
	r.encodeMu.Lock()
	var pkts []*rtp.Packet
	var err error
	if h265 {
		pkts, err = enc.(*rtph265.Encoder).Encode(nalUnits)
	} else {
		pkts, err = enc.(*rtph264.Encoder).Encode(nalUnits)
	}
	r.encodeMu.Unlock()
	if err != nil {
		logger.Errorf("RTSP sink H264 encode: %v", err)
		return nil
	}
	for _, p := range pkts {
		p.Timestamp = pts
		stream.WritePacketRTP(vMedia, p)
	}
	return nil
}


func (r *RTSPSink) writeAudio(f *Frame, stream *gortsplib.ServerStream) error {
	r.mu.RLock()
	aMedia := r.aMedia
	clock := r.audioClockRate
	sampleRate := r.audioSampleRate
	r.mu.RUnlock()

	if aMedia == nil {
		return nil
	}
	if sampleRate <= 0 {
		sampleRate = clock
	}
	if sampleRate <= 0 {
		return nil
	}

	pts := uint32(ConvertClock(f.Header.PTS, clock, sampleRate))

	auSize := len(f.Payload)
	auHeader := uint16(auSize) << 3

	payload := make([]byte, 4+auSize)
	payload[0] = 0
	payload[1] = 16 // AU-headers-length (bits)
	payload[2] = byte(auHeader >> 8)
	payload[3] = byte(auHeader)
	copy(payload[4:], f.Payload)

	rtpPkt := &rtp.Packet{
		Header: rtp.Header{
			Version:   2,
			PayloadType: 97,
			Timestamp: pts,
			SSRC:      0x12345678,
		},
		Payload: payload,
	}
	stream.WritePacketRTP(aMedia, rtpPkt)
	return nil
}

func (r *RTSPSink) Notify(sig *Signal) error {
	return nil
}

func (r *RTSPSink) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusStopped {
		return nil
	}
	if r.stream != nil {
		r.stream.Close()
		r.stream = nil
	}
	r.ready = false
	r.status = StreamStatusStopped
	logger.Infof("RTSP sink stopped: %s", r.id)
	return nil
}

// splitCodecConfigVideo 从 CodecConfig(AnnexB) 分离 SPS/PPS。
func splitCodecConfigVideo(config []byte) (sps, pps []byte) {
	nalUnits := splitAnnexB(config)
	for _, nal := range nalUnits {
		if len(nal) == 0 {
			continue
		}
		nalType := nal[0] & 0x1F
		switch nalType {
		case 7:
			sps = nal
		case 8:
			pps = nal
		}
	}
	return sps, pps
}

// buildAudioMedia 从音频 StreamInfo 构造 gortsplib AAC media。
func buildAudioMedia(s StreamInfo) (*description.Media, int, int, error) {
	config := s.CodecConfig
	if len(config) < 2 {
		return nil, 0, 0, fmt.Errorf("missing AAC config")
	}
	audioObjectType := int(config[0] >> 3)
	sampleRateIndex := int((config[0]&0x07)<<1 | config[1]>>7)
	channelConfig := int((config[1] >> 3) & 0x0F)

	sampleRates := []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
	sampleRate := 44100
	if sampleRateIndex < len(sampleRates) {
		sampleRate = sampleRates[sampleRateIndex]
	}
	clockRate := s.ClockRate
	if clockRate <= 0 {
		clockRate = sampleRate
	}

	aMedia := &description.Media{
		Type: description.MediaTypeAudio,
		Formats: []format.Format{
			&format.Generic{
				PayloadTyp: 97,
				RTPMa:      fmt.Sprintf("MPEG4-GENERIC/%d/%d", sampleRate, channelConfig),
				ClockRat:   sampleRate,
				FMT: map[string]string{
					"streamtype":       "5",
					"profile-level-id": fmt.Sprintf("%d", audioObjectType),
					"mode":             "AAC-hbr",
					"sizelength":       "13",
					"indexlength":      "3",
					"indexdeltalength": "3",
					"config":           fmt.Sprintf("%02x%02x", config[0], config[1]),
				},
			},
		},
	}
	return aMedia, clockRate, sampleRate, nil
}
