// Package device implements the GB/T 28181-2016/2022 device role (UAC):
// SIP registration with a platform, MANSCDP XML messaging, RTP/PS media
// push for live streaming, playback, and download.
//
// Extracted verbatim from the production implementation in
// mibee-eye-raspi-go (Mi-Bee Studio), hardened against real GB28181
// platforms (digest qop=auth, unique Via branches, local-IP detection,
// MANSCDP attribute form, SIP-over-TCP).
//
// # Host integration seams
//
// The package is capture- and storage-agnostic. The host injects:
//
//   - [FrameSource] — live video access units (bridge your capture hub)
//   - [RecordingSource] — recorded-segment index for RecordInfo/playback
//   - [Config] / [DeviceInfo] — connection settings and device identity
//
// Segment files use the reference format read by [OpenSegment]: bare
// Annex-B H.264 with a per-frame `<segment>.ts.jsonl` sidecar of
// millisecond timestamps.
package device

import (
	"context"
	"time"
)

// GB35114NotePolicy selects the device-side behavior when a
// platform→device request fails incoming Note verification (issue #52).
type GB35114NotePolicy int

const (
	// GB35114NoteReject answers 403 Forbidden. The zero value: inside an
	// authenticated A-level session a Note that does not verify is
	// tampering and must fail closed. Note-less requests still pass
	// (mixed-mode Digest platforms).
	GB35114NoteReject GB35114NotePolicy = iota
	// GB35114NoteWarn only logs the failure and serves the request — for
	// rollout observation on deployments with clock skew or signature
	// quirks.
	GB35114NoteWarn
	// GB35114NoteOff disables incoming Note verification entirely.
	GB35114NoteOff
)

// Config holds GB28181 device (UAC) connection settings. YAML shapes are
// identical to the source project's `gb28181:` section.
type Config struct {
	Enabled bool `yaml:"enabled"` // Enable GB28181 registration (default: false)
	// ProtocolVersion, when non-empty, is sent as the X-GB-Ver header on
	// REGISTER (GB/T 28181-2022 Annex I: "3.0"=2022, "2.0"=2016). The
	// platform's version, when it answers with one, is exposed via
	// Server.PlatformProtocolVersion(). Empty (default) omits the header.
	ProtocolVersion    string `yaml:"protocol_version"`
	PlatformSIPAddress string `yaml:"platform_sip_address"` // SIP server (platform) address
	PlatformSIPPort    int    `yaml:"platform_sip_port"`    // SIP server (platform) port
	// PlatformID, when non-empty, becomes the user part of outbound
	// request-URIs and the keepalive MESSAGE To header
	// (e.g. "sip:<PlatformID>@<domain>"). Some platforms (Hikvision
	// iVMS / Dahua-class) validate the platform service ID there;
	// empty keeps the historical "<domain>@<domain>" form.
	// Local patch: x-media GB28181 sink design §5.9.
	PlatformID            string `yaml:"platform_id"`
	DeviceID              string `yaml:"device_id"`               // GB28181 device ID (20 digits)
	ChannelID             string `yaml:"channel_id"`              // GB28181 channel ID (20 digits)
	SIPDomain             string `yaml:"sip_domain"`              // GB28181 SIP domain
	Password              string `yaml:"password"`                // SIP authentication password
	LocalSIPPort          int    `yaml:"local_sip_port"`          // Local SIP listening port
	RegisterIntervalSecs  int    `yaml:"register_interval_secs"`  // SIP REGISTER interval (seconds)
	HeartbeatIntervalSecs int    `yaml:"heartbeat_interval_secs"` // SIP keepalive heartbeat interval (seconds)
	HeartbeatTimeoutCount int    `yaml:"heartbeat_timeout_count"` // Missed heartbeats before declaring timeout
	// MulticastTTL sets IP_MULTICAST_TTL on the media socket when the
	// INVITE's media address is a multicast group (0 = OS default; the
	// LAN default of 1 stays within one subnet).
	MulticastTTL int `yaml:"multicast_ttl,omitempty"`

	// ExtraChannels lists additional catalog channels (A.2.1): the
	// device's own entry rides first, these follow — large lists page
	// across MESSAGEs automatically.
	ExtraChannels []ChannelItem `yaml:"extra_channels,omitempty"`

	// VideoCodec selects the live PS video codec: "h264" (default) or
	// "h265". Local patch (x-media GB28181 sink design §5.7): the live
	// muxer is psmux (H.264/H.265), replacing the H264-only path.
	VideoCodec string `yaml:"video_codec,omitempty"`
	// AudioCodec declares the live audio track: "" (none), "aac"
	// (ADTS-framed frames), "g711a" or "g711u". Local patch (§5.7).
	AudioCodec string `yaml:"audio_codec,omitempty"`
	// RTPMTU caps the RTP payload size in bytes (0 = 1400). Local patch
	// (§5.9: old platforms / tunnels need smaller fragments).
	RTPMTU int `yaml:"rtp_mtu,omitempty"`
	// TCPRole forces this device's TCP media role regardless of the
	// offer's RFC 4145 setup value: "" / "auto" (mirror the offer),
	// "active" (device dials the platform), "passive" (device listens).
	// Local patch (§5.9: vendor implementations that invert setup).
	TCPRole string `yaml:"tcp_role,omitempty"`
	// LocalIP overrides the advertised contact address (Contact headers).
	// Empty = auto: route-probed local IP, upgraded to the NAT-external
	// address learned from response Via received/rport when available.
	// Local patch (§5.9: NAT deployments with a fixed mapping).
	LocalIP string `yaml:"local_ip,omitempty"`
	// MediaTCPPort pins the local TCP port for outbound TCP media (the
	// advertised m= port and the connection's source port must match on
	// KDM-class platforms). 0 = pick a free port automatically.
	// Local patch (§5.9).
	MediaTCPPort int `yaml:"media_tcp_port,omitempty"`

	Transport string `yaml:"transport"` // SIP transport: udp (default), tcp (inbound listener), tcp-out (device dials the platform, full lifecycle), tls (SIPS outbound, GB/T 28181-2022 A-level), or tls-listen (inbound SIPS: the platform dials the device; requires the TLS pair)

	// IncomingNotePolicy sets the failure behavior of device-side Note
	// verification for platform→device requests (issue #52). Only active
	// when RegisterAuthenticator implements IncomingNoteVerifier (the
	// GB35114 A-level reference implementation does). Default: reject.
	IncomingNotePolicy GB35114NotePolicy `yaml:"incoming_note_policy"`

	// MaxSIPMessageSize bounds one Content-Length framed message (headers +
	// body) on the TCP/TLS read path — a forged Content-Length header must
	// drop the connection, not allocate (issue #37). 0 = default 1 MiB;
	// >0 = custom limit; <0 = unlimited (tests only).
	MaxSIPMessageSize int `yaml:"max_sip_message_size,omitempty"`

	// TLS fields (Transport "tls" only — SIPS signaling per GB/T 28181-2022
	// A-level security). The GB convention is a self-signed CA whose serial
	// is the device/platform ID; the host provisions the files.
	TLSCAFile             string `yaml:"tls_ca_file,omitempty"`              // CA (or the platform's self-signed cert) used to verify the platform
	TLSCertFile           string `yaml:"tls_cert_file,omitempty"`            // optional client certificate (mutual auth)
	TLSKeyFile            string `yaml:"tls_key_file,omitempty"`             // client certificate key
	TLSInsecureSkipVerify bool   `yaml:"tls_insecure_skip_verify,omitempty"` // accept any platform cert (lab only)

	// ConfigDownload VideoParamOpt block (A.2.1.20, issue #109): the
	// device's download speeds and supported resolutions as "/"-joined
	// strings (e.g. "1/2/4", "1920x1080/640x480", values per Annex G).
	// Empty (default) omits the block from ConfigDownload answers.
	VideoParamOptDownloadSpeed string `yaml:"video_param_opt_download_speed,omitempty"`
	VideoParamOptResolution    string `yaml:"video_param_opt_resolution,omitempty"`

	// RegisterAuthenticator opts into an alternative REGISTER
	// authentication strategy (e.g. GB 35114 A-level via the tagged
	// security35114 package). Nil keeps the built-in SIP Digest flow.
	// Not YAML-serializable: the host wires the implementation in code.
	RegisterAuthenticator RegisterAuthenticator `yaml:"-"`
}

