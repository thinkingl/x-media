package media

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"

	"github.com/x-media/x-media-server/pkg/logger"
)

// MP4Source 将本地 MP4 文件转换为标准帧（数据面）并响应信令（控制面）。
//
// 每个媒体 track 对应一个子通道（ChannelID），PTS/DTS 使用 track 的流内
// timescale（即 StreamInfo.ClockRate）。视频 sample 由 AVCC 转 AnnexB，
// 音频 sample 保持原始 AAC 帧（raw）。
type MP4Source struct {
	mu sync.RWMutex

	id       string
	path     string
	loop     bool
	status   StreamStatus
	handler  FrameHandler
	handlers []FrameHandler

	// 时间戳网格化重打：Video/Audio 独立开关。
	gridVideo bool
	gridAudio bool

	file    *os.File
	mp4f    *mp4.File
	streams []StreamInfo
	tracks  []*mp4Track
	baseTr  *mp4Track // 基准 track（时长最长者），循环回绕以它为准
	cancel  context.CancelFunc
	done    chan struct{} // readLoop 退出信号

	throttleOverride time.Duration // 测试辅助：>0 时覆盖默认节流间隔，加速驱动回绕

	// 无漂移节流锚点：以基准 track 媒体时间轴锚定单调墙钟，
	// 媒体时间轴相对墙钟跳变（Start/Seek/Resume）时重新锚定。
	paceAnchor      time.Time
	paceAnchorMedia int64
	paceAnchored    bool
}

// mp4Track 单个媒体轨道的运行时状态。
type mp4Track struct {
	info      StreamInfo
	trak      *mp4.TrakBox
	timescale uint32
	sampleNr  uint32
	play      bool // 是否允许发送（Pause 时为 false）
	isVideo   bool
	isBase    bool  // 是否为基准 track（时长最长者），决定循环回绕节奏
	loopDur   int64 // 已累计的循环偏移（自身 timescale），保证循环时时间戳单调递增

	// 时间戳网格化重打：开启后该 track 的帧时间戳按固定间隔单调递增
	// （不再跟随源 stts），消除 VFR/停滞/跳变造成的时间戳不规则。
	gridEnabled bool  // 本 track 是否开启网格化
	gridTS      int64 // 当前帧的网格时间戳（流内 timescale）
	gridStep    int64 // 每帧固定步进（流内 timescale）
}

func NewMP4Source(config *InputConfig) (*MP4Source, error) {
	if config.Path == "" {
		return nil, ErrInvalidConfig
	}
	id := config.ID
	if id == "" {
		id = "mp4_" + config.Path
	}
	var gridVideo, gridAudio bool
	if config.TimestampGrid != nil {
		gridVideo = config.TimestampGrid.Video
		gridAudio = config.TimestampGrid.Audio
	}
	return &MP4Source{
		id:        id,
		path:      config.Path,
		loop:      config.Loop,
		status:    StreamStatusStopped,
		gridVideo: gridVideo,
		gridAudio: gridAudio,
	}, nil
}

func (m *MP4Source) ID() string { return m.id }

func (m *MP4Source) Status() StreamStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// Start 打开 MP4 文件、解析轨道并启动读循环。
func (m *MP4Source) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.status == StreamStatusRunning {
		return nil
	}

	if !filepath.IsAbs(m.path) {
		if abs, err := filepath.Abs(m.path); err == nil {
			m.path = abs
		}
	}

	if err := ValidateFilePath(m.path); err != nil {
		m.status = StreamStatusError
		return err
	}

	f, err := os.Open(m.path)
	if err != nil {
		m.status = StreamStatusError
		return fmt.Errorf("open mp4: %w", err)
	}

	mp4f, err := mp4.DecodeFile(f, mp4.WithDecodeMode(mp4.DecModeLazyMdat))
	if err != nil {
		f.Close()
		m.status = StreamStatusError
		return fmt.Errorf("parse mp4: %w", err)
	}
	if mp4f.IsFragmented() {
		f.Close()
		m.status = StreamStatusError
		return fmt.Errorf("fragmented mp4 not supported: %s", m.path)
	}

	streams, tracks, err := buildMP4Tracks(mp4f)
	if err != nil {
		f.Close()
		m.status = StreamStatusError
		return err
	}

	// 时间戳网格化重打：按 track 计算固定步进（网格间隔）。
	//   - 视频：timescale / fps（名义帧率），fps 不可用时用中位 stts 间隔兜底
	//   - 音频：AAC 固定 1024 采样/帧（timescale=采样率时即 1024）
	applyTimestampGrid(tracks, m.gridVideo, m.gridAudio)

	ctx, m.cancel = context.WithCancel(ctx)
	m.file = f
	m.mp4f = mp4f
	m.streams = streams
	m.tracks = tracks
	m.baseTr = selectBaseTrack(tracks)
	m.done = make(chan struct{})
	m.status = StreamStatusRunning

	go m.readLoop(ctx)

	logger.Infof("MP4 source started: %s, file: %s, tracks: %d", m.id, m.path, len(tracks))
	return nil
}

