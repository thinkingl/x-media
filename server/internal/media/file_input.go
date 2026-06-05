package media

import (
	"context"
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
	args := []string{
		"-re",
		"-i", f.config.Path,
		"-c:v", "copy",
		"-an",
		"-f", "h264",
		"pipe:1",
	}

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
	var auBuffer []byte

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
			auBuffer = append(auBuffer, buf[:n]...)

			for {
				au, remaining := extractAccessUnit(auBuffer)
				if au == nil {
					auBuffer = remaining
					break
				}
				auBuffer = remaining

				isKey := containsIDR(au)

				pkt := &MediaPacket{
					StreamID:   f.id,
					Kind:       "video",
					CodecID:    CodecH264,
					CodecType:  "h264",
					IsVideo:    true,
					IsKeyFrame: isKey,
					Data:       au,
					PTS:        videoPTS,
					DTS:        videoPTS,
					Timestamp:  videoPTS / 1000,
				}
				videoPTS += 33000

				f.mu.RLock()
				handler := f.handler
				f.mu.RUnlock()

				if handler != nil {
					handler(pkt)
				}
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
