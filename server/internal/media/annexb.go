package media

// splitAnnexB 将 AnnexB(start code) 流拆分为 NAL 单元（不含 start code）。
func splitAnnexB(data []byte) [][]byte {
	var nalUnits [][]byte
	var currentNAL []byte
	i := 0

	for i < len(data) {
		if i+2 < len(data) && data[i] == 0x00 && data[i+1] == 0x00 {
			scLen := 0
			if i+3 < len(data) && data[i+2] == 0x00 && data[i+3] == 0x01 {
				scLen = 4
			} else if data[i+2] == 0x01 {
				scLen = 3
			}

			if scLen > 0 {
				if len(currentNAL) > 0 {
					nalUnits = append(nalUnits, currentNAL)
				}
				currentNAL = []byte{}
				i += scLen
				continue
			}
		}

		currentNAL = append(currentNAL, data[i])
		i++
	}

	if len(currentNAL) > 0 {
		nalUnits = append(nalUnits, currentNAL)
	}

	return nalUnits
}

// splitCodecConfigHevc 从 HEVC CodecConfig(AnnexB) 分离 VPS/SPS/PPS。
// HEVC NAL header 为 2 字节，nal_unit_type 取第 1 字节低 6 位：
//
//	32=VPS, 33=SPS, 34=PPS
func splitCodecConfigHevc(config []byte) (vps, sps, pps []byte) {
	for _, nal := range splitAnnexB(config) {
		if len(nal) < 2 {
			continue
		}
		nalType := nal[0]>>1 & 0x3F
		switch nalType {
		case 32:
			vps = nal
		case 33:
			sps = nal
		case 34:
			pps = nal
		}
	}
	return vps, sps, pps
}
