package media

import (
	"fmt"

	"github.com/x-media/x-media-server/pkg/logger"
)

const tsPacketSize = 188

type TSDemuxer struct {
	videoPID      uint16
	audioPID      uint16
	videoBuf      []byte
	audioBuf      []byte
	videoReady    bool
	audioReady    bool
	audioConfig   []byte
	onVideo       func(data []byte, isKey bool)
	onAudio       func(data []byte, config []byte)
	totalPkts     int
	videoPkts     int
	audioPkts     int
	otherPkts     int
	videoFrameCount int
	audioFrameCount int
	flushCount      int
	totalFlushed    int64
}

func NewTSDemuxer(videoPID, audioPID uint16) *TSDemuxer {
	return &TSDemuxer{
		videoPID: videoPID,
		audioPID: audioPID,
	}
}

func (d *TSDemuxer) OnVideo(fn func(data []byte, isKey bool))  { d.onVideo = fn }
func (d *TSDemuxer) OnAudio(fn func(data []byte, config []byte)) { d.onAudio = fn }
func (d *TSDemuxer) AudioConfig() []byte { return d.audioConfig }
func (d *TSDemuxer) SetAudioConfig(cfg []byte) { d.audioConfig = cfg }

func (d *TSDemuxer) Feed(data []byte) {
	for i := 0; i+tsPacketSize <= len(data); i += tsPacketSize {
		pkt := data[i : i+tsPacketSize]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		d.totalPkts++

		af := (pkt[3] >> 5) & 0x03
		payloadStart := 4
		if af == 0x00 || af == 0x02 {
			continue
		}
		if af == 0x03 {
			afLen := int(pkt[4])
			payloadStart = 5 + afLen
		}
		if payloadStart >= tsPacketSize {
			continue
		}

		payload := pkt[payloadStart:]
		switch pid {
		case d.videoPID:
			d.videoPkts++
			isPESStart := pkt[1]&0x40 != 0
			if isPESStart && len(payload) > 0 {
				pointerField := int(payload[0])
				if pointerField > 0 && pointerField < len(payload) && len(d.videoBuf) > 0 {
					d.videoBuf = append(d.videoBuf, payload[1:1+pointerField]...)
				}
				payload = payload[1+pointerField:]
				payload = d.stripPESHeaderInPlace(payload)
			}
			d.videoBuf = append(d.videoBuf, payload...)

			d.flushVideo()
		case d.audioPID:
			d.audioPkts++
			isPESStart := pkt[1]&0x40 != 0
			if isPESStart && len(payload) > 0 {
				pointerField := int(payload[0])
				if pointerField > 0 && pointerField < len(payload) && len(d.audioBuf) > 0 {
					d.audioBuf = append(d.audioBuf, payload[1:1+pointerField]...)
				}
				payload = payload[1+pointerField:]
				payload = d.stripPESHeaderInPlace(payload)
			}
			d.audioBuf = append(d.audioBuf, payload...)

			if len(d.audioBuf) > 1024*1024 {
				d.flushAudio()
			}
		default:
			d.otherPkts++
		}
	}

	if len(d.videoBuf) > 10*1024*1024 {
		d.flushVideo()
	}

	if d.totalPkts%10000 == 0 && d.totalPkts > 0 {
		logger.Infof("TS demuxer stats: total=%d video=%d audio=%d other=%d flushed=%d totalBytes=%d",
			d.totalPkts, d.videoPkts, d.audioPkts, d.otherPkts, d.flushCount, d.totalFlushed)
	}
}

func (d *TSDemuxer) stripPESHeaderInPlace(data []byte) []byte {
	if len(data) < 9 {
		return data
	}
	if data[0] != 0 || data[1] != 0 || data[2] != 1 {
		return data
	}
	streamID := data[3]
	if streamID < 0xC0 || streamID > 0xEF {
		return data
	}
	pesHeaderLen := int(data[8])
	totalHeaderLen := 9 + pesHeaderLen
	if totalHeaderLen >= len(data) {
		return nil
	}
	return data[totalHeaderLen:]
}


