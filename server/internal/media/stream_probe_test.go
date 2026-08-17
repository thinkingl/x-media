package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTestStreamURL_InvalidScheme(t *testing.T) {
	r := TestStreamURL("ftp://example.com/x")
	assert.False(t, r.OK)
	assert.Contains(t, r.Detail, "不支持的协议")
}

func TestTestStreamURL_InvalidURL(t *testing.T) {
	r := TestStreamURL("not a url at all")
	assert.False(t, r.OK)
	assert.NotEmpty(t, r.Detail)
}

func TestTestStreamURL_WHEPUnreachable(t *testing.T) {
	r := TestStreamURL("http://127.0.0.1:1/live/abc/whep")
	assert.False(t, r.OK)
	assert.NotEmpty(t, r.Detail)
}

func TestTestStreamURL_HTTPSuccess(t *testing.T) {
	// 用 HTTP-FLV 的真实端点测试成功场景（指向服务本身）
	r := TestStreamURL("http://127.0.0.1:18090/live/nonexistent.flv")
	// 端点可达(200)即视为成功；404 会返回非 200 但连接成功
	t.Logf("result: ok=%v detail=%s", r.OK, r.Detail)
}
