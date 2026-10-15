package media

// gb28181_sink.go —— GB28181 Sink（虚拟摄像头）。
//
// M1 范围（方案 §12）：内置 gb28181 设备端代码（internal/gb28181）驱动
// 注册/心跳/注销生命周期，配置模型与校验；帧→PS→RTP 媒体路径在 M3 接入。
//
// 设计文档：docs/gb28181-sink-design.md（§4 架构 / §5.9 兼容 / §7 配置）。

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/x-media/x-media-server/internal/gb28181/device"
	"github.com/x-media/x-media-server/pkg/logger"
)

// GB28181Config GB28181 输出端配置（service 层从 outputs.config JSON 映射，§7.1）。
type GB28181Config struct {
	PlatformIP   string // 平台 SIP 地址（必填）
	PlatformPort int    // 平台 SIP 端口（必填）
	PlatformID   string // 平台 SIP 服务 ID（20 位，可选；请求 URI/To 用，§5.9）
	DeviceID     string // 设备国标编码（20 位，必填）
	ChannelID    string // 通道国标编码（20 位，缺省 = DeviceID）
	SIPDomain    string // SIP 域（必填）
	Password     string // 摘要认证密码（必填）

	Transport  string // 信令传输 udp（默认）/tcp/tcp-out
	TCPRole    string // TCP 媒体角色 auto（默认）/active/passive，M4 生效（§5.9）
	RTPMTU     int    // RTP 负载上限（默认 1400），M3 生效
	VideoCodec string // auto（默认，跟随源）/h264（遇 H265 源拒绝启动）

	LocalSIPPort int    // 本地 SIP 端口（0=内核分配）
	LocalIP      string // 本地 IP 覆盖（空=自动探测；NAT 下用学习到的外部地址，§5.9）
	// MediaTCPPort 出站 TCP 媒体的本地端口（0=自动选空闲端口）。KDM 类平台按
	// 应答 SDP 的 c=/m= 匹配媒体连接来源，出站端口必须等于公告端口（§5.9）。
	MediaTCPPort int

	RegisterInterval      int    // 注册有效期（秒，默认 3600；按平台响应协商）
	HeartbeatInterval     int    // 心跳周期（秒，默认 60）
	HeartbeatTimeoutCount int    // 连续静默/失败多少次触发重注册（默认 3）
	ProtocolVersion       string // X-GB-Ver：""（省略）/"2.0"（2016）/"3.0"（2022）

	AudioEnable bool // 是否推送音频（默认 true，service 层填默认；M3 生效）

	DeviceName   string // 目录/设备信息展示字段
	Manufacturer string
	DeviceModel  string
	Firmware     string

	GOPReplay                  bool // 新会话先注入最近 GOP（默认 true；M3 生效）
	TeleBootRestart            bool // TeleBoot 是否接受（默认 false=拒绝；重启动作 M4）
	ControlRejectUnimplemented bool // 未实现控制指令是否拒绝（默认 false=接受并记录）
}

// gb28181IDPattern 国标编码：20 位数字。
var gb28181IDPattern = regexp.MustCompile(`^\d{20}$`)

