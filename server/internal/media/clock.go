package media

// ClockRate 换算工具。
//
// 标准帧内 PTS/DTS 使用流内原生 timescale（video 默认 90000，audio = 采样率）。
// 各 sink 需要按目标时钟换算：
//   - RTMP/HTTP-FLV tag 时间戳（ms）  = ConvertClock(pts, rate, 1000)
//   - RTSP H.264 RTP 时间戳（90kHz）   = ConvertClock(pts, rate, 90000)
//   - RTSP AAC RTP 时间戳（采样率）    = ConvertClock(pts, rate, sampleRate)
func ConvertClock(pts int64, fromRate, toRate int) int64 {
	if fromRate <= 0 {
		return pts
	}
	if toRate <= 0 {
		return 0
	}
	// 先乘再除，保持精度；int64 溢出由调用方按媒体时长规避（本期文件均远小于上限）。
	return pts * int64(toRate) / int64(fromRate)
}

// ToMilliseconds 将流内时间戳换算为毫秒（RTMP/HTTP-FLV tag 时间戳）。
func ToMilliseconds(pts int64, clockRate int) int64 {
	return ConvertClock(pts, clockRate, 1000)
}

// To90k 将流内时间戳换算为 90kHz（RTSP 视频 RTP 时间戳）。
func To90k(pts int64, clockRate int) int64 {
	return ConvertClock(pts, clockRate, 90000)
}
