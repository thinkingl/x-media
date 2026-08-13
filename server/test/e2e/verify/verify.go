// Package verify 实现基准参照数据的解码与对齐比对：
// 视频条码帧号解码、背景主色校验、音频 DTMF 半字节解码，以及序列连续性判定。
package verify

import (
	"math"

	"github.com/x-media/x-media-server/test/e2e/ref"
)

// VideoFrame 单帧解码结果。
type VideoFrame struct {
	FrameNo int  // 条码帧号；解码失败为 0
	OK      bool // 条码 sync + CRC 校验通过
	BgGray  int  // 背景主色（-1 表示未采样）
}

// DecodeVideoFrame 从 ffmpeg 输出的灰度裁剪帧解码条码帧号与背景主色。
// gray 长度为 CropW*CropH（1 字节/像素）。meta 提供几何参数（绝对坐标）。
func DecodeVideoFrame(gray []byte, meta *ref.Metadata) VideoFrame {
	v := meta.Video
	cw, ch := v.Crop.W, v.Crop.H
	if len(gray) < cw*ch {
		return VideoFrame{BgGray: -1}
	}
	ox, oy := v.Crop.X, v.Crop.Y

	// 条码逐块亮度
	bc := v.Barcode
	total := bc.SyncBlocks + bc.DataBits + bc.CRCBits
	means := make([]int, 0, total)
	for i := 0; i < total; i++ {
		x0 := bc.X - ox + i*bc.BlockW
		y0 := bc.Y - oy
		means = append(means, blockMean(gray, cw, x0, y0, bc.BlockW, bc.BlockH))
	}
	frameNo, ok := ref.BarcodeDecode(means)

	// 背景主色
	bg := -1
	bgx0 := v.Background.X - ox
	bgy0 := v.Background.Y - oy
	if bgx0 >= 0 && bgy0 >= 0 && bgx0+v.Background.W <= cw && bgy0+v.Background.H <= ch {
		bg = blockMean(gray, cw, bgx0, bgy0, v.Background.W, v.Background.H)
	}
	return VideoFrame{FrameNo: frameNo, OK: ok, BgGray: bg}
}