// applyDefaultsAndValidate 填默认值并校验（service 层已做 JSON 级校验，此处为
// 媒体层兜底，保证直接构造 OutputConfig 的调用方（测试/内部）也得到明确错误）。
func (c *GB28181Config) applyDefaultsAndValidate() error {
	if strings.TrimSpace(c.PlatformIP) == "" {
		return errors.New("gb28181: 平台 SIP 地址（platform_ip）不能为空")
	}
	if c.PlatformPort < 1 || c.PlatformPort > 65535 {
		return fmt.Errorf("gb28181: 平台 SIP 端口（platform_port）%d 超出范围", c.PlatformPort)
	}
	if !gb28181IDPattern.MatchString(c.DeviceID) {
		return errors.New("gb28181: 设备 ID（device_id）必须为 20 位数字")
	}
	if c.ChannelID == "" {
		c.ChannelID = c.DeviceID
	} else if !gb28181IDPattern.MatchString(c.ChannelID) {
		return errors.New("gb28181: 通道 ID（channel_id）必须为 20 位数字")
	}
	if c.PlatformID != "" && !gb28181IDPattern.MatchString(c.PlatformID) {
		return errors.New("gb28181: 平台服务 ID（platform_id）必须为 20 位数字")
	}
	if strings.TrimSpace(c.SIPDomain) == "" {
		return errors.New("gb28181: SIP 域（sip_domain）不能为空")
	}
	if c.Password == "" {
		return errors.New("gb28181: SIP 密码（password）不能为空")
	}

	if c.Transport == "" {
		c.Transport = "udp"
	}
	switch c.Transport {
	case "udp", "tcp", "tcp-out":
	default:
		return fmt.Errorf("gb28181: 信令传输（transport）%q 无效（可选 udp/tcp/tcp-out）", c.Transport)
	}
	if c.TCPRole == "" {
		c.TCPRole = "auto"
	}
	switch c.TCPRole {
	case "auto", "active", "passive":
	default:
		return fmt.Errorf("gb28181: TCP 角色（tcp_role）%q 无效（可选 auto/active/passive）", c.TCPRole)
	}
	if c.VideoCodec == "" {
		c.VideoCodec = "auto"
	}
	switch c.VideoCodec {
	case "auto", "h264":
	default:
		return fmt.Errorf("gb28181: 视频编码限制（video_codec）%q 无效（可选 auto/h264）", c.VideoCodec)
	}
	if c.RTPMTU == 0 {
		c.RTPMTU = 1400
	} else if c.RTPMTU < 400 || c.RTPMTU > 1400 {
		return fmt.Errorf("gb28181: RTP MTU（rtp_mtu）%d 超出范围 400~1400", c.RTPMTU)
	}
	if c.MediaTCPPort != 0 && (c.MediaTCPPort < 1 || c.MediaTCPPort > 65535) {
		return fmt.Errorf("gb28181: 媒体 TCP 端口（media_tcp_port）%d 超出范围 1~65535（0=自动）", c.MediaTCPPort)
	}
	if c.RegisterInterval == 0 {
		c.RegisterInterval = 3600
	} else if c.RegisterInterval < 1 || c.RegisterInterval > 86400 {
		return fmt.Errorf("gb28181: 注册周期（register_interval）%d 超出范围 1~86400", c.RegisterInterval)
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 60
	} else if c.HeartbeatInterval < 1 || c.HeartbeatInterval > 3600 {
		return fmt.Errorf("gb28181: 心跳周期（heartbeat_interval）%d 超出范围 1~3600", c.HeartbeatInterval)
	}
	if c.HeartbeatTimeoutCount == 0 {
		c.HeartbeatTimeoutCount = 3
	} else if c.HeartbeatTimeoutCount < 1 || c.HeartbeatTimeoutCount > 10 {
		return fmt.Errorf("gb28181: 心跳超时次数（heartbeat_timeout_count）%d 超出范围 1~10", c.HeartbeatTimeoutCount)
	}
	switch c.ProtocolVersion {
	case "", "2.0", "3.0":
	default:
		return fmt.Errorf("gb28181: protocol_version %q 无效（可选 \"2.0\"/\"3.0\"）", c.ProtocolVersion)
	}
	return nil
}

// toDeviceConfig 映射到内置设备端配置（internal/gb28181/device）。
func (c *GB28181Config) toDeviceConfig() device.Config {
	return device.Config{
		Enabled:               true,
		ProtocolVersion:       c.ProtocolVersion,
		PlatformSIPAddress:    c.PlatformIP,
		PlatformSIPPort:       c.PlatformPort,
		PlatformID:            c.PlatformID,
		DeviceID:              c.DeviceID,
		ChannelID:             c.ChannelID,
		SIPDomain:             c.SIPDomain,
		Password:              c.Password,
		LocalSIPPort:          c.LocalSIPPort,
		RegisterIntervalSecs:  c.RegisterInterval,
		HeartbeatIntervalSecs: c.HeartbeatInterval,
		HeartbeatTimeoutCount: c.HeartbeatTimeoutCount,
		Transport:             c.Transport,
		TCPRole:               c.TCPRole,
		RTPMTU:                c.RTPMTU,
		LocalIP:               c.LocalIP,
		MediaTCPPort:          c.MediaTCPPort,
	}
}

// GB28181Sink GB28181 输出端：把管道流作为"虚拟摄像机"注册到国标平台，
// 平台点播时以 PS over RTP 推流（媒体路径 M3 接入）。
type GB28181Sink struct {
	mu     sync.Mutex
	id     string
	cfg    GB28181Config
	status StreamStatus

	server *device.Server
	hub    *gb28181FrameHub
	pipe   *gb28181FramePipe

	cancel  context.CancelFunc
	done    chan struct{}
	started bool

	streams []StreamInfo

	// 观测计数（§9）
	registered atomic.Bool
	regFails   atomic.Int64
	sessions   atomic.Int64
	psBytes    atomic.Int64
}

// Compile-time check：GB28181Sink 实现 Sink 接口。
var _ Sink = (*GB28181Sink)(nil)

