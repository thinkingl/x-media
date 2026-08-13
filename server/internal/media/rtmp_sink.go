package media

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
)

// RTMPSink 将标准帧封装为 FLV 数据并经原生 RTMP 协议推流。
//
// 首次收到帧时建立连接并发送 onMetaData + sequence header，之后按帧发送
// audio/video 消息体（FLV tag data 部分）。
type RTMPSink struct {
	mu sync.RWMutex

	id      string
	url     string
	addr    string
	status  StreamStatus

	client *rtmpClient
	writer *FLVWriter
	ready  bool
	started bool

	streams []StreamInfo
	metaSent bool
	seqSent  bool
}

// NewRTMPSink 创建 RTMP sink。config.URL 为 rtmp://host[:port]/app/stream。
func NewRTMPSink(config *OutputConfig) (*RTMPSink, error) {
	if config.URL == "" {
		return nil, ErrInvalidConfig
	}
	id := config.ID
	if id == "" {
		id = "rtmp_" + config.URL
	}
	c, err := newRTMPClient(config.URL)
	if err != nil {
		return nil, err
	}
	addr := rtmpHostAddr(config.URL)
	return &RTMPSink{
		id:     id,
		url:    config.URL,
		addr:   addr,
		status: StreamStatusStopped,
		client: c,
		writer: NewFLVWriter(),
	}, nil
}

func (r *RTMPSink) ID() string { return r.id }

func (r *RTMPSink) Status() StreamStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *RTMPSink) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusRunning {
		return nil
	}
	r.status = StreamStatusRunning
	r.ready = true
	logger.Infof("RTMP sink started: %s, url: %s", r.id, r.url)
	return nil
}

// Configure 记录媒体信息与时钟率。
func (r *RTMPSink) Configure(streams []StreamInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streams = streams
	for _, s := range streams {
		if s.ClockRate > 0 {
			r.writer.SetClockRate(s.ChannelID, s.ClockRate)
		}
	}
	return nil
}

// WriteFrame 首次调用时连接 RTMP server 并发 metadata + sequence header。
func (r *RTMPSink) WriteFrame(f *Frame) error {
	r.mu.RLock()
	status := r.status
	ready := r.ready
	r.mu.RUnlock()

	if status != StreamStatusRunning || !ready {
		return nil
	}

	// 懒连接
	r.mu.Lock()
	if !r.started {
		if err := r.client.Connect(r.addr); err != nil {
			r.mu.Unlock()
			r.status = StreamStatusError
			return err
		}
		r.started = true
	}
	r.mu.Unlock()

	// 发送 metadata + sequence headers（首次）
	if !r.metaSent {
		r.sendMetadata()
		r.sendSequenceHeaders()
	}

	tagType, ts, data := r.writer.EncodeTagData(f)
	if len(data) == 0 {
		return nil
	}
	return r.client.SendData(tagType, ts, data)
}

// sendMetadata 发送 onMetaData。
func (r *RTMPSink) sendMetadata() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	meta := map[string]any{
		"width":    0.0,
		"height":   0.0,
		"framerate": 0.0,
		"videocodecid": float64(7),
		"audiocodecid": float64(10),
	}
	for _, s := range r.streams {
		if s.Kind == "video" {
			if w, ok := s.Parameters["width"].(int); ok {
				meta["width"] = float64(w)
			}
			if h, ok := s.Parameters["height"].(int); ok {
				meta["height"] = float64(h)
			}
			if fps, ok := s.Parameters["fps"].(float64); ok {
				meta["framerate"] = fps
			}
		}
	}
	meta["videocodecid"] = float64(7)
	meta["audiocodecid"] = float64(10)

	payload := make([]byte, 0)
	payload = append(payload, amf0String(cmdOnMetaData)...)
	b, err := encodeAMF0(meta)
	if err != nil {
		logger.Errorf("RTMP metadata encode: %v", err)
		return
	}
	payload = append(payload, b...)

	if err := r.client.SendData(msgTypeAMF0Data, 0, payload); err != nil {
		logger.Errorf("RTMP metadata send: %v", err)
		return
	}
	r.metaSent = true
}

// sendSequenceHeaders 发送音视频 sequence header。
func (r *RTMPSink) sendSequenceHeaders() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, s := range r.streams {
		if s.CodecConfig == nil {
			continue
		}
		switch s.Kind {
		case "video":
			sps, pps := splitCodecConfigVideo(s.CodecConfig)
			avcC := buildAVCCRecord(sps, pps)
			data := make([]byte, 0, 5+len(avcC))
			data = append(data, 0x17) // keyframe | H264
			data = append(data, flvAVCPacketTypeSeq)
			data = append(data, 0x00, 0x00, 0x00)
			data = append(data, avcC...)
			if err := r.client.SendData(msgTypeVideo, 0, data); err != nil {
				logger.Errorf("RTMP video seq header: %v", err)
			}
		case "audio":
			data := make([]byte, 0, 2+len(s.CodecConfig))
			data = append(data, 0xAF)
			data = append(data, flvAACPacketTypeSeq)
			data = append(data, s.CodecConfig...)
			if err := r.client.SendData(msgTypeAudio, 0, data); err != nil {
				logger.Errorf("RTMP audio seq header: %v", err)
			}
		}
	}
	r.seqSent = true
}

func (r *RTMPSink) Notify(sig *Signal) error {
	return nil
}

func (r *RTMPSink) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusStopped {
		return nil
	}
	r.ready = false
	r.status = StreamStatusStopped
	if r.client != nil {
		_ = r.client.Close()
	}
	logger.Infof("RTMP sink stopped: %s", r.id)
	return nil
}

// buildAVCCRecord 从 SPS/PPS 构造 AVCDecoderConfigurationRecord。
func buildAVCCRecord(sps, pps []byte) []byte {
	avcC := make([]byte, 0, 32)
	avcC = append(avcC, 0x01, 0x64, 0x00, 0x1f, 0xff)
	avcC = append(avcC, 0xe1)
	avcC = append(avcC, byte(len(sps)>>8), byte(len(sps)))
	avcC = append(avcC, sps...)
	avcC = append(avcC, 0x01)
	avcC = append(avcC, byte(len(pps)>>8), byte(len(pps)))
	avcC = append(avcC, pps...)
	return avcC
}

func rtmpHostAddr(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if u.Host == "" {
		return ""
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":1935"
	}
	return host
}