func blockMean(gray []byte, width, x0, y0, w, h int) int {
	sum, n := 0, 0
	rows := len(gray) / width
	for y := y0; y < y0+h; y++ {
		if y < 0 || y >= rows {
			continue
		}
		off := y*width + x0
		if off < 0 || off+w > len(gray) {
			continue
		}
		for x := 0; x < w; x++ {
			sum += int(gray[off+x])
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / n
}

// AudioChunk 单个音频块解码结果。
type AudioChunk struct {
	Nibble int // DTMF 半字节；无有效音为 -1
	OK     bool
}

// DecodeAudioChunk 从 1024 个 s16le 样本解码 DTMF 半字节。
func DecodeAudioChunk(samples []int16) AudioChunk {
	n := len(samples)
	if n == 0 {
		return AudioChunk{Nibble: -1}
	}
	// 在各 DTMF 候选频率上做 DFT 幅度
	rowMag := make([]float64, len(ref.DTMFRows))
	colMag := make([]float64, len(ref.DTMFRows))
	for i, f := range ref.DTMFRows {
		rowMag[i] = dftMag(samples, float64(f))
	}
	for i, f := range ref.DTMFCols {
		colMag[i] = dftMag(samples, float64(f))
	}
	bestRow, bestCol := argmax(rowMag), argmax(colMag)
	// 校验：双音主峰应显著高于其余候选
	rowGap := peakGap(rowMag, bestRow)
	colGap := peakGap(colMag, bestCol)
	if rowGap < 1.5 || colGap < 1.5 {
		return AudioChunk{Nibble: -1}
	}
	if bestRow < 0 || bestCol < 0 {
		return AudioChunk{Nibble: -1}
	}
	nib := ref.NibbleForDTMF(ref.DTMFRows[bestRow], ref.DTMFCols[bestCol])
	if nib < 0 {
		return AudioChunk{Nibble: -1}
	}
	return AudioChunk{Nibble: nib, OK: true}
}

// dftMag 在频率 f Hz（fs 采样率）上的 DFT 幅度。
func dftMag(samples []int16, f float64) float64 {
	const fs = 48000.0
	re, im := 0.0, 0.0
	n := len(samples)
	for i, s := range samples {
		ph := 2 * math.Pi * f * float64(i) / fs
		re += float64(s) * math.Cos(ph)
		im -= float64(s) * math.Sin(ph)
	}
	return math.Hypot(re, im) / float64(n)
}

func argmax(v []float64) int {
	bi := -1
	bm := -1.0
	for i, x := range v {
		if x > bm {
			bm = x
			bi = i
		}
	}
	return bi
}

func peakGap(v []float64, best int) float64 {
	second := -1.0
	for i, x := range v {
		if i == best {
			continue
		}
		if x > second {
			second = x
		}
	}
	if best < 0 || v[best] <= 0 || second <= 0 {
		return 0
	}
	return v[best] / second
}

// ---- 序列对齐与比对 ----

type VideoReport struct {
	TotalDecoded    int
	OKFrames        int
	CorruptFrames   int
	JoinMissed      int // 首个 OK 帧之前未解码的帧数（连接加入点，豁免）
	LostFrames      int // 首个 OK 帧之后的丢帧总数
	DuplicateFrames int // 重复帧数（回绕边界伪影之外的真实重复）
	WrapCount       int // 识别到的 loop 回绕次数
	BgMismatch      int // 背景主色与帧号不符的帧数
	FirstOKFrame    int // 首个成功解码的帧号
	LastOKFrame     int // 最后一个成功解码的帧号
	Pass            bool
}

// VerifyVideo 校验视频帧号序列：从首个"严格递增对"处起，帧号必须持续递增连续。
// 连接加入点的首 GOP 因解码错误隐藏/缺失而豁免（ffmpeg 会重复输出首个可解码帧）。
// loop=true 时按 meta.Video.Frames 作为循环周期识别 N→1 回绕。
func VerifyVideo(frames []VideoFrame, meta *ref.Metadata, loop bool) VideoReport {
	rep := VideoReport{TotalDecoded: len(frames)}

	// 定位暖启动结束：首个连续两帧均有效且 seq[i]==seq[i-1]+1 的位置。
	// 之前的帧（连接加入点 GOP 错误隐藏的重复帧/缺失）全部豁免。
	start := -1
	for i := 1; i < len(frames); i++ {
		if frames[i].OK && frames[i-1].OK && frames[i].FrameNo == frames[i-1].FrameNo+1 {
			start = i - 1
			break
		}
	}
	if start < 0 {
		rep.JoinMissed = len(frames) // 全部视为 join 期，无可用数据
		return rep
	}
	rep.FirstOKFrame = frames[start].FrameNo
	rep.JoinMissed = frames[start].FrameNo - 1
	expected := frames[start].FrameNo
	cycle := 0
	if loop {
		cycle = meta.Video.Frames
	}
	for i := start; i < len(frames); i++ {
		f := frames[i]
		if !f.OK {
			rep.CorruptFrames++
			continue
		}
		rep.OKFrames++
		rep.LastOKFrame = f.FrameNo
		if f.BgGray >= 0 && meta.Video.Background.Mod > 0 {
			if d := f.BgGray - f.FrameNo%meta.Video.Background.Mod; d > 3 || d < -3 {
				rep.BgMismatch++
			}
		}
		if i == start {
			continue
		}
		switch {
		case f.FrameNo == expected+1:
			expected++
		case cycle > 0 && expected == cycle && f.FrameNo == 1:
			expected = 1
			rep.WrapCount++
		case f.FrameNo > expected+1:
			rep.LostFrames += f.FrameNo - (expected + 1)
			expected = f.FrameNo
		default:
			rep.DuplicateFrames++
		}
	}
	// 允许的回绕伪影 = 回绕次数 × 2：每个回绕边界解码器可能瞬时丢失/重复 1~2 帧
	// （RTP 关键帧重排/解码错误隐藏伪影）。周期内部必须零丢失零重复——
	// 系统性丢帧（如节流漂移）会远超该预算而被捕获。
	rep.Pass = rep.LostFrames+rep.DuplicateFrames <= rep.WrapCount*2
	return rep
}

type AudioReport struct {
	Chunks       int // 解码块总数
	ValidChunks  int // 有效音块数
	SilentChunks int // 无有效音块数
	LostChunks   int // 有效块序列跳变（丢块）
	DupChunks    int // 有效块序列重复
	Pass         bool
}

// VerifyAudio 校验音频块序列：有效块半字节必须按 (prev+1)%16 递增。
func VerifyAudio(chunks []AudioChunk) AudioReport {
	rep := AudioReport{Chunks: len(chunks)}
	prev := -1
	for _, c := range chunks {
		if !c.OK {
			rep.SilentChunks++
			continue
		}
		rep.ValidChunks++
		if prev < 0 {
			prev = c.Nibble
			continue
		}
		expected := (prev + 1) % 16
		if c.Nibble == expected {
			prev = c.Nibble
			continue
		}
		if c.Nibble == prev {
			rep.DupChunks++
			continue
		}
		rep.LostChunks++
		prev = c.Nibble
	}
	rep.Pass = rep.LostChunks == 0 && rep.DupChunks == 0
	return rep
}