// buildMP4Tracks 从 mp4 文件提取轨道信息并初始化 sample 迭代状态。
func buildMP4Tracks(mp4f *mp4.File) ([]StreamInfo, []*mp4Track, error) {
	var streams []StreamInfo
	var tracks []*mp4Track

	channelID := uint8(0)
	for _, trak := range mp4f.Moov.Traks {
		if trak.Mdia == nil || trak.Mdia.Hdlr == nil || trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil {
			continue
		}

		handlerType := trak.Mdia.Hdlr.HandlerType
		stbl := trak.Mdia.Minf.Stbl
		info := StreamInfo{
			ChannelID:  channelID,
			Parameters: make(map[string]any),
		}
		if trak.Mdia.Mdhd != nil {
			info.ClockRate = int(trak.Mdia.Mdhd.Timescale)
		}

		tr := &mp4Track{trak: trak, sampleNr: 1, play: true}
		if trak.Mdia.Mdhd != nil {
			tr.timescale = trak.Mdia.Mdhd.Timescale
		}

		switch handlerType {
		case "vide":
			info.Kind = "video"
			tr.isVideo = true
			if stbl.Stsd != nil {
				if avc := stbl.Stsd.AvcX; avc != nil {
					info.CodecID = CodecH264
					info.CodecName = "H264"
					info.Parameters["width"] = int(avc.Width)
					info.Parameters["height"] = int(avc.Height)
					info.Parameters["fps"] = videoFPS(trak)
					if avc.AvcC != nil {
						info.CodecConfig = extractAVCDecConfRec(&avc.AvcC.DecConfRec)
					}
				} else if hvc := stbl.Stsd.HvcX; hvc != nil {
					info.CodecID = CodecH265
					info.CodecName = "H265"
					info.Parameters["width"] = int(hvc.Width)
					info.Parameters["height"] = int(hvc.Height)
					info.Parameters["fps"] = videoFPS(trak)
					if hvc.HvcC != nil {
						info.CodecConfig = extractHvcCDecConfRec(&hvc.HvcC.DecConfRec)
					}
				} else {
					continue
				}
			}
		case "soun":
			info.Kind = "audio"
			info.CodecID = CodecAAC
			info.CodecName = "AAC"
			if stbl.Stsd != nil && stbl.Stsd.Mp4a != nil {
				a := stbl.Stsd.Mp4a
				info.Parameters["channels"] = int(a.ChannelCount)
				info.Parameters["sample_rate"] = int(a.SampleRate)
				if a.Esds != nil && a.Esds.DecConfigDescriptor != nil && a.Esds.DecConfigDescriptor.DecSpecificInfo != nil {
					info.CodecConfig = a.Esds.DecConfigDescriptor.DecSpecificInfo.DecConfig
				}
			}
		default:
			// 字幕/元数据等轨道暂不支持
			continue
		}

		tr.info = info
		streams = append(streams, info)
		tracks = append(tracks, tr)
		channelID++
	}

	if len(tracks) == 0 {
		return nil, nil, fmt.Errorf("no supported media tracks found")
	}
	return streams, tracks, nil
}

// applyTimestampGrid 为各 track 计算网格化步进并开启网格模式（按配置）。
// 开启后帧时间戳不再跟随源 stts，而是按固定步进单调递增：
//   - 视频步进 = timescale/名义fps（fps 不可用时取 stts 中位间隔）
//   - 音频步进 = AAC 每帧采样数（timescale=采样率时即 1024）
func applyTimestampGrid(tracks []*mp4Track, gridVideo, gridAudio bool) {
	for _, tr := range tracks {
		if tr.isVideo && gridVideo {
			step := gridStepForVideo(tr)
			if step > 0 {
				tr.gridEnabled = true
				tr.gridStep = step
				tr.gridTS = 0
			}
		} else if !tr.isVideo && gridAudio {
			tr.gridEnabled = true
			tr.gridStep = aacSamplesPerFrame
			tr.gridTS = 0
		}
	}
}

