package media

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/pion/rtp"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

type RTSPOutput struct {
	mu           sync.RWMutex
	id           string
	config       *OutputConfig
	status       StreamStatus
	cancel       context.CancelFunc
	ctx          context.Context
	stream       *gortsplib.ServerStream
	h264Enc      *rtph264.Encoder
	videoMedia   *description.Media
	audioMedia   *description.Media
	startTime    time.Time
	sps          []byte
	pps          []byte
	audioConfig  []byte
	streamReady  bool
	rtspHandler  *RTSPServerHandler
}

func NewRTSPOutput(config *OutputConfig) (*RTSPOutput, error) {
	if config.Mode == "" {
		return nil, ErrInvalidConfig
	}
	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}

	return &RTSPOutput{
		id:     id,
		config: config,
		status: StreamStatusStopped,
	}, nil
}

func (r *RTSPOutput) ID() string           { return r.id }
func (r *RTSPOutput) Status() StreamStatus { r.mu.RLock(); defer r.mu.RUnlock(); return r.status }

func (r *RTSPOutput) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusRunning {
		return nil
	}
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.status = StreamStatusRunning
	r.startTime = time.Now()

	if r.config.Mode == "server" {
		h, err := globalRTSPManager.GetOrCreate(r.config.Addr)
		if err != nil {
			r.status = StreamStatusStopped
			return fmt.Errorf("failed to start RTSP server: %w", err)
		}
		r.rtspHandler = h
	}

	logger.Infof("RTSP output started: %s, mode: %s", r.id, r.config.Mode)
	return nil
}

func (r *RTSPOutput) StartWithFile(ctx context.Context, filePath string) error {
	return r.Start(ctx)
}

func (r *RTSPOutput) initStream() error {
	medias := []*description.Media{}

	videoMedia := &description.Media{
		Type: description.MediaTypeVideo,
		Formats: []format.Format{
			&format.H264{
				PayloadTyp:        96,
				PacketizationMode: 1,
				SPS:               r.sps,
				PPS:               r.pps,
			},
		},
	}
	medias = append(medias, videoMedia)

	var audioMedia *description.Media
	if len(r.audioConfig) >= 2 {
		audioObjectType := int(r.audioConfig[0] >> 3)
		sampleRateIndex := int((r.audioConfig[0] & 0x07) << 1 | r.audioConfig[1] >> 7)
		channelConfig := int((r.audioConfig[1] >> 3) & 0x0F)

		sampleRates := []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
		sampleRate := 44100
		if sampleRateIndex < len(sampleRates) {
			sampleRate = sampleRates[sampleRateIndex]
		}

		configHex := fmt.Sprintf("%02x%02x", r.audioConfig[0], r.audioConfig[1])

		audioMedia = &description.Media{
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
						"config":           configHex,
					},
				},
			},
		}
		medias = append(medias, audioMedia)
	}

	desc := &description.Session{
		Medias: medias,
	}

	stream := &gortsplib.ServerStream{
		Server: r.rtspHandler.server,
		Desc:   desc,
	}
	if err := stream.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize stream: %w", err)
	}

	r.stream = stream
	r.videoMedia = videoMedia
	r.audioMedia = audioMedia
	r.streamReady = true

	streamPath := "live/" + r.id
	r.rtspHandler.mutex.Lock()
	r.rtspHandler.paths[streamPath] = &rtspPath{
		stream: stream,
	}
	r.rtspHandler.mutex.Unlock()

	logger.Infof("RTSP stream initialized: %s, path: %s, video SPS:%d PPS:%d, audio:%v", r.id, streamPath, len(r.sps), len(r.pps), audioMedia != nil)
	return nil
}

func (r *RTSPOutput) WritePacket(pkt *MediaPacket) error {
	r.mu.RLock()
	status := r.status
	r.mu.RUnlock()

	if status != StreamStatusRunning {
		return nil
	}

	if pkt.IsAudio {
		return r.writeAudio(pkt)
	}

	if !pkt.IsVideo {
		return nil
	}

	return r.writeVideo(pkt)
}

