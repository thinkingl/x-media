package media

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// StreamTestResult 流地址测试结果。
type StreamTestResult struct {
	URL     string `json:"url"`
	OK      bool   `json:"ok"`
	Latency int64  `json:"latency_ms"` // 耗时（毫秒）
	Detail  string `json:"detail"`
}

// TestStreamURL 测试一个流地址的可达性/连通性。
// 支持协议：
//   - whep/webrtc  http://.../whep  探测端点是否响应（完整建连由浏览器完成）
//   - http(s)://...flv  HTTP-FLV  探测 HTTP 200
func TestStreamURL(rawURL string) *StreamTestResult {
	start := time.Now()
	res := &StreamTestResult{URL: rawURL}

	u, err := url.Parse(rawURL)
	if err != nil {
		return failResult(res, start, "URL 解析失败: "+err.Error())
	}

	switch strings.ToLower(u.Scheme) {
	case "whep", "webrtc":
		// WebRTC 完整建连需浏览器；后端只探测 WHEP 端点是否可达
		res.Detail = testHTTPEndpoint(u, "POST")
	case "http", "https":
		res.Detail = testHTTPEndpoint(u, "GET")
	default:
		return failResult(res, start, "不支持的协议: "+u.Scheme)
	}

	res.Latency = time.Since(start).Milliseconds()
	res.OK = res.Detail == ""
	if res.Detail == "" {
		res.Detail = "连接成功"
	}
	return res
}

// testHTTPEndpoint 探测 HTTP 端点（WHEP/HTTP-FLV）。
func testHTTPEndpoint(u *url.URL, method string) string {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(method, u.String(), nil)
	if err != nil {
		return "HTTP 请求构造失败: " + err.Error()
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/sdp")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "HTTP 连接失败: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("HTTP 状态码异常: %d", resp.StatusCode)
	}
	return ""
}

// failResult 构造失败结果（探测前解析错误）。
func failResult(res *StreamTestResult, start time.Time, detail string) *StreamTestResult {
	res.OK = false
	res.Detail = detail
	res.Latency = time.Since(start).Milliseconds()
	return res
}