// NewGB28181Sink 创建 GB28181 sink（配置来自 service 层映射或直接构造）。
func NewGB28181Sink(config *OutputConfig) (*GB28181Sink, error) {
	if config == nil || config.GB28181 == nil {
		return nil, errors.New("gb28181: 缺少 GB28181 配置段")
	}
	cfg := *config.GB28181
	if err := cfg.applyDefaultsAndValidate(); err != nil {
		return nil, err
	}
	return &GB28181Sink{
		id:     config.ID,
		cfg:    cfg,
		status: StreamStatusStopped,
		hub:    newGB28181FrameHub(),
		pipe:   newGB28181FramePipe(),
	}, nil
}

// ID 返回输出端唯一标识。
func (s *GB28181Sink) ID() string { return s.id }

// Status 返回当前状态。
func (s *GB28181Sink) Status() StreamStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Start 启动设备端：注册、心跳、SIP 监听（阻塞循环放后台 goroutine）。
// 注册被拒/平台不可达不阻断启动（库内退避重试 + 静默重注册）。
func (s *GB28181Sink) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	server := device.New(s.cfg.toDeviceConfig(), s.deviceInfo(), s.hub)
	server.SetMetricsHooks(&gb28181Hooks{sink: s})
	server.SetControlHandlers(s.controlCallbacks())
	server.SetConfigHandlers(s.configCallbacks())
	server.SetMediaMTU(s.cfg.RTPMTU)

	ctx, cancel := context.WithCancel(ctx)
	s.server = server
	s.cancel = cancel
	s.done = make(chan struct{})
	s.started = true
	s.status = StreamStatusRunning
	done := s.done
	s.mu.Unlock()

	logger.Infof("gb28181 sink started: %s, platform=%s:%d, device=%s, transport=%s",
		s.id, s.cfg.PlatformIP, s.cfg.PlatformPort, s.cfg.DeviceID, s.cfg.Transport)

	go func() {
		defer close(done)
		if err := server.Start(ctx); err != nil {
			logger.Errorf("gb28181 sink server exited: %s: %v", s.id, err)
			s.mu.Lock()
			if s.status == StreamStatusRunning {
				s.status = StreamStatusError
			}
			s.mu.Unlock()
			return
		}
		logger.Infof("gb28181 sink server stopped: %s", s.id)
	}()
	return nil
}

// Stop 停止：尽力注销（REGISTER Expires:0，2s 超时）→ 关停 SIP/媒体 → 清理。
func (s *GB28181Sink) Stop() error {
	s.mu.Lock()
	if !s.started {
		s.status = StreamStatusStopped
		s.mu.Unlock()
		return nil
	}
	server, cancel, done := s.server, s.cancel, s.done
	s.started = false
	s.server = nil
	s.cancel = nil
	s.status = StreamStatusStopped
	s.mu.Unlock()

	if server != nil {
		dctx, dcancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := server.Deregister(dctx); err != nil {
			logger.Warnf("gb28181 sink deregister failed (best effort): %s: %v", s.id, err)
		} else {
			logger.Infof("gb28181 sink deregistered: %s", s.id)
		}
		dcancel()
		server.Stop()
	}
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			logger.Warnf("gb28181 sink stop timeout: %s", s.id)
		}
	}
	return nil
}

// Configure 管道协商：校验媒体能力（仅 H.264/H.265 视频；video_codec=h264 拒绝 H265 源），
// 填充帧转换参数并把协商到的编码同步给内置设备端（媒体会话按 INVITE 时点取用）。
func (s *GB28181Sink) Configure(streams []StreamInfo) error {
	if err := s.pipe.configure(streams, s.cfg.VideoCodec, s.cfg.AudioEnable); err != nil {
		return err
	}
	s.mu.Lock()
	s.streams = append([]StreamInfo(nil), streams...)
	server := s.server
	s.mu.Unlock()

	if server != nil {
		video, audio := s.pipe.videoCodecName()
		server.SetMediaCodecs(video, audio)
		server.SetMediaMTU(s.cfg.RTPMTU)
	}
	return nil
}

// WriteFrame 数据面：视频帧 → AccessUnit（NALU+音频窗口）→ hub 扇出；
// 音频帧先入队，随下一个视频帧并入同一 PS 突发（§5.7）。
func (s *GB28181Sink) WriteFrame(f *Frame) error {
	switch f.Header.FrameType {
	case FrameTypeVideo:
		if au := s.pipe.videoAU(f); au != nil {
			s.hub.broadcast(*au)
		}
	case FrameTypeAudio:
		s.pipe.audioFrame(f)
	}
	return nil
}

// Notify 处理 source 异步事件（当前无额外处理）。
func (s *GB28181Sink) Notify(sig *Signal) error { return nil }

