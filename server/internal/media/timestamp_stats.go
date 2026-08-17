package media

import (
	"math"
	"os"
	"sort"

	"github.com/Eyevinn/mp4ff/mp4"
)

// TimestampDeltaStats 单个 track 的时间戳 delta 统计（用于评估源时间戳质量）。
// delta 单位为流内 timescale（视频常为 90000，音频为采样率）。
type TimestampDeltaStats struct {
	Kind        string  `json:"kind"`         // video / audio
	Timescale   uint32  `json:"timescale"`    // 流内 timescale
	Frames      int     `json:"frames"`       // 采样总数
	MinDelta    int64   `json:"min_delta"`    // 最小帧间隔
	MaxDelta    int64   `json:"max_delta"`    // 最大帧间隔
	AvgDelta    float64 `json:"avg_delta"`    // 平均帧间隔
	MedianDelta int64   `json:"median_delta"` // 中位帧间隔
	StdDev      float64 `json:"std_dev"`      // 帧间隔标准差（抖动）
	Stalls      int     `json:"stalls"`       // 停滞次数（delta < 平均/2）
	Jumps       int     `json:"jumps"`        // 跳变次数（delta > 平均*2）
	JitterMs    float64 `json:"jitter_ms"`    // 抖动换算为毫秒
	Regular     bool    `json:"regular"`      // 是否恒等间隔（CFR 严格网格）
}

// AnalyzeMP4TimestampStats 解析 MP4 stts 表，统计每个音视频 track 的帧间隔
// delta 分布。非 MP4 文件或解析失败返回 nil（调用方忽略即可）。
func AnalyzeMP4TimestampStats(filePath string) []TimestampDeltaStats {
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	mp4f, err := mp4.DecodeFile(f, mp4.WithDecodeMode(mp4.DecModeLazyMdat))
	if err != nil {
		return nil
	}

	var stats []TimestampDeltaStats
	for _, trak := range mp4f.Moov.Traks {
		if trak.Mdia == nil || trak.Mdia.Hdlr == nil || trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil || trak.Mdia.Minf.Stbl.Stts == nil {
			continue
		}
		handlerType := trak.Mdia.Hdlr.HandlerType
		kind := ""
		switch handlerType {
		case "vide":
			kind = "video"
		case "soun":
			kind = "audio"
		default:
			continue
		}

		timescale := uint32(90000)
		if trak.Mdia.Mdhd != nil && trak.Mdia.Mdhd.Timescale > 0 {
			timescale = trak.Mdia.Mdhd.Timescale
		}

		s := analyzeSttsDelta(trak.Mdia.Minf.Stbl.Stts, timescale)
		s.Kind = kind
		stats = append(stats, s)
	}
	return stats
}

// analyzeSttsDelta 从 stts 表展开采样序列并统计 delta 分布。
func analyzeSttsDelta(stts *mp4.SttsBox, timescale uint32) TimestampDeltaStats {
	out := TimestampDeltaStats{
		Timescale: timescale,
	}
	var deltas []int64
	for i, c := range stts.SampleCount {
		d := int64(stts.SampleTimeDelta[i])
		for j := uint32(0); j < c; j++ {
			deltas = append(deltas, d)
		}
	}
	if len(deltas) == 0 {
		return out
	}

	out.Frames = len(deltas)
	sum := int64(0)
	out.MinDelta = deltas[0]
	out.MaxDelta = deltas[0]
	for _, d := range deltas {
		sum += d
		if d < out.MinDelta {
			out.MinDelta = d
		}
		if d > out.MaxDelta {
			out.MaxDelta = d
		}
	}
	out.AvgDelta = float64(sum) / float64(len(deltas))

	// 排序求中位数
	sorted := make([]int64, len(deltas))
	copy(sorted, deltas)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	out.MedianDelta = sorted[mid]
	if len(sorted)%2 == 0 && mid > 0 {
		out.MedianDelta = (sorted[mid-1] + sorted[mid]) / 2
	}

	// 标准差（抖动）
	avg := out.AvgDelta
	var sq float64
	for _, d := range deltas {
		diff := float64(d) - avg
		sq += diff * diff
	}
	out.StdDev = math.Sqrt(sq / float64(len(deltas)))

	// 停滞/跳变：相对平均间隔
	half := avg / 2
	twice := avg * 2
	for _, d := range deltas {
		if float64(d) < half {
			out.Stalls++
		}
		if float64(d) > twice {
			out.Jumps++
		}
	}

	out.JitterMs = out.StdDev * 1000 / float64(timescale)
	out.Regular = out.MinDelta == out.MaxDelta
	return out
}
