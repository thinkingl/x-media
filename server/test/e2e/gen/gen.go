// Package gen 生成基准参照 MP4：逐帧渲染（帧号条码 + 七段数码 + 主色背景）
// + DTMF 音频，经 ffmpeg 编码封装为 MP4，并输出 metadata.json 供校验器比对。
package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/x-media/x-media-server/test/e2e/ref"
)

// Config 生成参数。
type Config struct {
	Scenario   string
	Width      int
	Height     int
	FPS        int
	Frames     int // 视频帧数（Frames/FPS = 时长）
	WithAudio  bool
	SampleRate int
	Channels   int
	ChunkSize  int
	CRF        int
	GOP        int
	Bframes    int // -1 使用 libx264 默认
	Loop       bool
}

func (c *Config) fillDefaults() {
	if c.Width == 0 {
		c.Width = ref.DefaultWidth
	}
	if c.Height == 0 {
		c.Height = ref.DefaultHeight
	}
	if c.FPS == 0 {
		c.FPS = ref.DefaultFPS
	}
	if c.Frames == 0 {
		c.Frames = ref.DefaultFPS * 10 // 默认 10s
	}
	if c.SampleRate == 0 {
		c.SampleRate = ref.DefaultSampleRate
	}
	if c.Channels == 0 {
		c.Channels = ref.DefaultChannels
	}
	if c.ChunkSize == 0 {
		c.ChunkSize = ref.DefaultChunkSize
	}
	if c.CRF == 0 {
		c.CRF = 23
	}
	if c.GOP == 0 {
		c.GOP = 60
	}
	if c.Scenario == "" {
		c.Scenario = "default"
	}
}

// FillDefaults 填充未设置字段的默认值。
func (c *Config) FillDefaults() {
	c.fillDefaults()
}

// AudioChunks 返回音频块数（按 ChunkSize 整除采样）。
func (c Config) AudioChunks() int {
	if c.ChunkSize <= 0 || c.FPS <= 0 {
		return 0
	}
	total := c.Frames * c.SampleRate / c.FPS
	return total / c.ChunkSize
}

// AudioSamples 返回音频样本总数（ChunkSize 的整数倍）。
func (c Config) AudioSamples() int {
	return c.AudioChunks() * c.ChunkSize
}

// Metadata 基于配置构造元数据。
func (c Config) Metadata() *ref.Metadata {
	m := &ref.Metadata{
		Scenario: c.Scenario,
		Loop:     c.Loop,
		Video: ref.VideoMeta{
			Codec:   "h264",
			Width:   c.Width,
			Height:  c.Height,
			FPS:     c.FPS,
			Frames:  c.Frames,
			GOP:     c.GOP,
			CRF:     c.CRF,
			Bframes: c.Bframes,
			Crop:    ref.CropMeta{X: ref.CropX, Y: ref.CropY, W: ref.CropW, H: ref.CropH},
			Barcode: ref.BarcodeMeta{
				X: ref.DefaultBarcodeX, Y: ref.DefaultBarcodeY,
				BlockW: ref.DefaultBarcodeBW, BlockH: ref.DefaultBarcodeBH,
				SyncBlocks: ref.BarcodeSyncBlocks,
				DataBits:   ref.BarcodeDataBits,
				CRCBits:    ref.BarcodeCRCBits,
			},
			Background: ref.BackgroundMeta{X: ref.BgX, Y: ref.BgY, W: ref.BgW, H: ref.BgH, Mod: ref.BgMod},
		},
	}
	if c.WithAudio {
		m.Audio = ref.AudioMeta{
			Codec:      "aac",
			SampleRate: c.SampleRate,
			Channels:   c.Channels,
			ChunkSize:  c.ChunkSize,
			Chunks:     c.AudioChunks(),
			DTMF: ref.DTMFMeta{
				Rows:         ref.DTMFRows,
				Cols:         ref.DTMFCols,
				BitsPerChunk: 4,
			},
		}
	}
	return m
}

// RenderFrame 渲染第 frameNo 帧（1-based）为 rgb24 字节。
func (c Config) RenderFrame(frameNo int) []byte {
	buf := make([]byte, c.Width*c.Height*3)

	bg := byte(frameNo % ref.BgMod)
	// 纯色背景
	for i := 0; i < c.Width*c.Height; i++ {
		buf[i*3] = bg
		buf[i*3+1] = bg
		buf[i*3+2] = bg
	}

	// 条码区（黑色 box 垫底后画白/黑块，保证与背景区分）
	bx, by := ref.DefaultBarcodeX, ref.DefaultBarcodeY
	bbw, bbh := ref.DefaultBarcodeBW, ref.DefaultBarcodeBH
	bits := ref.BarcodeBits(frameNo)
	for i, b := range bits {
		x := bx + i*bbw
		fillRect(buf, c.Width, x, by, bbw, bbh, segColor(bg, b))
	}

	// 七段数码（黑色垫底 + 白色数字）
	drawSevenSeg(buf, c.Width, frameNo)

	// 底部固定棋盘条
	drawBottomStrip(buf, c.Width, c.Height)

	return buf
}

