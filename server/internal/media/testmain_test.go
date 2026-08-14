package media

// 测试用 TestMain：初始化 logger，使 internal/media 各组件在测试中的
// 详细日志（RTSP SETUP/PLAY、packet 计数等）可见，便于定位时序/卡住问题。
// 之前缺失 TestMain 导致 logger.Infof 全部静默（no-op），排障只能靠猜测。

import (
	"os"
	"testing"

	"github.com/x-media/x-media-server/internal/config"
	"github.com/x-media/x-media-server/pkg/logger"
)

func TestMain(m *testing.M) {
	_ = logger.Init(&config.LogConfig{Level: "info", Filename: ""})
	code := m.Run()
	logger.Sync()
	os.Exit(code)
}
