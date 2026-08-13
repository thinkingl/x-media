// Package ref 定义基准参照式 E2E 测试的共享常量与编解码逻辑，
// 由生成器（gen）与校验器（verify）共同引用，保证两侧几何/编码一致。
package ref

import "encoding/json"

// ---- 视频布局常量（1080p） ----

const (
	DefaultWidth      = 1920
	DefaultHeight     = 1080
	DefaultFPS        = 30
	DefaultBarcodeX   = 16
	DefaultBarcodeY   = 16
	DefaultBarcodeBW  = 16 // 每个 bit 块宽度（px）
	DefaultBarcodeBH  = 40 // 每个 bit 块高度（px）
	BarcodeSyncBlocks = 2
	BarcodeDataBits   = 24 // 帧号 bit 数（最多 1677 万帧）
	BarcodeCRCBits    = 8  // CRC8
	BarcodeBlocks     = BarcodeSyncBlocks + BarcodeDataBits + BarcodeCRCBits

	// 校验用裁剪区（含条码 + 一段纯背景色，单次 ffmpeg crop 即可取到两路信号）
	CropX, CropY = 0, 0
	CropW, CropH = 720, 64

	// 背景主色采样区（绝对坐标，位于裁剪区内）
	BgX, BgY, BgW, BgH = 600, 0, 120, 64
	BgMod              = 256 // 背景灰度 = frameNo % BgMod

	// 七段数码显示（人工查看，右上角）
	SegDigitW  = 40
	SegDigitH  = 70
	SegGap     = 8
	SegThick   = 8
	SegDigitsX = 1624
	SegDigitsY = 16
	SegDigitsN = 6

	// 底部固定棋盘条（检测内容错乱/串扰，所有帧一致）
	BottomStripH    = 24
	BottomStripCell = 8
)

// ---- 音频布局常量 ----

const (
	DefaultSampleRate = 48000
	DefaultChannels   = 1
	DefaultChunkSize  = 1024 // AAC 帧样本数；每块编码 1 个半字节
)

// DTMF 频率表：值 v(0..15) → 行频×列频。
var DTMFRows = []int{697, 770, 852, 941}
var DTMFCols = []int{1209, 1336, 1477, 1633}

// ---- 元数据 ----

type Metadata struct {
	Scenario string    `json:"scenario"`
	Loop     bool      `json:"loop"`
	Video    VideoMeta `json:"video"`
	Audio    AudioMeta `json:"audio"`
}

