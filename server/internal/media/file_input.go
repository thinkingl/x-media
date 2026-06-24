package media

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/x-media/x-media-server/pkg/logger"
	"github.com/x-media/x-media-server/pkg/utils"
)

type FileInput struct {
	mu      sync.RWMutex
	id      string
	config  *InputConfig
	status  StreamStatus
	handler PacketHandler
	cancel  context.CancelFunc
	ctx     context.Context
	streams []StreamInfo
	cmd     *exec.Cmd
}

func NewFileInput(config *InputConfig) (*FileInput, error) {
	if config.Path == "" {
		return nil, ErrInvalidConfig
	}
	id := config.ID
	if id == "" {
		id = utils.GenerateID()
	}
	return &FileInput{
		id:     id,
		config: config,
		status: StreamStatusStopped,
	}, nil
}

func (f *FileInput) ID() string           { return f.id }
func (f *FileInput) Status() StreamStatus { f.mu.RLock(); defer f.mu.RUnlock(); return f.status }

func (f *FileInput) GetStreams() []StreamInfo {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.streams
}

func (f *FileInput) Start(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.status == StreamStatusRunning {
		return nil
	}

	filePath := f.config.Path
	if !filepath.IsAbs(filePath) {
		abs, err := filepath.Abs(filePath)
		if err == nil {
			filePath = abs
		}
	}

	streams, err := ProbeFileStreams(filePath)
	if err != nil {
		logger.Errorf("failed to probe file %s: %v", filePath, err)
		f.status = StreamStatusError
		return err
	}

	f.streams = streams
	f.config.Path = filePath
	f.ctx, f.cancel = context.WithCancel(ctx)
	f.status = StreamStatusRunning

	go f.readLoop()

	logger.Infof("file input started: %s, file: %s, streams: %d", f.id, filePath, len(streams))
	return nil
}

func (f *FileInput) readLoop() {
	args := []string{"-re"}
	if f.config.Loop {
		args = append(args, "-stream_loop", "-1")
	}
	args = append(args,
		"-i", f.config.Path,
		"-c", "copy",
		"-f", "mpegts",
		"pipe:1",
	)

	f.cmd = exec.CommandContext(f.ctx, "ffmpeg", args...)
	stdout, err := f.cmd.StdoutPipe()
	if err != nil {
		logger.Errorf("failed to create stdout pipe for %s: %v", f.id, err)
		f.status = StreamStatusError
		return
	}

	stderr, err := f.cmd.StderrPipe()
	if err != nil {
		logger.Errorf("failed to create stderr pipe for %s: %v", f.id, err)
		f.status = StreamStatusError
		return
	}

	if err := f.cmd.Start(); err != nil {
		logger.Errorf("failed to start ffmpeg for %s: %v", f.id, err)
		f.status = StreamStatusError
		return
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				logger.Debugf("ffmpeg stderr [%s]: %s", f.id, string(buf[:n]))
			}
			if err != nil {
				break
			}
		}
	}()

	var videoPTS int64
	var audioPTS int64
	var tsBuf []byte
	var videoPID, audioPID uint16
	pidDetected := false

	demuxer := NewTSDemuxer(0, 0)

	var audioCfg []byte
	for _, s := range f.streams {
		if s.Kind == "audio" && s.CodecID == CodecAAC {
			audioCfg = buildAACSpecificConfig(s)
			demuxer.SetAudioConfig(audioCfg)
			logger.Infof("AAC AudioSpecificConfig from probe: %02x%02x", audioCfg[0], audioCfg[1])
			break
		}
	}

	demuxer.OnVideo(func(data []byte, isKey bool) {
		f.mu.RLock()
		handler := f.handler
		f.mu.RUnlock()
		if handler == nil {
			return
		}
		handler(&MediaPacket{
			StreamID:   f.id,
			Kind:       "video",
			CodecID:    CodecH264,
			CodecType:  "h264",
			IsVideo:    true,
			IsKeyFrame: isKey,
			Data:       data,
			PTS:        videoPTS,
			DTS:        videoPTS,
			Timestamp:  videoPTS / 1000,
		})
		videoPTS += 33000
	})
	demuxer.OnAudio(func(data []byte, config []byte) {
		f.mu.RLock()
		handler := f.handler
		f.mu.RUnlock()
		if handler == nil {
			return
		}
		handler(&MediaPacket{
			StreamID:    f.id,
			Kind:        "audio",
			CodecID:     CodecAAC,
			CodecType:   "aac",
			IsAudio:     true,
			Data:        data,
			CodecConfig: config,
			PTS:         audioPTS,
			DTS:         audioPTS,
			Timestamp:   audioPTS / 1000,
		})
		audioPTS += 23000
	})

	buf := make([]byte, 64*1024)
	for {
		select {
		case <-f.ctx.Done():
			f.cmd.Process.Kill()
			return
		default:
		}

		n, err := stdout.Read(buf)
		if n > 0 {
			tsBuf = append(tsBuf, buf[:n]...)

			if !pidDetected && len(tsBuf) >= tsPacketSize*100 {
				videoPID, audioPID = DetectTSPIDs(tsBuf)
				if videoPID > 0 {
					demuxer.videoPID = videoPID
					demuxer.audioPID = audioPID
					pidDetected = true
					logger.Infof("TS PIDs detected: video=%d audio=%d", videoPID, audioPID)
				}
			}

			if pidDetected {
				demuxer.Feed(tsBuf)
				tsBuf = nil
			} else if len(tsBuf) > 1024*1024 {
				tsBuf = nil
			}
		}
		if err != nil {
			if err != io.EOF {
				logger.Debugf("file input read error for %s: %v", f.id, err)
			}
			break
		}
	}

	f.cmd.Wait()
	logger.Infof("file input read loop finished: %s", f.id)
}

