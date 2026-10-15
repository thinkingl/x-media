// gbplat —— GB28181 平台端联调工具（双实现互操作验证）。
//
// 使用 gb28181-go 的平台端（MiBeeNvr 生产实现）作为"真实平台"：
//  1. 等待设备注册（digest qop=auth）与目录上报；
//  2. INVITE 点播设备通道（UDP 媒体）；
//  3. 平台端解 RTP/PS，统计视频 AU（IDR）、音频帧；
//  4. 输出 SUMMARY 行并以退出码表达验收结果。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mickeyzzc/gb28181-go/platform"
	gbsip "github.com/mickeyzzc/gb28181-go/platform/sip"
)

type memStore struct {
	mu          sync.Mutex
	devices     map[string]gbsip.GB28181Device
	channels    map[string]gbsip.GB28181Channel
	registered  chan string
	catalogSeen atomic.Int64
}

func newMemStore() *memStore {
	return &memStore{
		devices:    map[string]gbsip.GB28181Device{},
		channels:   map[string]gbsip.GB28181Channel{},
		registered: make(chan string, 4),
	}
}

func (m *memStore) UpsertGB28181Device(_ context.Context, d gbsip.GB28181Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, existed := m.devices[d.ID]
	m.devices[d.ID] = d
	if !existed {
		select {
		case m.registered <- d.ID:
		default:
		}
	}
	return nil
}

func (m *memStore) UpsertGB28181Channel(_ context.Context, c gbsip.GB28181Channel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[c.ID] = c
	m.catalogSeen.Add(1)
	return nil
}

func (m *memStore) ListGB28181Devices(context.Context) ([]gbsip.GB28181Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []gbsip.GB28181Device
	for _, d := range m.devices {
		out = append(out, d)
	}
	return out, nil
}

func (m *memStore) ListGB28181Channels(_ context.Context, deviceID string) ([]gbsip.GB28181Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []gbsip.GB28181Channel
	for _, c := range m.channels {
		if c.DeviceID == deviceID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memStore) MarkDeviceOffline(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.devices[id]; ok {
		d.Status = "offline"
		m.devices[id] = d
	}
	return nil
}

func (m *memStore) BindChannelCamera(_ context.Context, channelID, cameraID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.channels[channelID]; ok {
		c.CameraID = cameraID
		m.channels[channelID] = c
	}
	return nil
}

func (m *memStore) DeleteGB28181Device(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.devices, id)
	return nil
}

func (m *memStore) GetGB28181Device(_ context.Context, id string) (*gbsip.GB28181Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.devices[id]; ok {
		return &d, nil
	}
	return nil, fmt.Errorf("device %s not found", id)
}

func (m *memStore) DeleteGB28181Channel(_ context.Context, channelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.channels, channelID)
	return nil
}

// interopEnroller 实现平台端 CameraEnroller seam：把解复用的视频 AU / 音频帧
// 计数（等价于 MiBeeNvr 录制管线的接入点）。
type interopEnroller struct {
	videoAUs, idrs, audioFrames atomic.Int64
	audioCodec                  atomic.Value // string
	audioConfig                 atomic.Value // string hex
}