// Compile-time check：实现 ClientInfoProvider（输出端详情"当前客户端"）。
var _ ClientInfoProvider = (*GB28181Sink)(nil)

// Clients 返回当前平台点播会话（0 或 1 个；内置设备端为单会话模型，§9）。
func (s *GB28181Sink) Clients() []ClientInfo {
	s.mu.Lock()
	server := s.server
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	info, ok := server.LiveSession()
	if !ok {
		return nil
	}
	transport := info.Transport
	if transport == "" {
		transport = s.cfg.Transport
	}
	ua := "GB28181 平台"
	if info.SSRC != 0 {
		ua = fmt.Sprintf("GB28181 平台（ssrc=%d）", info.SSRC)
	}
	return []ClientInfo{{
		Address:     info.RemoteAddr,
		UserAgent:   ua,
		Transport:   transport,
		ConnectedAt: info.Since,
	}}
}

func (s *GB28181Sink) deviceInfo() device.DeviceInfo {
	return device.DeviceInfo{
		Name:         s.cfg.DeviceName,
		Manufacturer: s.cfg.Manufacturer,
		Model:        s.cfg.DeviceModel,
		Firmware:     s.cfg.Firmware,
	}
}

// controlCallbacks 控制指令回调（§5.4 应答矩阵）：
// 有回调 = 应答 OK（记录日志）；无回调 = 内置拒绝（ERROR）。
func (s *GB28181Sink) controlCallbacks() device.ControlCallbacks {
	cb := device.ControlCallbacks{
		OnPTZCmd: func(cmd device.PtzCommand) {
			logger.Infof("gb28181[%s]: PTZ cmd accepted (no physical PTZ): %+v", s.id, cmd)
		},
		OnForceIFrame: func() {
			// M3：向 Source 发送"请求关键帧"信令（源不支持则忽略）。
			logger.Infof("gb28181[%s]: IFrame requested by platform", s.id)
		},
		OnRecordCmd: func(start bool) {
			logger.Infof("gb28181[%s]: RecordCmd accepted (no local recording), start=%v", s.id, start)
		},
		OnGuardCmd: func(arm bool) {
			logger.Infof("gb28181[%s]: GuardCmd accepted, arm=%v", s.id, arm)
		},
		OnResetAlarm: func() {
			logger.Infof("gb28181[%s]: AlarmCmd(ResetAlarm) accepted", s.id)
		},
	}
	if s.cfg.TeleBootRestart {
		cb.OnTeleBoot = func() {
			// 默认拒绝（Q7 决议）；开启后接受并记录，进程重启动作在 M4 接入。
			logger.Warnf("gb28181[%s]: TeleBoot accepted (process restart not implemented yet)", s.id)
		}
	}
	return cb
}

// configCallbacks DeviceConfig 下发（BasicParam 记录；热应用 M4 评估）。
func (s *GB28181Sink) configCallbacks() device.ConfigCallbacks {
	return device.ConfigCallbacks{
		OnBasicParam: func(p device.BasicParamCfg) {
			logger.Infof("gb28181[%s]: BasicParam received: %s (applied on next start)", s.id, p.String())
		},
	}
}

// gb28181Hooks 观测钩子（§9）：注册/心跳/会话/媒体字节数。
type gb28181Hooks struct{ sink *GB28181Sink }

func (h *gb28181Hooks) RegisterAttempt() {
	logger.Infof("gb28181[%s]: REGISTER attempt", h.sink.id)
}

func (h *gb28181Hooks) RegisterOK() {
	h.sink.registered.Store(true)
	h.sink.regFails.Store(0)
	logger.Infof("gb28181[%s]: registered", h.sink.id)
}

func (h *gb28181Hooks) RegisterFail() {
	h.sink.regFails.Add(1)
	logger.Warnf("gb28181[%s]: register failed", h.sink.id)
}

func (h *gb28181Hooks) KeepaliveFail() {
	logger.Warnf("gb28181[%s]: keepalive failed / platform silent", h.sink.id)
}

func (h *gb28181Hooks) InviteSessionStarted() {
	h.sink.sessions.Add(1)
	logger.Infof("gb28181[%s]: platform invite session started", h.sink.id)
}

func (h *gb28181Hooks) InviteSessionStopped() {
	if n := h.sink.sessions.Add(-1); n < 0 {
		h.sink.sessions.Store(0)
	}
	logger.Infof("gb28181[%s]: platform invite session stopped", h.sink.id)
}

func (h *gb28181Hooks) InviteFail() {
	logger.Warnf("gb28181[%s]: invite failed", h.sink.id)
}

func (h *gb28181Hooks) PSBytesOut(n int64) {
	h.sink.psBytes.Add(n)
}
