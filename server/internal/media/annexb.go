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