// findFrameSplit finds the byte offset where a new access unit begins.
// It looks for AUD (type 9) after existing slice data, which marks a frame boundary.
func (d *TSDemuxer) findFrameSplit() int {
	hasSlice := false
	for i := 0; i < len(d.videoBuf)-4; i++ {
		if d.videoBuf[i] == 0 && d.videoBuf[i+1] == 0 {
			var nalStart int
			if i+3 < len(d.videoBuf) && d.videoBuf[i+2] == 0 && d.videoBuf[i+3] == 1 {
				nalStart = i + 4
			} else if d.videoBuf[i+2] == 1 {
				nalStart = i + 3
			} else {
				continue
			}
			if nalStart >= len(d.videoBuf) {
				continue
			}
			nalType := d.videoBuf[nalStart] & 0x1F
			switch nalType {
			case 5, 1: // IDR / non-IDR slice
				hasSlice = true
			case 9: // AUD - access unit delimiter
				if hasSlice {
					return i
				}
			}
		}
	}
	return -1
}

func (d *TSDemuxer) flushVideo() {
	splitAt := d.findFrameSplit()
	if splitAt < 0 {
		return
	}

	frameData := make([]byte, splitAt)
	copy(frameData, d.videoBuf[:splitAt])
	d.videoBuf = d.videoBuf[splitAt:]

	if len(frameData) == 0 {
		return
	}

	d.flushCount++
	d.totalFlushed += int64(len(frameData))
	d.videoFrameCount++

	isKey := false
	for i := 0; i < len(frameData)-4; i++ {
		if frameData[i] == 0 && frameData[i+1] == 0 {
			var nalStart int
			if frameData[i+2] == 0 && frameData[i+3] == 1 {
				nalStart = i + 4
			} else if frameData[i+2] == 1 {
				nalStart = i + 3
			} else {
				continue
			}
			if nalStart < len(frameData) {
				nalType := frameData[nalStart] & 0x1F
				if nalType == 5 || nalType == 7 || nalType == 8 {
					isKey = true
					break
				}
			}
		}
	}

	if d.videoFrameCount <= 25 {
		h := ""
		for i := 0; i < len(frameData) && i < 16; i++ {
			h += fmt.Sprintf("%02x ", frameData[i])
		}
		logger.Infof("[TRACE-1] TS→Input #%d: size=%d key=%v head=[%s]",
			d.videoFrameCount, len(frameData), isKey, h)
	}

	if d.onVideo != nil {
		d.onVideo(frameData, isKey)
	}
}

func (d *TSDemuxer) flushAudio() {
	data := d.audioBuf
	d.audioBuf = nil
	if len(data) == 0 {
		return
	}

	payload := data

	if len(payload) > 7 && payload[0] == 0xFF && (payload[1]&0xF0) == 0xF0 {
		profile := int((payload[2] >> 6) & 0x03)
		sampleRateIndex := int((payload[2] >> 2) & 0x0F)
		channelConfig := int((payload[2]&0x01)<<2 | (payload[3] >> 6))

		audioObjectType := profile + 1
		if len(d.audioConfig) == 0 {
			d.audioConfig = []byte{
				byte(audioObjectType<<3 | sampleRateIndex>>1),
				byte((sampleRateIndex&0x01)<<7 | channelConfig<<3),
			}
			logger.Infof("AAC AudioSpecificConfig: objectType=%d sampleRateIndex=%d channels=%d config=%02x%02x",
				audioObjectType, sampleRateIndex, channelConfig, d.audioConfig[0], d.audioConfig[1])
		}

		adtsHeaderLen := 7
		if payload[1]&0x01 == 0 {
			adtsHeaderLen = 9
		}
		if len(payload) > adtsHeaderLen {
			payload = payload[adtsHeaderLen:]
		}
	}

	if d.onAudio != nil {
		d.onAudio(payload, d.audioConfig)
	}
}