type VideoMeta struct {
	Codec   string `json:"codec"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	FPS     int    `json:"fps"`
	Frames  int    `json:"frames"`
	GOP     int    `json:"gop"`
	Bframes int    `json:"bframes"`
	CRF     int    `json:"crf"`

	Crop       CropMeta       `json:"crop"`
	Barcode    BarcodeMeta    `json:"barcode"`
	Background BackgroundMeta `json:"background"`
}

type CropMeta struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type BarcodeMeta struct {
	X          int `json:"x"`
	Y          int `json:"y"`
	BlockW     int `json:"block_w"`
	BlockH     int `json:"block_h"`
	SyncBlocks int `json:"sync_blocks"`
	DataBits   int `json:"data_bits"`
	CRCBits    int `json:"crc_bits"`
}

type BackgroundMeta struct {
	X   int `json:"x"`
	Y   int `json:"y"`
	W   int `json:"w"`
	H   int `json:"h"`
	Mod int `json:"mod"`
}

type AudioMeta struct {
	Codec      string   `json:"codec"`
	SampleRate int      `json:"sample_rate"`
	Channels   int      `json:"channels"`
	ChunkSize  int      `json:"chunk_size"`
	Chunks     int      `json:"chunks"`
	DTMF       DTMFMeta `json:"dtmf"`
}

type DTMFMeta struct {
	Rows         []int `json:"rows"`
	Cols         []int `json:"cols"`
	BitsPerChunk int   `json:"bits_per_chunk"`
}

func LoadMetadata(b []byte) (*Metadata, error) {
	var m Metadata
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ---- 条码编解码 ----

// CRC8 计算（多项式 0x07，初值 0）。
func CRC8(data []byte) byte {
	var crc byte
	for _, b := range data {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&0x80 != 0 {
				crc = (crc << 1) ^ 0x07
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// BarcodeBits 返回帧号对应的 34 个 bit 值：sync[黑=0,白=1] + data(24bit,MSB先) + crc(8bit,MSB先)。
func BarcodeBits(frameNo int) []int {
	bits := make([]int, 0, BarcodeBlocks)
	bits = append(bits, 0, 1) // sync: 黑, 白

	// 24bit 帧号，大端
	data := []byte{byte(frameNo >> 16), byte(frameNo >> 8), byte(frameNo)}
	crc := CRC8(data)
	full := []byte{data[0], data[1], data[2], crc}
	for _, b := range full {
		for i := 7; i >= 0; i-- {
			bits = append(bits, int((b>>i)&1))
		}
	}
	return bits
}

// BarcodeDecode 将 34 个块亮度（0..255 灰度）解码为帧号。
// 返回帧号与是否通过 sync + CRC 校验。
func BarcodeDecode(means []int) (int, bool) {
	if len(means) < BarcodeBlocks {
		return 0, false
	}
	// 两簇阈值：取亮度中位/均值划分黑白
	lo, hi := 255, 0
	for _, v := range means[:BarcodeBlocks] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi-lo < 40 {
		return 0, false // 动态范围太小，无法分簇
	}
	th := (lo + hi) / 2
	bits := make([]int, 0, BarcodeBlocks)
	for _, v := range means[:BarcodeBlocks] {
		if v >= th {
			bits = append(bits, 1)
		} else {
			bits = append(bits, 0)
		}
	}
	if bits[0] != 0 || bits[1] != 1 {
		return 0, false // sync 失败
	}
	var bytes [4]byte
	for b := 0; b < 4; b++ {
		for i := 0; i < 8; i++ {
			bytes[b] = (bytes[b] << 1) | byte(bits[2+b*8+i])
		}
	}
	if CRC8(bytes[:3]) != bytes[3] {
		return 0, false
	}
	return int(bytes[0])<<16 | int(bytes[1])<<8 | int(bytes[2]), true
}

// ---- 七段数码管 ----

// digitSegments 返回数字 0..9 亮起的段位（a..g 用 0..6 表示）。
var digitSegments = [][]int{
	{0, 1, 2, 3, 4, 5},    // 0: a b c d e f
	{1, 2},                // 1: b c
	{0, 1, 6, 3, 4},       // 2: a b g e d
	{0, 1, 6, 2, 3},       // 3: a b g c d
	{5, 6, 1, 2},          // 4: f g b c
	{0, 5, 6, 2, 3},       // 5: a f g c d
	{0, 5, 6, 4, 2, 3},    // 6: a f g e c d
	{0, 1, 2},             // 7: a b c
	{0, 1, 2, 3, 4, 5, 6}, // 8: all
	{0, 5, 6, 1, 2, 3},    // 9: a f g b c d
}

// segmentRects 返回段位 a..g 在数码管盒内的矩形（x0,y0,x1,y1）。
func segmentRects() [][4]int {
	t := SegThick
	w, h := SegDigitW, SegDigitH
	return [][4]int{
		{8, 0, w - 8, t},                 // a 上
		{w - t, 8, w, h/2 - 4},           // b 右上
		{w - t, h/2 + 4, w, h - 8},       // c 右下
		{8, h - t, w - 8, h},             // d 下
		{0, h/2 + 4, t, h - 8},           // e 左下
		{0, 8, t, h/2 - 4},               // f 左上
		{8, h/2 - t/2, w - 8, h/2 + t/2}, // g 中
	}
}

// SegmentRects 导出段位矩形（供渲染）。
func SegmentRects() [][4]int { return segmentRects() }

// DigitSegments 返回数字 v(0..9) 点亮的段位索引。
func DigitSegments(v int) []int {
	if v < 0 || v > 9 {
		return nil
	}
	return digitSegments[v]
}

// ---- DTMF ----

// DTMFForNibble 返回半字节 v(0..15) 对应的 (行频, 列频) Hz。
func DTMFForNibble(v int) (int, int) {
	return DTMFRows[v/len(DTMFCols)], DTMFCols[v%len(DTMFCols)]
}

// NibbleForDTMF 由 (行频, 列频) 反查半字节 v；找不到返回 -1。
func NibbleForDTMF(rowHz, colHz int) int {
	ri, ci := -1, -1
	for i, f := range DTMFRows {
		if rowHz == f {
			ri = i
			break
		}
	}
	for i, f := range DTMFCols {
		if colHz == f {
			ci = i
			break
		}
	}
	if ri < 0 || ci < 0 {
		return -1
	}
	return ri*len(DTMFCols) + ci
}
