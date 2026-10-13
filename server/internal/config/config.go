package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config 应用配置
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Log      LogConfig      `yaml:"log"`
	Media    MediaConfig    `yaml:"media"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	HTTPAddr string `yaml:"http_addr"`
	RTMPAddr string `yaml:"rtmp_addr"`
	RTSPAddr string `yaml:"rtsp_addr"`
	// APIKey 管理 API 鉴权密钥（请求头 X-API-Key）；为空则拒绝所有管理 API
	APIKey string `yaml:"api_key"`
	// CORSOrigins 跨域白名单；为空则不下发 CORS 头
	CORSOrigins []string `yaml:"cors_origins"`
	// RTSP UDP transport 端口（可选；不配置则 RTSP 仅支持 TCP）
	RTSPUDPRTPAddr    string `yaml:"rtsp_udp_rtp_addr"`
	RTSPUDPRTCPAddr   string `yaml:"rtsp_udp_rtcp_addr"`
	RTSPMulticastIP   string `yaml:"rtsp_multicast_ip"`
	RTSPMulticastRTP  int    `yaml:"rtsp_multicast_rtp_port"`
	RTSPMulticastRTCP int    `yaml:"rtsp_multicast_rtcp_port"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level      string `yaml:"level"`
	Filename   string `yaml:"filename"`
	MaxSize    int    `yaml:"max_size"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAge     int    `yaml:"max_age"`
	Compress   bool   `yaml:"compress"`
}

// MediaConfig 媒体相关配置
type MediaConfig struct {
	// Roots 是文件浏览接口允许访问的媒体根目录白名单
	Roots []string `yaml:"roots"`
}

// Load 加载配置文件
func Load(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Server: ServerConfig{
			HTTPAddr: ":8080",
			RTMPAddr: ":1935",
			RTSPAddr: ":5544",
		},
		Database: DatabaseConfig{
			Driver: "sqlite",
			DSN:    "./data/x-media.db",
		},
		Log: LogConfig{
			Level:      "info",
			Filename:   "./logs/x-media.log",
			MaxSize:    100,
			MaxBackups: 5,
			MaxAge:     30,
			Compress:   true,
		},
		Media: MediaConfig{
			Roots: []string{"./uploads", "./test_data"},
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