// gridStepForVideo 计算视频轨道网格步进：优先按名义帧率（timescale/fps），
// 不可用时取 stts 中位间隔，保证步进贴近源平均节奏。
func gridStepForVideo(tr *mp4Track) int64 {
	if tr.trak == nil || tr.trak.Mdia == nil || tr.trak.Mdia.Minf == nil || tr.trak.Mdia.Minf.Stbl == nil || tr.trak.Mdia.Minf.Stbl.Stts == nil {
		return 0
	}
	ts := int64(tr.timescale)
	if ts <= 0 {
		ts = 90000
	}
	if fps := videoFPS(tr.trak); fps > 0 {
		if step := int64(float64(ts)/fps + 0.5); step > 0 {
			return step
		}
	}
	return medianSttsDelta(tr.trak.Mdia.Minf.Stbl.Stts)
}

// medianSttsDelta 返回 stts 表的中位采样间隔（流内 timescale）；空表返回 0。
func medianSttsDelta(stts *mp4.SttsBox) int64 {
	if stts == nil || len(stts.SampleCount) == 0 {
		return 0
	}
	total := uint64(0)
	for _, c := range stts.SampleCount {
		total += uint64(c)
	}
	if total == 0 {
		return 0
	}
	// 找出中位数所在条目
	mid := (total + 1) / 2
	acc := uint64(0)
	for i, c := range stts.SampleCount {
		acc += uint64(c)
		if acc >= mid {
			return int64(stts.SampleTimeDelta[i])
		}
	}
	return int64(stts.SampleTimeDelta[len(stts.SampleTimeDelta)-1])
}

// trackAbsDuration 返回轨道绝对时长（纳秒量纲），用于跨 timescale 比较时长。
func (tr *mp4Track) trackAbsDuration() int64 {
	ts := tr.timescale
	if ts == 0 {
		ts = 90000
	}
	// 用 1MHz 统一量纲比较，避免不同 timescale 直接比较
	return ConvertClock(tr.totalDuration(), int(ts), 1000000)
}

// selectBaseTrack 选出绝对时长最长的 track 作为基准。
func selectBaseTrack(tracks []*mp4Track) *mp4Track {
	var base *mp4Track
	var maxDur int64 = -1
	for _, tr := range tracks {
		d := tr.trackAbsDuration()
		if d > maxDur {
			maxDur = d
			base = tr
		}
	}
	if base != nil {
		base.isBase = true
	}
	return base
}

// emitNext 推进 tr 一个 sample 并发出。返回是否产帧、是否读完。
func (m *MP4Source) emitNext(tr *mp4Track, ws []byte, tick *int, first *bool) (emitted, done bool, err error) {
	f, done, err := m.nextSample(tr)
	if done || err != nil {
		return false, done, err
	}
	if f == nil {
		return false, false, nil
	}
	data, err := m.readSampleData(tr, ws)
	if err != nil {
		return false, false, err
	}
	if f.Header.FrameType == FrameTypeVideo {
		data = avccToAnnexB(data)
	}
	f.Payload = data
	f.Header.PayloadLen = uint32(len(data))
	*tick++
	if *first {
		logger.Infof("MP4 source first frame [%s] type=%d ch=%d pts=%d size=%d", m.id, f.Header.FrameType, f.Header.ChannelID, f.Header.PTS, len(data))
		*first = false
	}
	if *tick%300 == 0 {
		logger.Infof("MP4 source heartbeat [%s] frames=%d", m.id, *tick)
	}
	m.emit(f)
	return true, false, nil
}

// trackDecodeTime 返回 track 指定 sample 的流内解码时间：
// 网格模式下按 帧数×固定步进 计算（与源 stts 无关），否则用源 stts。
func (tr *mp4Track) trackDecodeTime(sampleNr uint32) uint64 {
	if tr.gridEnabled {
		return uint64(int64(sampleNr-1) * tr.gridStep)
	}
	if tr.trak == nil || tr.trak.Mdia == nil || tr.trak.Mdia.Minf == nil || tr.trak.Mdia.Minf.Stbl == nil || tr.trak.Mdia.Minf.Stbl.Stts == nil {
		return 0
	}
	dts, _ := tr.trak.Mdia.Minf.Stbl.Stts.GetDecodeTime(sampleNr)
	return dts
}

