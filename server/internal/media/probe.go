package media

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type MediaInfo struct {
	FileName    string         `json:"file_name"`
	FilePath    string         `json:"file_path"`
	FileSize    int64          `json:"file_size"`
	Duration    float64        `json:"duration"`
	BitRate     int64          `json:"bit_rate"`
	FormatName  string         `json:"format_name"`
	FormatLong  string         `json:"format_long_name"`
	Streams     []FFprobeStream `json:"streams"`
	ThumbnailPath string       `json:"thumbnail_path,omitempty"`
}

type FFprobeStream struct {
	Index       int    `json:"index"`
	CodecType   string `json:"codec_type"`
	CodecName   string `json:"codec_name"`
	CodecLong   string `json:"codec_long_name"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Profile     string `json:"profile,omitempty"`
	PixFmt      string `json:"pix_fmt,omitempty"`
	SampleRate  string `json:"sample_rate,omitempty"`
	Channels    int    `json:"channels,omitempty"`
	ChannelLayout string `json:"channel_layout,omitempty"`
	BitRate     string `json:"bit_rate,omitempty"`
}

type ffprobeFormat struct {
	Streams     []ffprobeStream `json:"streams"`
	Format      ffprobeFormatInfo `json:"format"`
}

type ffprobeStream struct {
	Index          int    `json:"index"`
	CodecType      string `json:"codec_type"`
	CodecName      string `json:"codec_name"`
	CodecLongName  string `json:"codec_long_name"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	Profile        string `json:"profile"`
	PixFmt         string `json:"pix_fmt"`
	SampleRate     string `json:"sample_rate"`
	Channels       int    `json:"channels"`
	ChannelLayout  string `json:"channel_layout"`
	BitRate        string `json:"bit_rate"`
}

type ffprobeFormatInfo struct {
	Filename       string `json:"filename"`
	NBStreams      int    `json:"nb_streams"`
	FormatName     string `json:"format_name"`
	FormatLongName string `json:"format_long_name"`
	Duration       string `json:"duration"`
	Size           string `json:"size"`
	BitRate        string `json:"bit_rate"`
}

func ProbeFile(filePath string) (*MediaInfo, error) {
	if err := ValidateFilePath(filePath); err != nil {
		return nil, fmt.Errorf("invalid file path: %w", err)
	}
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w, output: %s", err, string(output))
	}

	var probe ffprobeFormat
	if err := json.Unmarshal(output, &probe); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	info := &MediaInfo{
		FileName:   filepath.Base(filePath),
		FilePath:   filePath,
		FormatName:  probe.Format.FormatName,
		FormatLong: probe.Format.FormatLongName,
	}

	fmt.Sscanf(probe.Format.Duration, "%f", &info.Duration)
	fmt.Sscanf(probe.Format.Size, "%d", &info.FileSize)
	fmt.Sscanf(probe.Format.BitRate, "%d", &info.BitRate)

	for _, s := range probe.Streams {
		stream := FFprobeStream{
			Index:        s.Index,
			CodecType:    s.CodecType,
			CodecName:    s.CodecName,
			CodecLong:    s.CodecLongName,
			Width:        s.Width,
			Height:       s.Height,
			Profile:      s.Profile,
			PixFmt:       s.PixFmt,
			SampleRate:   s.SampleRate,
			Channels:     s.Channels,
			ChannelLayout: s.ChannelLayout,
			BitRate:      s.BitRate,
		}
		info.Streams = append(info.Streams, stream)
	}

	return info, nil
}

func ExtractThumbnail(filePath, outputPath string, timeSeconds float64) error {
	if err := ValidateFilePath(filePath); err != nil {
		return fmt.Errorf("invalid input file path: %w", err)
	}
	if err := ValidateFilePath(outputPath); err != nil {
		return fmt.Errorf("invalid output path: %w", err)
	}
	cmd := exec.Command("ffmpeg",
		"-y",
		"-ss", fmt.Sprintf("%.2f", timeSeconds),
		"-i", filePath,
		"-vframes", "1",
		"-vf", "scale=320:-1",
		outputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg thumbnail failed: %w, output: %s", err, string(output))
	}
	return nil
}

func FormatDuration(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func FormatBitRate(bps int64) string {
	if bps >= 1000000 {
		return fmt.Sprintf("%.2f Mbps", float64(bps)/1000000)
	}
	return fmt.Sprintf("%.0f Kbps", float64(bps)/1000)
}

func GetVideoStream(info *MediaInfo) *FFprobeStream {
	for i := range info.Streams {
		if strings.EqualFold(info.Streams[i].CodecType, "video") {
			return &info.Streams[i]
		}
	}
	return nil
}

func GetAudioStream(info *MediaInfo) *FFprobeStream {
	for i := range info.Streams {
		if strings.EqualFold(info.Streams[i].CodecType, "audio") {
			return &info.Streams[i]
		}
	}
	return nil
}

func ValidateFilePath(filePath string) error {
	if filePath == "" {
		return fmt.Errorf("file path is empty")
	}
	if strings.ContainsRune(filePath, '\x00') {
		return fmt.Errorf("file path contains null byte")
	}
	if !filepath.IsAbs(filePath) {
		return fmt.Errorf("file path must be absolute: %s", filePath)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("file not accessible: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory, not a file: %s", filePath)
	}
	return nil
}