func (e *interopEnroller) EnsureGB28181Camera(deviceID, channelID, name, sourceIP string) error {
	return nil
}
func (e *interopEnroller) GB28181CameraIDByChannel(deviceID, channelID string) (string, bool) {
	return "interop-cam-" + channelID, true
}
func (e *interopEnroller) GB28181NALUWriter(cameraID string) func(au [][]byte, ptsTicks int64, isIDR bool) {
	return func(au [][]byte, ptsTicks int64, isIDR bool) {
		e.videoAUs.Add(1)
		if isIDR {
			e.idrs.Add(1)
		}
	}
}
func (e *interopEnroller) GB28181AudioWriter(cameraID string) func(codec string, data, config []byte, ptsTicks int64, samples int) {
	return func(codec string, data, config []byte, ptsTicks int64, samples int) {
		e.audioFrames.Add(1)
		e.audioCodec.Store(codec)
		if len(config) > 0 {
			e.audioConfig.Store(fmt.Sprintf("%x", config))
		}
	}
}
func (e *interopEnroller) OnGB28181Invite(cameraID string) {}
func (e *interopEnroller) OnGB28181Bye(cameraID string)    {}
func (e *interopEnroller) NewGB28181PlaybackSink(cameraID string) (platform.AUWriter, error) {
	return nil, fmt.Errorf("interop: playback not supported")
}
func (e *interopEnroller) GB28181PlaybackAudioWriter(cameraID string) func(string, []byte, []byte, int64, int) {
	return nil
}
func (e *interopEnroller) UpdateGB28181DeviceMeta(deviceID, manufacturer, model string) error {
	return nil
}
func (e *interopEnroller) ArchiveGB28181Camera(deviceID, channelID string) error { return nil }
func (e *interopEnroller) GB28181RecordingWanted(deviceID, channelID string) bool {
	return false
}
func (e *interopEnroller) GB28181SubChannelID(deviceID, channelID string) string { return "" }
func (e *interopEnroller) SetGB28181SubChannel(deviceID, channelID, subChannelID string) error {
	return nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	deviceID := env("DEVICE_ID", "34020000001320000001")
	channelID := env("CHANNEL_ID", "34020000001310000001")
	runSecs, _ := strconv.Atoi(env("RUN_SECONDS", "12"))
	_ = deviceID

	cfg := gbsip.Config{
		Enabled:        true,
		SIPListen:      env("SIP_LISTEN", ":25060"),
		ServerID:       env("SERVER_ID", "34020000002000000001"),
		Realm:          env("REALM", "3402000000"),
		Password:       env("PASSWORD", "interop1234"),
		PortRange:      env("PORT_RANGE", "30500-30550"),
		MediaTransport: "udp",
	}

	store := newMemStore()
	dm := platform.NewDeviceManager(60 * time.Second)
	ports := platform.NewPortManager(30500, 30550)
	sessions := platform.NewSessionManager(ports, cfg.ServerID)
	srv := gbsip.NewServer(cfg, dm, sessions, store)
	enr := &interopEnroller{}
	srv.SetCameraEnroller(enr)

	if err := srv.Start(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "platform start: %v\n", err)
		os.Exit(2)
	}
	defer func() { _ = srv.Stop() }()
	fmt.Printf("PLATFORM listening %s serverID=%s\n", cfg.SIPListen, cfg.ServerID)

	// 1) 等待设备注册
	var device string
	select {
	case device = <-store.registered:
		fmt.Printf("DEVICE registered: %s\n", device)
	case <-time.After(30 * time.Second):
		summary(map[string]any{"registered": false})
		os.Exit(1)
	}

	// 目录上报（平台注册后自动查询；给一拍时间）
	deadline := time.Now().Add(5 * time.Second)
	for store.catalogSeen.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	catalog := store.catalogSeen.Load()

	// 2) INVITE 点播
	time.Sleep(500 * time.Millisecond)
	if err := srv.InviteChannel(device, channelID); err != nil {
		fmt.Fprintf(os.Stderr, "invite: %v\n", err)
		summary(map[string]any{"registered": true, "catalog": catalog, "invite": false})
		os.Exit(1)
	}
	fmt.Println("INVITE sent")

	// 3) 收流统计（Audio/video callbacks ride the CameraEnroller seam —
	// the same path MiBeeNvr's recorder pipeline uses.)
	time.Sleep(time.Duration(runSecs) * time.Second)

	// 4) 停流并清理
	_ = srv.ByeChannel(device, channelID)
	time.Sleep(500 * time.Millisecond)

	videoAUs, idrs, audioFrames := enr.videoAUs.Load(), enr.idrs.Load(), enr.audioFrames.Load()
	audioCodec, _ := enr.audioCodec.Load().(string)
	audioConfig, _ := enr.audioConfig.Load().(string)
	ok := videoAUs >= 30 && idrs >= 1 && audioFrames >= 10
	summary(map[string]any{
		"registered":  true,
		"catalog":     catalog,
		"invite":      true,
		"videoAUs":    videoAUs,
		"idrs":        idrs,
		"audioFrames": audioFrames,
		"audioCodec":  audioCodec,
		"audioConfig": audioConfig,
		"ok":          ok,
	})
	if !ok {
		os.Exit(1)
	}
}

func summary(m map[string]any) {
	b, _ := json.Marshal(m)
	fmt.Printf("SUMMARY %s\n", b)
}