func stripPESHeader(data []byte) []byte {
	if len(data) < 9 {
		logger.Debugf("stripPES: too short len=%d", len(data))
		return data
	}
	if data[0] != 0 || data[1] != 0 || data[2] != 1 {
		logger.Debugf("stripPES: no start code, first3=%02x%02x%02x", data[0], data[1], data[2])
		return data
	}
	streamID := data[3]
	if streamID < 0xC0 || streamID > 0xEF {
		logger.Debugf("stripPES: not audio/video streamID=0x%02x", streamID)
		return data
	}
	pesHeaderLen := int(data[8])
	totalHeaderLen := 9 + pesHeaderLen
	logger.Debugf("stripPES: streamID=0x%02x pesHeaderLen=%d totalHeaderLen=%d dataLen=%d", streamID, pesHeaderLen, totalHeaderLen, len(data))
	if totalHeaderLen >= len(data) {
		logger.Debugf("stripPES: header >= data, returning nil")
		return nil
	}
	return data[totalHeaderLen:]
}

func DetectTSPIDs(data []byte) (videoPID uint16, audioPID uint16) {
	videoPID = 0
	audioPID = 0
	pmtPID := uint16(0)

	for i := 0; i+tsPacketSize <= len(data); i += tsPacketSize {
		pkt := data[i : i+tsPacketSize]
		if pkt[0] != 0x47 {
			continue
		}
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])

		af := (pkt[3] >> 5) & 0x03
		payloadStart := 4
		if af == 0x02 {
			continue
		}
		if af == 0x03 {
			if int(4+pkt[4]) < tsPacketSize {
				payloadStart = 5 + int(pkt[4])
			}
		}
		if payloadStart >= tsPacketSize {
			continue
		}
		payload := pkt[payloadStart:]

		hasPayloadStart := pkt[1]&0x40 != 0

		if pid == 0 && hasPayloadStart && len(payload) > 1 {
			pointerField := int(payload[0])
			patStart := 1 + pointerField
			if patStart+8 < len(payload) {
				tableId := payload[patStart]
				if tableId == 0 {
					sectionLen := int(payload[patStart+1]&0x0F)<<8 | int(payload[patStart+2])
					offset := patStart + 8
					for offset+4 <= patStart+3+sectionLen && offset+4 <= len(payload) {
						progNum := int(payload[offset])<<8 | int(payload[offset+1])
						if progNum > 0 {
							pmtPID = uint16(payload[offset+2]&0x1F)<<8 | uint16(payload[offset+3])
							logger.Infof("TS: found PMT PID=%d (program=%d)", pmtPID, progNum)
							break
						}
						offset += 4
					}
				}
			}
		}

		if pid == pmtPID && pmtPID > 0 && len(payload) > 1 {
			pmtStart := 0
			if hasPayloadStart {
				pointerField := int(payload[0])
				pmtStart = 1 + pointerField
			}
			if pmtStart+12 < len(payload) {
				tableId := payload[pmtStart]
				if tableId == 2 {
					sectionLen := int(payload[pmtStart+1]&0x0F)<<8 | int(payload[pmtStart+2])
					progInfoLen := int(payload[pmtStart+10]&0x0F)<<8 | int(payload[pmtStart+11])
					logger.Infof("TS: PMT packet found pid=%d sectionLen=%d progInfoLen=%d", pid, sectionLen, progInfoLen)
					offset := pmtStart + 12 + progInfoLen
					for offset+5 <= len(payload) && offset+5 <= pmtStart+3+sectionLen {
						streamType := payload[offset]
						elemPID := uint16(payload[offset+1]&0x1F)<<8 | uint16(payload[offset+2])
						esInfoLen := int(payload[offset+3]&0x0F)<<8 | int(payload[offset+4])
						logger.Infof("TS: PMT entry streamType=0x%02x PID=%d esInfoLen=%d", streamType, elemPID, esInfoLen)
						if streamType == 0x1b {
							if videoPID == 0 {
								videoPID = elemPID
								logger.Infof("TS: video PID=%d (H.264)", elemPID)
							}
						} else if streamType == 0x0f || streamType == 0x81 || streamType == 0x87 {
							if audioPID == 0 {
								audioPID = elemPID
								logger.Infof("TS: audio PID=%d (AAC/AC3)", elemPID)
							}
						}
						offset += 5 + esInfoLen
					}
				}
			}
		}

		if videoPID > 0 && audioPID > 0 {
			return
		}
	}

	return
}
