package media

// gb28181_media.go —— 标准帧 → GB28181 媒体层输入（方案 §5.7）。
//
// 视频：Annex-B 帧 → NALU（关键帧补齐 SPS/PPS/VPS 参数集，来自 CodecConfig）；
// 音频：raw AAC 帧 + 7 字节 ADTS 头（参数来自 AudioSpecificConfig，无需转码）；
// 时间戳：流内 PTS（ClockRate 单位）→ 会话基准单调墙钟，内置库再换算 90kHz。

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/x-media/x-media-server/internal/gb28181/device"
	"github.com/x-media/x-media-server/pkg/logger"
)

// gb28181AudioQueueMax 待装配音频帧上限（≈6s @ 43 帧/s）；溢出丢最旧。
const gb28181AudioQueueMax = 256

// gb28181FramePipe 帧转换器（Configure 填充；数据面逐帧调用，独立小锁）。
type gb28181FramePipe struct {
	mu sync.Mutex

	videoCodec  string // "h264" | "h265"
	videoClock  int
	configNALUs [][]byte // H264: SPS/PPS；H265: VPS/SPS/PPS

	audioEnabled bool
	audioClock   int
	adts         [7]byte

	epoch  time.Time
	audioQ []device.AudioFrame
}

func newGB28181FramePipe() *gb28181FramePipe {
	return &gb28181FramePipe{epoch: time.Now()}
}

