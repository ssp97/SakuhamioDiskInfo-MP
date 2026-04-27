package smart

import "encoding/binary"

func ExtractTemperature(disk *RawDisk) float64 {
	if disk == nil {
		return 0
	}
	if disk.Basic.Protocol == "NVMe" {
		return extractNVMeTemp(disk.Raw.SmartHealthLog)
	}
	return extractATATemp(disk.Raw.SmartReadData)
}

func extractNVMeTemp(log []byte) float64 {
	if len(log) < 4 {
		return 0
	}
	tempK := binary.LittleEndian.Uint16(log[1:3])
	if tempK > 273 {
		return float64(tempK - 273)
	}
	return 0
}

func extractATATemp(data []byte) float64 {
	if len(data) < 512 {
		return 0
	}
	for i := 0; i < 30; i++ {
		base := 2 + i*12
		if base+6 > len(data) {
			break
		}
		id := data[base]
		if id == 0xBE || id == 0xC2 {
			temp := data[base+5]
			if temp > 0 && temp < 100 {
				return float64(temp)
			}
		}
	}
	return 0
}