// peekMediaAbs 返回 tr 下一个 sample 的绝对媒体时间（µs）；已读完或无 stts 返回 -1。
// 用于非基准 track 按各自帧率对齐补发。
func (m *MP4Source) peekMediaAbs(tr *mp4Track) int64 {
	m.mu.RLock()
	trak := tr.trak
	sampleNr := tr.sampleNr
	loopDur := tr.loopDur
	ts := int(tr.timescale)
	m.mu.RUnlock()
	if trak == nil || trak.Mdia == nil || trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil {
		return -1
	}
	if sampleNr > trak.GetNrSamples() {
		return -1
	}
	if !tr.gridEnabled && trak.Mdia.Minf.Stbl.Stts == nil {
		return -1
	}
	dts := tr.trackDecodeTime(sampleNr)
	media := int64(dts) + loopDur
	if ts <= 0 {
		ts = 90000
	}
	return ConvertClock(media, ts, 1000000)
}

// readLoop 逐轨道按各自帧率推进并推送帧：基准轨道每迭代一个 sample 驱动节流，
// 其他轨道按媒体时间对齐补发，使音视频各按自身帧率平滑输出（避免同频突发/停顿）。
func (m *MP4Source) readLoop(ctx context.Context) {
	defer close(m.done)
	ws := make([]byte, 0, 512*1024)
	baseDone := false
	var tick int
	var first bool = true
	exitReason := ""

	defer func() {
		logger.Infof("MP4 source readLoop exited [%s] reason=%q frames=%d", m.id, exitReason, tick)
	}()

	for {
		select {
		case <-ctx.Done():
			exitReason = "context cancelled (Stop called)"
			return
		default:
		}

		m.mu.RLock()
		tracks := m.tracks
		base := m.baseTr
		m.mu.RUnlock()

		if len(tracks) == 0 {
			exitReason = "no tracks"
			return
		}

		m.mu.RLock()
		anchored := m.paceAnchored
		m.mu.RUnlock()
		if !anchored {
			m.reAnchorPacing()
		}

		baseDone = false
		emitted := false

		// 迭代时钟：所有 track 各自的下一帧绝对媒体时间（µs），选最早者驱动本迭代。
		// 每个 track 按自己的时间戳节奏推进：音视频不再"视频时钟捆绑突发"，
		// 而是各自到点即发，避免音频帧攒批随视频帧一起发出导致接收端抖动。
		// 找所有 track 中"下一帧最早"的 track 作为本迭代的发送候选。
		var nextTr *mp4Track
		nextMedia := int64(-1)
		for _, tr := range tracks {
			m.mu.RLock()
			play := tr.play
			m.mu.RUnlock()
			if !play {
				continue
			}
			media := m.peekMediaAbs(tr)
			if media < 0 {
				continue
			}
			if nextMedia < 0 || media < nextMedia {
				nextMedia = media
				nextTr = tr
			}
		}

		if nextTr != nil {
			tr := nextTr
			e, done, err := m.emitNext(tr, ws, &tick, &first)
			if err != nil {
				logger.Errorf("MP4 source read error [%s]: %v", m.id, err)
				exitReason = "nextSample error: " + err.Error()
				return
			}
			if done && tr == base {
				baseDone = true
			}
			emitted = emitted || e
			// 采用"每迭代发最早帧"策略后，音频与视频交错按各自时钟发出，无需再补发。
		} else {
			// 所有 track 的 peek 均返回 -1（已读完或暂停）：若基准 track 已读完则触发回绕。
			m.mu.RLock()
			baseDone = base != nil && base.sampleNr > base.trak.GetNrSamples()
			m.mu.RUnlock()
		}

		if !emitted && !baseDone {
			m.mu.RLock()
			var st []string
			for _, tr := range tracks {
				st = append(st, fmt.Sprintf("%s:play=%v nr=%d total=%d", tr.info.Kind, tr.play, tr.sampleNr, tr.trak.GetNrSamples()))
			}
			m.mu.RUnlock()
			logger.Warnf("MP4 source idle [%s] baseDone=%v tracks=%v", m.id, baseDone, st)
		}

		if baseDone {
			if !m.loop {
				exitReason = "end of file (loop disabled)"
				return
			}
			logger.Infof("MP4 source loop wrap [%s]", m.id)
			m.alignLoopDuration()
		}

		// 节流：模拟实时媒体节奏，避免忙转与瞬间读完文件。
		// 按全局"下一帧最早媒体时间"推进（音视频各自时间戳节奏），
		// 暂停/无产帧时退化为固定间隔轮询，避免忙转。
		if emitted {
			if m.throttleOverride > 0 {
				select {
				case <-ctx.Done():
					exitReason = "context cancelled (Stop called)"
					return
				case <-time.After(m.throttleOverride):
				}
				continue
			}
			if target := m.nextPaceTarget(); !target.IsZero() {
				if d := time.Until(target); d > 0 {
					select {
					case <-ctx.Done():
						exitReason = "context cancelled (Stop called)"
						return
					case <-time.After(d):
					}
				}
			}
			continue
		}
		select {
		case <-ctx.Done():
			exitReason = "context cancelled (Stop called)"
			return
		case <-time.After(m.throttle()):
		}
	}
}