func findStartCode(data []byte, start int) (int, int) {
	for i := start; i < len(data)-3; i++ {
		if data[i] == 0x00 && data[i+1] == 0x00 {
			if i+3 < len(data) && data[i+2] == 0x00 && data[i+3] == 0x01 {
				return i, 4
			}
			if data[i+2] == 0x01 {
				return i, 3
			}
		}
	}
	return -1, 0
}

func extractAccessUnit(data []byte) (au []byte, remaining []byte) {
	if len(data) < 4 {
		return nil, data
	}

	pos, scLen := findStartCode(data, 0)
	if pos < 0 {
		return nil, data
	}

	nextPos, _ := findStartCode(data, pos+scLen)
	if nextPos < 0 {
		return nil, data
	}

	return data[pos:nextPos], data[nextPos:]
}

func containsIDR(au []byte) bool {
	for i := 0; i < len(au)-4; i++ {
		if au[i] == 0x00 && au[i+1] == 0x00 {
			var nalStart int
			if au[i+2] == 0x00 && au[i+3] == 0x01 {
				nalStart = i + 4
			} else if au[i+2] == 0x01 {
				nalStart = i + 3
			} else {
				continue
			}
			if nalStart < len(au) {
				nalType := au[nalStart] & 0x1F
				if nalType == 5 {
					return true
				}
			}
		}
	}
	return false
}

func (f *FileInput) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.status == StreamStatusStopped {
		return nil
	}

	if f.cancel != nil {
		f.cancel()
	}

	if f.cmd != nil && f.cmd.Process != nil {
		f.cmd.Process.Kill()
	}

	f.status = StreamStatusStopped
	logger.Infof("file input stopped: %s", f.id)
	return nil
}

func (f *FileInput) ReadPacket() (*MediaPacket, error) {
	return nil, ErrStreamNotRunning
}

func (f *FileInput) OnPacket(handler PacketHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = handler
}

func buildAACSpecificConfig(s StreamInfo) []byte {
	sampleRate := 44100
	channels := 2
	if sr, ok := s.Parameters["sample_rate"].(int); ok && sr > 0 {
		sampleRate = sr
	}
	if sr, ok := s.Parameters["sample_rate"].(string); ok {
		fmt.Sscanf(sr, "%d", &sampleRate)
	}
	if ch, ok := s.Parameters["channels"].(int); ok && ch > 0 {
		channels = ch
	}

	objectType := 2 // AAC-LC
	if prof, ok := s.Parameters["profile"].(string); ok {
		switch prof {
		case "HE-AAC", "HE-AACv2":
			objectType = 5
		case "LC":
			objectType = 2
		}
	}

	sampleRateIndex := 4 // default 44100Hz
	switch sampleRate {
	case 96000:
		sampleRateIndex = 0
	case 88200:
		sampleRateIndex = 1
	case 64000:
		sampleRateIndex = 2
	case 48000:
		sampleRateIndex = 3
	case 44100:
		sampleRateIndex = 4
	case 32000:
		sampleRateIndex = 5
	case 24000:
		sampleRateIndex = 6
	case 22050:
		sampleRateIndex = 7
	case 16000:
		sampleRateIndex = 8
	case 12000:
		sampleRateIndex = 9
	case 11025:
		sampleRateIndex = 10
	case 8000:
		sampleRateIndex = 11
	case 7350:
		sampleRateIndex = 12
	}

	if channels > 2 {
		channels = 2
	}

	byte0 := byte(objectType<<3 | sampleRateIndex>>1)
	byte1 := byte((sampleRateIndex&0x01)<<7 | channels<<3)
	return []byte{byte0, byte1}
}
