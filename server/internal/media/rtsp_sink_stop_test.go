package media

import (
	"testing"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRTSPSinkStopRemovesPath 复现 path 残留：
// Stop 此前不删除 handler.paths 条目 —— onPlay 闭包反向钉住 sink（泄漏），
// 且输出删除后 DESCRIBE/SETUP 仍返回 200 + nil stream。
func TestRTSPSinkStopRemovesPath(t *testing.T) {
	sink := newRTSPTestSink(t)
	require.NoError(t, sink.Configure(flvTestStreams()))
	path := "live/" + sink.ID()

	sink.handler.mutex.RLock()
	_, ok := sink.handler.paths[path]
	sink.handler.mutex.RUnlock()
	require.True(t, ok, "Configure 后 path 应注册")

	// Stop 前捕获 handler（Stop 会置 sink.handler=nil 并释放 server 引用）
	handler := sink.handler
	require.NoError(t, sink.Stop())

	handler.mutex.RLock()
	_, ok = handler.paths[path]
	handler.mutex.RUnlock()
	require.False(t, ok, "Stop 后 path 必须删除（残留钉住 sink 且让 DESCRIBE 返回 200+空 stream）")
}

// TestRTSPDescribeAfterStop404 验证 Stop 后信令返回 404 而非 200+nil stream。
// 注：SETUP 的 nil-stream 防御无法在不构造 gortsplib.ServerSession 的情况下
// 单测（OnSetup 入口即 ctx.Session.State()），其语义与 DESCRIBE 分支相同。
func TestRTSPDescribeAfterStop404(t *testing.T) {
	sink := newRTSPTestSink(t)
	require.NoError(t, sink.Configure(flvTestStreams()))
	path := "live/" + sink.ID()
	handler := sink.handler // Stop 会置 sink.handler=nil
	require.NoError(t, sink.Stop())

	resp, stream, err := handler.OnDescribe(&gortsplib.ServerHandlerOnDescribeCtx{Path: path})
	require.NoError(t, err)
	assert.Equal(t, base.StatusNotFound, resp.StatusCode, "Stop 后 DESCRIBE 必须 404（旧代码 200+nil stream）")
	assert.Nil(t, stream)
}
