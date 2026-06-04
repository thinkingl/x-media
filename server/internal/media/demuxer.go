package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/x-media/x-media-server/pkg/logger"
)

type StreamDemuxer struct {
	mu       sync.RWMutex
	source   string
	streams  []StreamInfo
	cancel   context.CancelFunc
	handlers map[uint8]func(*MediaPacket)
}

func NewStreamDemuxer(source string) *StreamDemuxer {
	return &StreamDemuxer{
		source:   source,
		handlers: make(map[uint8]func(*MediaPacket)),
	}
}

func (d *StreamDemuxer) Probe() ([]StreamInfo, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		d.source,
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	streams, err := DetectStreamsFromFFprobe(output)
	if err != nil {
		return nil, fmt.Errorf("failed to detect streams: %w", err)
	}

	d.mu.Lock()
	d.streams = streams
	d.mu.Unlock()

	return streams, nil
}

func (d *StreamDemuxer) GetStreams() []StreamInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.streams
}

func (d *StreamDemuxer) OnPacket(channelID uint8, handler func(*MediaPacket)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[channelID] = handler
}

func (d *StreamDemuxer) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.streams) == 0 {
		return fmt.Errorf("no streams detected, call Probe() first")
	}

	ctx, d.cancel = context.WithCancel(ctx)

	go d.demuxAll(ctx)

	return nil
}

func (d *StreamDemuxer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cancel != nil {
		d.cancel()
	}
}

func (d *StreamDemuxer) demuxAll(ctx context.Context) {
	args := []string{
		"-re",
		"-i", d.source,
		"-c", "copy",
		"-f", "flv",
		"pipe:1",
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Errorf("failed to create stdout pipe: %v", err)
		return
	}

	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		logger.Errorf("failed to start ffmpeg demuxer: %v", err)
		return
	}

	logger.Infof("demux started for %s (%d streams)", d.source, len(d.streams))

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			cmd.Process.Kill()
			return
		default:
		}

		n, err := stdout.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])

			pkt := &MediaPacket{
				StreamID:  "demux",
				Kind:      "video",
				CodecID:   CodecH264,
				CodecType: "H264",
				IsVideo:   true,
				Data:      data,
				Timestamp: time.Now().UnixMilli(),
			}

			d.mu.RLock()
			for _, handler := range d.handlers {
				handler(pkt)
			}
			d.mu.RUnlock()
		}
		if err != nil {
			break
		}
	}

	cmd.Wait()
	logger.Infof("demux stopped for %s", d.source)
}

func ProbeFileStreams(filePath string) ([]StreamInfo, error) {
	demuxer := NewStreamDemuxer(filePath)
	return demuxer.Probe()
}

func ProbeAndSaveMediaInfo(filePath string) (string, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe failed: %w", err)
	}

	var probe struct {
		Streams []struct {
			Index     int    `json:"index"`
			CodecName string `json:"codec_name"`
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			RFrameRate string `json:"r_frame_rate"`
			SampleRate string `json:"sample_rate"`
			Channels  int    `json:"channels"`
			BitRate   string `json:"bit_rate"`
			Profile   string `json:"profile"`
		} `json:"streams"`
		Format struct {
			Filename string `json:"filename"`
			Duration string `json:"duration"`
			Size     string `json:"size"`
			BitRate  string `json:"bit_rate"`
			FormatName string `json:"format_name"`
		} `json:"format"`
	}

	if err := json.Unmarshal(output, &probe); err != nil {
		return "", fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	info := map[string]any{
		"file_name":  probe.Format.Filename,
		"file_path":  filePath,
		"duration":   probe.Format.Duration,
		"size":       probe.Format.Size,
		"bit_rate":   probe.Format.BitRate,
		"format":     probe.Format.FormatName,
		"streams":    make([]map[string]any, 0),
	}

	streams := make([]map[string]any, 0)
	for _, s := range probe.Streams {
		stream := map[string]any{
			"index":      s.Index,
			"codec_name": s.CodecName,
			"codec_type": s.CodecType,
			"profile":    s.Profile,
		}
		if s.CodecType == "video" {
			stream["width"] = s.Width
			stream["height"] = s.Height
			stream["frame_rate"] = s.RFrameRate
		} else if s.CodecType == "audio" {
			stream["sample_rate"] = s.SampleRate
			stream["channels"] = s.Channels
		}
		streams = append(streams, stream)
	}
	info["streams"] = streams

	jsonData, err := json.Marshal(info)
	if err != nil {
		return "", fmt.Errorf("failed to marshal media info: %w", err)
	}

	return string(jsonData), nil
}
