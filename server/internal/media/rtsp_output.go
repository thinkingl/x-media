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
	startTime    time.Time
	sps          []byte
	pps          []byte
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

	desc := &description.Session{
		Medias: []*description.Media{videoMedia},
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
	r.streamReady = true

	r.rtspHandler.mutex.Lock()
	r.rtspHandler.paths[r.id] = &rtspPath{
		stream: stream,
	}
	r.rtspHandler.mutex.Unlock()

	logger.Infof("RTSP stream initialized: %s, SPS: %d bytes, PPS: %d bytes", r.id, len(r.sps), len(r.pps))
	return nil
}

func (r *RTSPOutput) WritePacket(pkt *MediaPacket) error {
	r.mu.RLock()
	status := r.status
	r.mu.RUnlock()

	if status != StreamStatusRunning {
		return nil
	}

	if !pkt.IsVideo {
		return nil
	}

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