func (r *RTSPOutput) writeVideo(pkt *MediaPacket) error {
	nalUnits := splitAnnexB(pkt.Data)
	if len(nalUnits) == 0 {
		return nil
	}

	for _, nal := range nalUnits {
		if len(nal) < 1 {
			continue
		}
		nalType := nal[0] & 0x1F
		switch nalType {
		case 7:
			r.mu.Lock()
			r.sps = nal
			r.mu.Unlock()
			logger.Infof("RTSP output got SPS: %d bytes", len(nal))
		case 8:
			r.mu.Lock()
			r.pps = nal
			r.mu.Unlock()
			logger.Infof("RTSP output got PPS: %d bytes", len(nal))
		}
	}

	r.mu.Lock()
	if !r.streamReady && r.sps != nil && r.pps != nil {
		if err := r.initStream(); err != nil {
			r.mu.Unlock()
			logger.Errorf("failed to init stream: %v", err)
			return nil
		}
	}
	stream := r.stream
	videoMedia := r.videoMedia
	enc := r.h264Enc
	ready := r.streamReady
	r.mu.Unlock()

	if !ready || stream == nil {
		return nil
	}

	if enc == nil {
		r.mu.Lock()
		r.h264Enc = &rtph264.Encoder{
			PayloadType:        96,
			PacketizationMode: 1,
		}
		if err := r.h264Enc.Init(); err != nil {
			r.mu.Unlock()
			return fmt.Errorf("failed to init H264 encoder: %w", err)
		}
		enc = r.h264Enc
		r.mu.Unlock()
	}

	elapsed := time.Since(r.startTime)
	pts := uint32(elapsed.Seconds() * 90000)

	pkts, err := enc.Encode(nalUnits)
	if err != nil {
		logger.Errorf("failed to encode H264: %v", err)
		return nil
	}

	for _, p := range pkts {
		p.Timestamp = pts
		stream.WritePacketRTP(videoMedia, p)
	}

	return nil
}

func (r *RTSPOutput) writeAudio(pkt *MediaPacket) error {
	r.mu.Lock()
	if !r.streamReady && r.sps != nil && r.pps != nil {
		if err := r.initStream(); err != nil {
			r.mu.Unlock()
			logger.Errorf("failed to init stream: %v", err)
			return nil
		}
	}

	if len(r.audioConfig) == 0 && len(pkt.CodecConfig) > 0 {
		r.audioConfig = pkt.CodecConfig
		logger.Infof("RTSP output got audio config: %02x%02x", pkt.CodecConfig[0], pkt.CodecConfig[1])

		if r.streamReady && r.audioMedia == nil {
			logger.Infof("RTSP stream reinitializing with audio")
			if r.stream != nil {
				r.stream.Close()
			}
			r.streamReady = false
			if err := r.initStream(); err != nil {
				r.mu.Unlock()
				logger.Errorf("failed to reinit stream with audio: %v", err)
				return nil
			}
		}
		r.mu.Unlock()
		return nil
	}

	stream := r.stream
	audioMedia := r.audioMedia
	ready := r.streamReady
	r.mu.Unlock()

	if !ready || stream == nil || audioMedia == nil {
		return nil
	}

	elapsed := time.Since(r.startTime)
	pts := uint32(elapsed.Seconds() * 44100)

	auSize := len(pkt.Data)
	headerLen := uint16(1)
	auHeader := uint16(auSize) << 3

	payload := make([]byte, 2+2+len(pkt.Data))
	payload[0] = byte(headerLen >> 8)
	payload[1] = byte(headerLen)
	payload[2] = byte(auHeader >> 8)
	payload[3] = byte(auHeader)
	copy(payload[4:], pkt.Data)

	rtpPkt := &rtp.Packet{
		Header: rtp.Header{
			Version:     2,
			PayloadType: 97,
			Timestamp:   pts,
			SSRC:        0x12345678,
		},
		Payload: payload,
	}
	stream.WritePacketRTP(audioMedia, rtpPkt)

	return nil
}

func (r *RTSPOutput) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == StreamStatusStopped {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
	}

	if r.stream != nil {
		r.stream.Close()
		r.stream = nil
	}

	r.status = StreamStatusStopped
	logger.Infof("RTSP output stopped: %s", r.id)
	return nil
}

func splitAnnexB(data []byte) [][]byte {
	var nalUnits [][]byte
	var currentNAL []byte
	i := 0

	for i < len(data) {
		if i+2 < len(data) && data[i] == 0x00 && data[i+1] == 0x00 {
			scLen := 0
			if i+3 < len(data) && data[i+2] == 0x00 && data[i+3] == 0x01 {
				scLen = 4
			} else if data[i+2] == 0x01 {
				scLen = 3
			}

			if scLen > 0 {
				if len(currentNAL) > 0 {
					nalUnits = append(nalUnits, currentNAL)
				}
				currentNAL = []byte{}
				i += scLen
				continue
			}
		}

		currentNAL = append(currentNAL, data[i])
		i++
	}

	if len(currentNAL) > 0 {
		nalUnits = append(nalUnits, currentNAL)
	}

	return nalUnits
}