// nextPaceTarget 基于所有 track 中"下一帧最早媒体时间"计算墙钟调度目标。
// 与 paceTarget 不同：不再只用视频基准 track，而是取音视频各自下一帧的最早者，
// 使音频按其自身时钟（11.4ms）与视频（40ms）交错匀速发送，避免音频攒批突发。
func (m *MP4Source) nextPaceTarget() time.Time {
	m.mu.RLock()
	base := m.baseTr
	if base == nil || base.trak == nil || base.trak.Mdia == nil ||
		base.trak.Mdia.Minf == nil || base.trak.Mdia.Minf.Stbl == nil ||
		base.trak.Mdia.Minf.Stbl.Stts == nil || !m.paceAnchored {
		m.mu.RUnlock()
		return time.Time{}
	}
	baseTS := int64(base.timescale)
	if baseTS <= 0 {
		baseTS = 90000
	}
	// 收集各 track 下一帧媒体时间（µs）与 timescale
	type cand struct {
		media int64 // µs
		ts    int64
	}
	var cands []cand
	for _, tr := range m.tracks {
		if tr.trak == nil || tr.trak.Mdia == nil || tr.trak.Mdia.Minf == nil || tr.trak.Mdia.Minf.Stbl == nil || tr.trak.Mdia.Minf.Stbl.Stts == nil {
			continue
		}
		sampleNr := tr.sampleNr
		loopDur := tr.loopDur
		ts := int64(tr.timescale)
		if ts <= 0 {
			ts = 90000
		}
		if sampleNr > tr.trak.GetNrSamples() {
			continue
		}
		dts := tr.trackDecodeTime(sampleNr)
		media := int64(dts) + loopDur
		cands = append(cands, cand{media: ConvertClock(media, int(ts), 1000000), ts: ts})
	}
	anchor := m.paceAnchor
	anchorMedia := m.paceAnchorMedia
	m.mu.RUnlock()

	// 取最早者，换算到视频 timescale 与锚点对齐
	nextMedia := int64(-1)
	for _, c := range cands {
		mib := ConvertClock(c.media, 1000000, int(baseTS))
		if nextMedia < 0 || mib < nextMedia {
			nextMedia = mib
		}
	}
	if nextMedia < 0 || nextMedia <= anchorMedia {
		return time.Time{}
	}
	return anchor.Add(time.Duration((nextMedia - anchorMedia) * int64(time.Second) / baseTS))
}

// nextSample 读取下一个 sample 的元数据并构造帧头。done=true 表示该轨道已读完。
func (m *MP4Source) nextSample(tr *mp4Track) (f *Frame, done bool, err error) {
	m.mu.RLock()
	trak := tr.trak
	sampleNr := tr.sampleNr
	loopDur := tr.loopDur
	m.mu.RUnlock()

	total := trak.GetNrSamples()
	if sampleNr > total {
		return nil, true, nil
	}

	dts := tr.trackDecodeTime(sampleNr)
	pts := int64(dts) + loopDur
	if tr.isVideo {
		if ctts := trak.Mdia.Minf.Stbl.Ctts; ctts != nil {
			pts = int64(int64(dts) + int64(ctts.GetCompositionTimeOffset(sampleNr)) + loopDur)
		}
	}

	isKey := false
	if tr.isVideo {
		if stss := trak.Mdia.Minf.Stbl.Stss; stss != nil {
			isKey = stss.IsSyncSample(sampleNr)
		}
	}

	m.mu.Lock()
	tr.sampleNr++
	m.mu.Unlock()

	ft := FrameTypeAudio
	codec := tr.info.CodecID
	if tr.isVideo {
		ft = FrameTypeVideo
	}
	return &Frame{
		Header: FrameHeader{
			Magic:     FrameMagic,
			Version:   FrameVersion,
			ChannelID: tr.info.ChannelID,
			FrameType: ft,
			Codec:     codec,
			Flags:     frameFlags(isKey, false),
			PTS:       pts,
			DTS:       int64(dts) + loopDur,
		},
		Payload: nil,
	}, false, nil
}