// configure 从管道协商结果填充转换参数（校验规则与旧实现保持一致）。
func (p *gb28181FramePipe) configure(streams []StreamInfo, codecLimit string, audioEnable bool) error {
	var video, audio *StreamInfo
	for i := range streams {
		st := &streams[i]
		switch st.Kind {
		case "video":
			if st.CodecID == CodecH264 || st.CodecID == CodecH265 {
				video = st
			}
		case "audio":
			if st.CodecID == CodecAAC && audio == nil {
				audio = st
			}
		}
	}
	if video == nil {
		return errors.New("gb28181: 未找到 H.264/H.265 视频轨道（GB28181 输出仅支持这两种视频编码）")
	}

	videoCodec := "h264"
	if video.CodecID == CodecH265 {
		if codecLimit == "h264" {
			return errors.New("gb28181: 源为 H.265，但输出配置 video_codec=h264（不做转码，无法输出）；请改用 H.264 源或移除该限制")
		}
		videoCodec = "h265"
	}

	var configNALUs [][]byte
	if video.CodecID == CodecH265 {
		vps, sps, pps := splitCodecConfigHevc(video.CodecConfig)
		for _, n := range [][]byte{vps, sps, pps} {
			if len(n) > 0 {
				configNALUs = append(configNALUs, n)
			}
		}
	} else {
		sps, pps := splitCodecConfigVideo(video.CodecConfig)
		for _, n := range [][]byte{sps, pps} {
			if len(n) > 0 {
				configNALUs = append(configNALUs, n)
			}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.videoCodec = videoCodec
	p.videoClock = video.ClockRate
	if p.videoClock <= 0 {
		p.videoClock = 90000
	}
	p.configNALUs = configNALUs
	p.audioEnabled = audioEnable && audio != nil
	if p.audioEnabled {
		clock, adts, err := gb28181ADTSHeader(audio.CodecConfig, audio.ClockRate)
		if err != nil {
			return fmt.Errorf("gb28181: %w", err)
		}
		p.audioClock, p.adts = clock, adts
	}
	p.audioQ = nil
	return nil
}

// videoAU 转换一个视频帧；关键帧确保参数集在位；附带自上次视频帧以来的音频窗口。
func (p *gb28181FramePipe) videoAU(f *Frame) *device.AccessUnit {
	nals := splitAnnexB(f.Payload)
	if len(nals) == 0 {
		return nil
	}
	isKey := f.Header.Flags&FlagKeyframe != 0

	p.mu.Lock()
	defer p.mu.Unlock()

	if isKey && len(p.configNALUs) > 0 && !hasVideoParameterSets(nals, p.videoCodec) {
		all := make([][]byte, 0, len(p.configNALUs)+len(nals))
		all = append(all, p.configNALUs...)
		all = append(all, nals...)
		nals = all
	}

	aus := make([]device.NALU, 0, len(nals))
	for _, n := range nals {
		aus = append(aus, device.NALU{
			Type:  gb28181NALUType(n, p.videoCodec),
			Data:  n,
			IsIDR: gb28181IsIDR(n, p.videoCodec),
		})
	}
	au := &device.AccessUnit{
		NALUs:     aus,
		Timestamp: p.tsLocked(f.Header.PTS, p.videoClock),
		// DTS 同步换算：B 帧源的 PTS 非单调，PS/PES 必须同时携带 PTS+DTS。
		DTS:      p.tsLocked(f.Header.DTS, p.videoClock),
		KeyFrame: isKey,
	}
	if p.audioEnabled && len(p.audioQ) > 0 {
		au.Audio = p.audioQ
		p.audioQ = nil
	}
	return au
}

// audioFrame 入队一个音频帧（raw AAC + ADTS 头）。
func (p *gb28181FramePipe) audioFrame(f *Frame) {
	if len(f.Payload) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.audioEnabled {
		return
	}
	frameLen := 7 + len(f.Payload)
	hdr := p.adts
	hdr[3] |= byte((frameLen >> 11) & 0x03)
	hdr[4] = byte((frameLen >> 3) & 0xFF)
	hdr[5] = byte((frameLen&0x07)<<5) | 0x1F

	data := make([]byte, 0, frameLen)
	data = append(data, hdr[:]...)
	data = append(data, f.Payload...)

	p.audioQ = append(p.audioQ, device.AudioFrame{
		Data:      data,
		Timestamp: p.tsLocked(f.Header.PTS, p.audioClock),
	})
	if len(p.audioQ) > gb28181AudioQueueMax {
		p.audioQ = p.audioQ[1:]
	}
}

// tsLocked PTS（ClockRate 单位）→ 会话基准单调墙钟；调用方须持有 p.mu。
func (p *gb28181FramePipe) tsLocked(pts int64, clock int) time.Time {
	if clock <= 0 {
		clock = 90000
	}
	if pts < 0 {
		pts = 0
	}
	return p.epoch.Add(time.Duration(pts) * time.Second / time.Duration(clock))
}

// videoCodecName 当前视频编码（供 Start 时设置内置库初始值；默认 h264）。
func (p *gb28181FramePipe) videoCodecName() (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	audio := ""
	if p.audioEnabled {
		audio = "aac"
	}
	if p.videoCodec == "" {
		return "h264", audio
	}
	return p.videoCodec, audio
}

// hasVideoParameterSets 判断 NALU 列表是否已携带参数集（SPS+PPS / H265 VPS/SPS/PPS 至少 SPS+PPS）。
func hasVideoParameterSets(nals [][]byte, codec string) bool {
	var sps, pps bool
	for _, n := range nals {
		if len(n) == 0 {
			continue
		}
		if codec == "h265" {
			if len(n) < 2 {
				continue
			}
			switch n[0] >> 1 & 0x3F {
			case 33:
				sps = true
			case 34:
				pps = true
			}
		} else {
			switch n[0] & 0x1F {
			case 7:
				sps = true
			case 8:
				pps = true
			}
		}
	}
	return sps && pps
}

// gb28181NALUType 取 NALU 类型（H264 低 5 位；H265 首字节 6 位）。
func gb28181NALUType(n []byte, codec string) byte {
	if len(n) == 0 {
		return 0
	}
	if codec == "h265" {
		if len(n) < 2 {
			return 0
		}
		return n[0] >> 1 & 0x3F
	}
	return n[0] & 0x1F
}

// gb28181IsIDR 判断 NALU 是否为 IDR（H264 type 5；H265 type 19/20）。
func gb28181IsIDR(n []byte, codec string) bool {
	switch gb28181NALUType(n, codec) {
	case 5, 19, 20:
		return true
	}
	return false
}

// gb28181ADTSHeader 由 AudioSpecificConfig 生成 ADTS 模板（长度域每帧填写）。
// 返回 (音频时钟, 7 字节模板, error)。无需转码：AAC 直封（方案 §5.6）。
func gb28181ADTSHeader(asc []byte, clock int) (int, [7]byte, error) {
	var h [7]byte
	if len(asc) < 2 {
		return 0, h, errors.New("AAC 源缺少 AudioSpecificConfig（CodecConfig）")
	}
	aot := int(asc[0] >> 3)
	if aot < 1 {
		aot = 2 // 缺省按 AAC-LC 处理
	}
	sfIdx := int((asc[0]&0x07)<<1 | asc[1]>>7)
	chanCfg := int((asc[1] >> 3) & 0x0F)

	sampleRates := []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
	sr := 44100
	if sfIdx < len(sampleRates) {
		sr = sampleRates[sfIdx]
	}
	if clock <= 0 {
		clock = sr
	}

	h[0] = 0xFF
	h[1] = 0xF1 // MPEG-4 / layer 0 / no CRC
	h[2] = byte((aot-1)&0x03)<<6 | byte(sfIdx&0x0F)<<2 | byte((chanCfg>>2)&0x01)
	h[3] = byte(chanCfg&0x03) << 6 // 长度高 2 位每帧补齐
	h[6] = 0xFC                    // buffer fullness 0x7FF + raw blocks 0
	logger.Debugf("gb28181: ADTS template ready (sample_rate=%d channels=%d)", sr, chanCfg)
	return clock, h, nil
}