// segColor 条码块颜色：bit=1 → 白（与背景高对比），bit=0 → 黑。
func segColor(bg byte, bit int) byte {
	if bit == 1 {
		return 255
	}
	return 0
}

func fillRect(buf []byte, width, x0, y0, w, h int, gray byte) {
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			if x < 0 || y < 0 || x >= width {
				continue
			}
			off := (y*width + x) * 3
			buf[off] = gray
			buf[off+1] = gray
			buf[off+2] = gray
		}
	}
}

func drawSevenSeg(buf []byte, width int, frameNo int) {
	bx, by := ref.SegDigitsX, ref.SegDigitsY
	totalW := ref.SegDigitsN*ref.SegDigitW + (ref.SegDigitsN-1)*ref.SegGap + 16
	fillRect(buf, width, bx-8, by-8, totalW, ref.SegDigitH+16, 0) // 黑色垫底

	s := fmt.Sprintf("%0*d", ref.SegDigitsN, frameNo)
	rects := ref.SegmentRects()
	for i := 0; i < ref.SegDigitsN; i++ {
		d := int(s[i] - '0')
		dx := bx + i*(ref.SegDigitW+ref.SegGap)
		for _, seg := range ref.DigitSegments(d) {
			r := rects[seg]
			fillRect(buf, width, dx+r[0], by+r[1], r[2]-r[0], r[3]-r[1], 255)
		}
	}
}

func drawBottomStrip(buf []byte, width, height int) {
	y0 := height - ref.BottomStripH
	for y := y0; y < height; y++ {
		for x := 0; x < width; x++ {
			cell := (x / ref.BottomStripCell) + (y-y0)/ref.BottomStripCell
			gray := byte(0)
			if cell%2 == 0 {
				gray = 255
			}
			off := (y*width + x) * 3
			buf[off] = gray
			buf[off+1] = gray
			buf[off+2] = gray
		}
	}
}

// RenderAudio 生成 s16le PCM：每 ChunkSize 样本一块，编码半字节 digit = chunkNo % 16 的 DTMF 双音。
func (c Config) RenderAudio() []byte {
	samples := c.AudioSamples()
	out := make([]byte, samples*c.Channels*2)
	chunks := samples / c.ChunkSize
	amp := 0.35 * 32767
	for ch := 0; ch < chunks; ch++ {
		rowHz, colHz := ref.DTMFForNibble(ch % 16)
		base := ch * c.ChunkSize
		for i := 0; i < c.ChunkSize; i++ {
			t := float64(base+i) / float64(c.SampleRate)
			v := amp * (math.Sin(2*math.Pi*float64(rowHz)*t) + math.Sin(2*math.Pi*float64(colHz)*t))
			sample := int16(v)
			o := (base + i) * c.Channels * 2
			out[o] = byte(sample)
			out[o+1] = byte(sample >> 8)
		}
	}
	return out
}

// Generate 渲染并编码，产出 outMP4 与 outMeta。
// videoStdin=true 时通过管道向 ffmpeg 直送 rgb24 帧（不落盘）。
func (c Config) Generate(videoRaw []byte, audioRaw []byte, outMP4, outMeta string) error {
	c.fillDefaults()

	dir := filepath.Dir(outMP4)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	audioPath := ""
	var ffmpegArgs []string
	ffmpegArgs = append(ffmpegArgs, "-y")

	// 输入 0：rawvideo 帧流（stdin）
	ffmpegArgs = append(ffmpegArgs,
		"-f", "rawvideo", "-pix_fmt", "rgb24",
		"-s", fmt.Sprintf("%dx%d", c.Width, c.Height),
		"-r", fmt.Sprintf("%d", c.FPS),
		"-i", "-",
	)

	if c.WithAudio {
		audioPath = filepath.Join(dir, ".gen_audio.raw")
		if err := os.WriteFile(audioPath, audioRaw, 0o644); err != nil {
			return err
		}
		defer os.Remove(audioPath)
		// 输入 1：s16le PCM
		ffmpegArgs = append(ffmpegArgs,
			"-f", "s16le",
			"-ar", fmt.Sprintf("%d", c.SampleRate),
			"-ac", fmt.Sprintf("%d", c.Channels),
			"-i", audioPath,
		)
	}

	ffmpegArgs = append(ffmpegArgs,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", fmt.Sprintf("%d", c.CRF),
		"-pix_fmt", "yuv420p", "-g", fmt.Sprintf("%d", c.GOP),
		"-threads", "1",
	)
	if c.Bframes >= 0 {
		ffmpegArgs = append(ffmpegArgs, "-bf", fmt.Sprintf("%d", c.Bframes))
	}
	if c.WithAudio {
		ffmpegArgs = append(ffmpegArgs, "-c:a", "aac", "-b:a", "128k")
	}
	if c.WithAudio {
		ffmpegArgs = append(ffmpegArgs, "-shortest")
	}
	ffmpegArgs = append(ffmpegArgs, outMP4)

	cmd := exec.Command("ffmpeg", ffmpegArgs...)
	cmd.Stdin = bytes.NewReader(videoRaw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg encode failed: %v: %s", err, truncate(stderr.String(), 4000))
	}

	meta := c.Metadata()
	mb, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outMeta, mb, 0o644)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