// readSampleData 读取当前 sample 的原始字节（AVCC 或 raw AAC）。
func (m *MP4Source) readSampleData(tr *mp4Track, ws []byte) ([]byte, error) {
	m.mu.RLock()
	trak := tr.trak
	sampleNr := tr.sampleNr - 1
	m.mu.RUnlock()
	if sampleNr < 1 {
		sampleNr = 1
	}

	w := newBytesWriter()
	if err := m.mp4f.CopySampleData(w, m.file, trak, sampleNr, sampleNr, ws); err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

// throttle 返回帧推进间隔。默认按视频帧率节奏，视频缺失时退化为固定间隔。
func (m *MP4Source) throttle() time.Duration {
	m.mu.RLock()
	ov := m.throttleOverride
	m.mu.RUnlock()
	if ov > 0 {
		return ov
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, tr := range m.tracks {
		if tr.info.Kind == "video" {
			rate := tr.info.ClockRate
			if rate <= 0 {
				rate = 90000
			}
			if fps, ok := tr.info.Parameters["fps"].(float64); ok && fps > 0 {
				return time.Duration(float64(time.Second) / fps)
			}
			// 无 fps 元数据时按 1/30s 兜底
			return time.Second / 30
		}
	}
	return 5 * time.Millisecond
}

// paceTarget 返回下一帧的墙钟调度目标（无漂移节流）。
// 以基准 track 的媒体时间轴（dts+loopDur，自身 timescale）锚定单调墙钟，
// 使发送节奏与媒体时间戳精确一致，消除固定 sleep + 处理开销造成的累计漂移。
// 返回零值表示当前无法调度（暂停/无样本/无基准轨道）。
func (m *MP4Source) paceTarget() time.Time {
	m.mu.RLock()
	base := m.baseTr
	if base == nil || base.trak == nil || base.trak.Mdia == nil ||
		base.trak.Mdia.Minf == nil || base.trak.Mdia.Minf.Stbl == nil ||
		base.trak.Mdia.Minf.Stbl.Stts == nil {
		m.mu.RUnlock()
		return time.Time{}
	}
	sampleNr := base.sampleNr
	loopDur := base.loopDur
	ts := int64(base.timescale)
	anchor := m.paceAnchor
	anchorMedia := m.paceAnchorMedia
	m.mu.RUnlock()
	if ts <= 0 {
		ts = 90000
	}
	if sampleNr > base.trak.GetNrSamples() {
		return time.Time{}
	}
	stts := base.trak.Mdia.Minf.Stbl.Stts
	if stts == nil {
		return time.Time{}
	}
	dts := base.trackDecodeTime(sampleNr)
	media := int64(dts) + loopDur
	if media <= anchorMedia {
		return time.Time{}
	}
	return anchor.Add(time.Duration((media - anchorMedia) * int64(time.Second) / ts))
}

// reAnchorPacing 在媒体时间轴相对墙钟跳变后重置无漂移节流锚点，
// 避免按旧锚点休眠造成长时间等待或突发追赶。Start/Seek/Resume 时调用。
func (m *MP4Source) reAnchorPacing() {
	m.mu.Lock()
	defer m.mu.Unlock()
	base := m.baseTr
	if base == nil || base.trak == nil || base.trak.Mdia == nil ||
		base.trak.Mdia.Minf == nil || base.trak.Mdia.Minf.Stbl == nil ||
		base.trak.Mdia.Minf.Stbl.Stts == nil {
		return
	}
	media := base.loopDur
	if nr := base.sampleNr; nr <= base.trak.GetNrSamples() {
		if dts := base.trackDecodeTime(nr); dts > 0 {
			media += int64(dts)
		}
	}
	m.paceAnchor = time.Now()
	m.paceAnchorMedia = media
	m.paceAnchored = true
}

// videoFPS 从 stts 推导平均帧率。
func videoFPS(trak *mp4.TrakBox) float64 {
	stts := trak.Mdia.Minf.Stbl.Stts
	if stts == nil || trak.GetNrSamples() == 0 {
		return 0
	}
	total := int64(0)
	for i, c := range stts.SampleCount {
		total += int64(c) * int64(stts.SampleTimeDelta[i])
	}
	if total <= 0 {
		return 0
	}
	timescale := uint32(90000)
	if trak.Mdia.Mdhd != nil {
		timescale = trak.Mdia.Mdhd.Timescale
	}
	return float64(trak.GetNrSamples()) * float64(timescale) / float64(total)
}

// resetTracks 循环回绕：所有轨道回到第一个 sample。
func (m *MP4Source) resetTracks() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tr := range m.tracks {
		tr.sampleNr = 1
	}
}

// alignLoopDuration 统一基准循环回绕：
//   - 基准 track（绝对时长最长者）读完触发回绕，其 loopDur 累加自身时长；
//   - 其他 track 的 loopDur 换算为与基准 track 相同的绝对时间起点，
//     保证回绕后首帧时间戳落在同一绝对时间轴，音视频不漂移。
func (m *MP4Source) alignLoopDuration() {
	m.mu.Lock()
	defer m.mu.Unlock()

	base := m.baseTr
	if base == nil || len(m.tracks) == 0 {
		return
	}

	// 基准 track 累加一圈
	base.loopDur += base.totalDuration()
	base.sampleNr = 1

	baseTS := int(base.timescale)
	if baseTS <= 0 {
		baseTS = 90000
	}
	// 基准 track 当前圈的绝对时间起点（基准 timescale）
	baseStart := base.loopDur

	for _, tr := range m.tracks {
		if tr == base {
			continue
		}
		ts := int(tr.timescale)
		if ts <= 0 {
			ts = 90000
		}
		// 将基准绝对时间起点换算到本 track timescale，作为本 track 的循环偏移。
		tr.loopDur = ConvertClock(baseStart, baseTS, ts)
		tr.sampleNr = 1
	}
}

// totalDuration 返回轨道总时长（流内 timescale）。
// 网格模式下按 帧数×固定步进 计算，保证循环回绕后的网格时间轴连续。
func (tr *mp4Track) totalDuration() int64 {
	if tr.gridEnabled && tr.gridStep > 0 {
		return int64(tr.trak.GetNrSamples()) * tr.gridStep
	}
	stts := tr.trak.Mdia.Minf.Stbl.Stts
	if stts == nil {
		return 0
	}
	var total int64
	for i, c := range stts.SampleCount {
		total += int64(c) * int64(stts.SampleTimeDelta[i])
	}
	return total
}

func (m *MP4Source) Stop() error {
	m.mu.Lock()
	if m.status == StreamStatusStopped {
		m.mu.Unlock()
		return nil
	}

	if m.cancel != nil {
		m.cancel()
	}
	done := m.done
	m.mu.Unlock()

	// 等待 readLoop 退出，避免文件句柄被并发关闭。
	if done != nil {
		<-done
	}

	m.mu.Lock()
	if m.file != nil {
		m.file.Close()
		m.file = nil
	}
	m.status = StreamStatusStopped
	m.mu.Unlock()
	logger.Infof("MP4 source stopped: %s", m.id)
	return nil
}

func (m *MP4Source) Streams() ([]StreamInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.streams, nil
}

// Subscribe 返回当前媒体信息。
func (m *MP4Source) Subscribe(_ context.Context, req *SubscribeRequest) (*SubscribeResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &SubscribeResponse{Streams: m.streams}, nil
}

func (m *MP4Source) Unsubscribe(_ context.Context, channels []uint8) error {
	return nil
}

// Signal 处理控制信令。
func (m *MP4Source) Signal(ctx context.Context, sig *Signal) (*Signal, error) {
	switch sig.Type {
	case SignalStart, SignalResume:
		m.setPlay(true)
		return NewReply(sig, nil)
	case SignalPause:
		m.setPlay(false)
		return NewReply(sig, nil)
	case SignalSeek:
		var req SeekRequest
		if err := sig.DecodePayload(&req); err != nil {
			return nil, err
		}
		m.seek(req.ChannelID, req.PTS)
		return NewReply(sig, nil)
	case SignalStop:
		m.setPlay(false)
		return NewReply(sig, nil)
	case SignalGetStreamInfo:
		return NewReply(sig, &SubscribeResponse{Streams: m.streams})
	default:
		return NewReply(sig, nil)
	}
}

func (m *MP4Source) setPlay(play bool) {
	m.mu.Lock()
	for _, tr := range m.tracks {
		tr.play = play
	}
	reAnchor := play && m.paceAnchored
	m.mu.Unlock()
	// Resume/Start 时媒体时间轴相对墙钟已停留，重新锚定避免突发追赶。
	if reAnchor {
		m.reAnchorPacing()
	}
}

// seek 将指定子通道定位到 PTS。
func (m *MP4Source) seek(channelID uint8, pts int64) {
	m.mu.Lock()
	for _, tr := range m.tracks {
		if tr.info.ChannelID != channelID {
			continue
		}
		nr, err := tr.trak.Mdia.Minf.Stbl.Stts.GetSampleNrAtTime(uint64(pts))
		if err != nil {
			tr.sampleNr = 1
			continue
		}
		// 视频回退到最近关键帧
		if tr.isVideo {
			if stss := tr.trak.Mdia.Minf.Stbl.Stss; stss != nil {
				for n := nr; n >= 1; n-- {
					if stss.IsSyncSample(n) {
						nr = n
						break
					}
				}
			}
		}
		tr.sampleNr = nr
		tr.play = true
	}
	m.mu.Unlock()
	// 跳转后媒体时间轴相对墙钟前跳，重新锚定避免按旧锚点长时间休眠。
	m.reAnchorPacing()
}

func (m *MP4Source) SetFrameHandler(h FrameHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = h
}

func (m *MP4Source) AddFrameHandler(h FrameHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers = append(m.handlers, h)
}

// emit 将帧派发给所有已注册的帧回调。
func (m *MP4Source) emit(f *Frame) {
	m.mu.RLock()
	hs := m.handlers
	single := m.handler
	m.mu.RUnlock()
	if single != nil {
		single(f)
	}
	for _, h := range hs {
		h(f)
	}
}

// ---- 辅助 ----

func frameFlags(isKey, isConfig bool) FrameFlag {
	var fl FrameFlag
	if isKey {
		fl |= FlagKeyframe
	}
	if isConfig {
		fl |= FlagConfig
	}
	return fl
}

// avccToAnnexB 将 AVCC(length-prefixed) 转 Annex B(start code)。
func avccToAnnexB(data []byte) []byte {
	out := make([]byte, 0, len(data)*2)
	i := 0
	for i+4 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[i : i+4]))
		i += 4
		if n <= 0 || i+n > len(data) {
			break
		}
		out = append(out, 0x00, 0x00, 0x00, 0x01)
		out = append(out, data[i:i+n]...)
		i += n
	}
	return out
}

