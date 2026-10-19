package media

// mp4_source_pace_overflow_test.go —— 节流锚点 int64 溢出回归测试（2026-10-19）
//
// 缺陷：nextPaceTarget() 用
//     anchor.Add(time.Duration((nextMedia - anchorMedia) * int64(time.Second) / baseTS))
// 计算目标墙钟时刻。(nextMedia-anchorMedia) 是媒体时间刻度差（例如 90000Hz），
// 乘以 1e9（纳秒）在媒体时长约 28.5 小时（= int64 上限 / 1e9 / 90000）后溢出为
// 负数，time.Duration 随之变成负值，target 落在过去 → time.Until(target) <= 0
// → readLoop 不睡眠 → 全速忙转（实测 4 天实例：视频 6278fps、CPU 140%）。
//
// 该测试把锚点人为移到"很久以前"，模拟长跑后的媒体时间差，断言
// nextPaceTarget 仍返回未来时刻且落在合理区间内。

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMP4SourcePaceTargetNoOverflow 长跑（媒体时间差跨越 int64 纳秒溢出点）后，
// 节流目标时刻必须仍是"未来且合理"的时刻，不得因溢出变成过去时刻。
func TestMP4SourcePaceTargetNoOverflow(t *testing.T) {
	path := testFixturePath(t, "../../test/fixtures/test.mp4")
	src, err := NewMP4Source(&InputConfig{
		ID:   "overflow_" + t.Name(),
		Type: "file",
		Path: path,
		Loop: true,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, src.Start(ctx))
	defer src.Stop()

	// 等首帧，确保轨道与锚点已就绪
	require.Eventually(t, func() bool {
		src.mu.RLock()
		defer src.mu.RUnlock()
		return src.paceAnchored && len(src.tracks) > 0
	}, 5*time.Second, 20*time.Millisecond, "锚点应已建立")

	// 模拟长跑 30 小时：锚点在 30 小时前建立（媒体时间=锚点值），
	// 此后媒体轴随播放累计到 30 小时刻度差 —— 这正是生产实例运行
	// 4 天后的真实状态（锚点固定、nextMedia 持续增长）。
	src.mu.Lock()
	base := src.baseTr
	require.NotNil(t, base)
	baseTS := int64(base.timescale)
	if baseTS <= 0 {
		baseTS = 90000
	}
	// 30 小时媒体时长对应的刻度差（超过 int64/1e9/baseTS 的溢出阈值 28.5h）
	shiftTicks := int64(30 * 3600 * baseTS)
	for _, tr := range src.tracks {
		tr.loopDur += shiftTicks
	}
	src.paceAnchor = time.Now().Add(-30 * time.Hour)
	// 注意：paceAnchorMedia 保持锚点建立时的值（不回拨），
	// 这样 (nextMedia - anchorMedia) 才等于 30 小时媒体刻度差。
	src.mu.Unlock()

	target := src.nextPaceTarget()
	require.False(t, target.IsZero(), "长跑后仍应能算出节流目标")

	now := time.Now()
	require.True(t, target.After(now),
		"节流目标必须在未来（溢出会使其落在过去 → 忙转）；实际 target=%v now=%v", target, now)

	// 单帧媒体步进（约 1/25s）换算后不应超过数秒
	require.Less(t, target.Sub(now), 10*time.Second,
		"节流目标不应远在未来（换算错误会放大间隔）")
}