// DeviceInfo identifies the device in Catalog/DeviceInfo responses.
// YAML shapes are identical to the source project's `device:` section.
type DeviceInfo struct {
	Name         string `yaml:"name"`          // Camera friendly name
	Manufacturer string `yaml:"manufacturer"`  // Device manufacturer
	Model        string `yaml:"model"`         // Device model
	Firmware     string `yaml:"firmware"`      // Firmware version
	HardwareID   string `yaml:"hardware_id"`   // Device hardware identifier
	SerialNumber string `yaml:"serial_number"` // Device serial number
}

// UserAgent is the SIP User-Agent stamped on every message the device
// role emits. The neutral default carries no host brand — hosts rebrand
// by assigning their own value before calling New/Start (concurrent
// mutation afterwards races the message builders).
var UserAgent = "GB28181-Go/1.0"

// NALU is one H.264 NAL unit (payload without Annex-B start code).
type NALU struct {
	Type  byte   // NALU type (first byte & 0x1F)
	Data  []byte // Raw NALU data (without start code)
	IsIDR bool   // True if type == 5
	IsSPS bool   // True if type == 7
	IsPPS bool   // True if type == 8
}

// AccessUnit is a complete H.264/H.265 access unit (one encoder frame).
// Local patch (x-media GB28181 sink design §5.7): Audio carries the audio
// frames that fall into this AU's window; the live muxer appends them to
// the same PS burst so the burst-final RTP marker still delimits the AU.
type AccessUnit struct {
	NALUs     []NALU
	Timestamp time.Time
	KeyFrame  bool // True if contains IDR
	Audio     []AudioFrame
}

// AudioFrame is one audio frame riding with a video AU. Data is the codec
// frame as-is: AAC must already carry its 7-byte ADTS header, G.711 raw
// bytes. Local patch (§5.7).
type AudioFrame struct {
	Data      []byte
	Timestamp time.Time
}

// FrameSubscription is a live-frame subscription handed out by
// [FrameSource.Subscribe]. Cancel the context passed to Subscribe to tear
// the subscription down; [FrameSource.Unsubscribe] also works by ID.
type FrameSubscription struct {
	ID      string
	Channel <-chan AccessUnit
}

// FrameSource supplies live H.264 access units for INVITE-driven media
// sessions. Implement over your capture pipeline's fan-out hub; bounded
// channels with drop-on-full are expected (never block the producer).
type FrameSource interface {
	Subscribe(ctx context.Context) *FrameSubscription
	Unsubscribe(id string)
}

// SegmentMeta describes one recorded segment for RecordInfo queries and
// playback/download INVITEs.
type SegmentMeta struct {
	File      string `json:"file"`      // path relative to the recording root
	StartMS   int64  `json:"start_ms"`  // unix milliseconds of first frame
	EndMS     int64  `json:"end_ms"`    // unix milliseconds of last frame
	Size      int64  `json:"size"`      // segment file size in bytes
	Frames    int    `json:"frames"`    // number of access units
	Keyframes int    `json:"keyframes"` // number of keyframe access units
}