// bytesWriter 最小化 CopySampleData 目标 writer。
type bytesWriter struct {
	buf []byte
}

func newBytesWriter() *bytesWriter {
	return &bytesWriter{buf: make([]byte, 0, 512*1024)}
}

func (b *bytesWriter) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *bytesWriter) Bytes() []byte { return b.buf }

// extractAVCDecConfRec 从 AVCDecoderConfigurationRecord 提取 SPS/PPS NAL。
func extractAVCDecConfRec(rec *avc.DecConfRec) []byte {
	var out []byte
	for _, sps := range rec.SPSnalus {
		out = appendAnnexBNAL(out, sps)
	}
	for _, pps := range rec.PPSnalus {
		out = appendAnnexBNAL(out, pps)
	}
	return out
}

func extractHvcCDecConfRec(rec *hevc.DecConfRec) []byte {
	var out []byte
	out = appendAnnexBNALs(out, rec.GetNalusForType(hevc.NALU_VPS))
	out = appendAnnexBNALs(out, rec.GetNalusForType(hevc.NALU_SPS))
	out = appendAnnexBNALs(out, rec.GetNalusForType(hevc.NALU_PPS))
	return out
}

func appendAnnexBNALs(dst []byte, nals [][]byte) []byte {
	for _, nal := range nals {
		dst = appendAnnexBNAL(dst, nal)
	}
	return dst
}

func appendAnnexBNAL(dst, nal []byte) []byte {
	dst = append(dst, 0x00, 0x00, 0x00, 0x01)
	return append(dst, nal...)
}
