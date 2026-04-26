package smart

import (
	"encoding/binary"
	"strings"
)

const (
	ataIdentifySize = 512
	ataSmartSize    = 512

	ataCmdIdentify       = 0xEC
	ataCmdCheckPowerMode = 0xE5
	ataCmdSmart          = 0xB0
	ataCmdReadLogExt     = 0x2F
	ataReadAttributes    = 0xD0
	ataReadThresholds    = 0xD1
	ataSmartCylinderLo   = 0x4F
	ataSmartCylinderHi   = 0xC2
	ataDriveHead         = 0xA0
	ataDeviceLBA         = 0x40
)

const (
	vendorHDDGeneral = iota
	vendorSSDGeneral
	vendorMtron
	vendorIndilinx
	vendorJMicron
	vendorIntel
	vendorSamsung
	vendorSandForce
	vendorMicron
	vendorOCZ
	vendorSeagate
	vendorWDC
	vendorPlextor
	vendorSanDisk
	vendorOCZVector
	vendorToshiba
	vendorCorsair
	vendorKingston
	vendorMicronMU03
	vendorNVMe
	vendorRealtek
	vendorSKHynix
	vendorKioxia
	vendorSSSTC
	vendorIntelDC
	vendorApacer
	vendorSiliconMotion
	vendorPhison
	vendorMarvell
	vendorMaxiotek
	vendorYMTC
	vendorSCY
	vendorJMicron60x
	vendorJMicron61x
	vendorJMicron66x
	vendorSeagateIronWolf
	vendorSeagateBarraCuda
	vendorSanDiskGb
	vendorKingstonSuv
	vendorKingstonKC600
	vendorKingstonDC500
	vendorKingstonSA400
	vendorRecadata
	vendorSanDiskDell
	vendorSanDiskHp
	vendorSanDiskHpVenus
	vendorSanDiskLenovo
	vendorSanDiskLenovoHelenVenus
	vendorSanDiskCloud
	vendorSiliconMotionCVC
	vendorAdataIndustrial
)

func parseATAIdentify(buf []byte, disk *RawDisk) {
	if len(buf) < ataIdentifySize {
		return
	}
	disk.Raw.IdentifyDevice = cloneBytes(buf)
	disk.Basic.Serial = ataString(buf, 10, 20)
	disk.Basic.Firmware = ataString(buf, 23, 8)
	disk.Basic.Model = ataString(buf, 27, 40)
	disk.Basic.Protocol = "SATA"

	disk.Support.Smart = word(buf, 82)&0x0001 != 0
	disk.Support.SmartEnabled = word(buf, 85)&0x0001 != 0
	disk.Support.Trim = word(buf, 169)&0x0001 != 0
	disk.Support.NCQ = word(buf, 76)&0x0100 != 0
	disk.Support.SmartThreshold = disk.Support.Smart
	disk.Basic.SmartSupported = disk.Support.Smart
	disk.Basic.SmartEnabled = disk.Support.SmartEnabled

	sectors := uint64(word(buf, 60)) | uint64(word(buf, 61))<<16
	if word(buf, 83)&0x0400 != 0 {
		sectors = uint64(word(buf, 100)) |
			uint64(word(buf, 101))<<16 |
			uint64(word(buf, 102))<<32 |
			uint64(word(buf, 103))<<48
	}
	if sectors > 0 {
		disk.CapacityBytes = sectors * 512
	}
}

func classifyATADevice(disk *RawDisk) {
	ids := smartAttributeIDs(disk.Raw.SmartReadData)
	model := strings.ToUpper(disk.Basic.Model)

	dt := DeviceType{SmartKeyName: "Smart", DiskVendorID: vendorHDDGeneral}
	rotation := word(disk.Raw.IdentifyDevice, 217)
	isSSD := rotation == 1 || containsAny(model, "SSD", "NVME", "EMMC")
	if isSSD {
		dt = DeviceType{SmartKeyName: "SmartSsd", DiskVendorID: vendorSSDGeneral, IsSSD: true}
	}

	switch {
	case containsAny(model, "SAMSUNG", "MZ-"):
		dt = DeviceType{SmartKeyName: "SmartSamsung", DiskVendorID: vendorSamsung, IsSSD: true}
	case containsAny(model, "INTEL") && containsAny(model, "DC", "D3-", "S3"):
		dt = DeviceType{SmartKeyName: "SmartIntelDc", DiskVendorID: vendorIntelDC, IsSSD: true}
	case containsAny(model, "INTEL"):
		dt = DeviceType{SmartKeyName: "SmartIntel", DiskVendorID: vendorIntel, IsSSD: true}
	case containsAny(model, "SANDISK", "SDSS", "X400", "X600"):
		dt = DeviceType{SmartKeyName: "SmartSanDisk", DiskVendorID: vendorSanDisk, IsSSD: true}
	case containsAny(model, "WDC", "WD ") && isSSD:
		dt = DeviceType{SmartKeyName: "SmartWdc", DiskVendorID: vendorWDC, IsSSD: true}
	case containsAny(model, "KINGSTON"):
		dt = classifyKingston(model)
	case containsAny(model, "CRUCIAL", "MICRON", "MTFD", "CT") && isSSD:
		dt = DeviceType{SmartKeyName: "SmartMicron", DiskVendorID: vendorMicron, IsSSD: true}
	case containsAny(model, "PHISON"):
		dt = DeviceType{SmartKeyName: "SmartPhison", DiskVendorID: vendorPhison, IsSSD: true}
	case containsAny(model, "REALTEK"):
		dt = DeviceType{SmartKeyName: "SmartRealtek", DiskVendorID: vendorRealtek, IsSSD: true}
	case containsAny(model, "SKHYNIX", "SK HYNIX", "HFS", "HFM"):
		dt = DeviceType{SmartKeyName: "SmartSKhynix", DiskVendorID: vendorSKHynix, IsSSD: true}
	case containsAny(model, "KIOXIA", "TOSHIBA") && isSSD:
		dt = DeviceType{SmartKeyName: "SmartKioxia", DiskVendorID: vendorKioxia, IsSSD: true}
	case containsAny(model, "SEAGATE", "ST") && isSSD:
		dt = DeviceType{SmartKeyName: "SmartSeagate", DiskVendorID: vendorSeagate, IsSSD: true}
	case containsAny(model, "JMICRON", "JMF"):
		dt = DeviceType{SmartKeyName: "SmartJMicron", DiskVendorID: vendorJMicron, IsSSD: true}
	case containsAny(model, "SILICONMOTION", "SMI"):
		dt = DeviceType{SmartKeyName: "SmartSiliconMotion", DiskVendorID: vendorSiliconMotion, IsSSD: true}
	case looksLikeSandForce(ids):
		dt = DeviceType{SmartKeyName: "SmartSandForce", DiskVendorID: vendorSandForce, IsSSD: true, IsRawValues7: true}
	case looksLikeJMicron60x(ids):
		dt = DeviceType{SmartKeyName: "SmartJMicron60x", DiskVendorID: vendorJMicron, IsSSD: true, IsRawValues8: true}
	}

	disk.DeviceType = dt
}

func classifyKingston(model string) DeviceType {
	switch {
	case strings.Contains(model, "SUV"):
		return DeviceType{SmartKeyName: "SmartKingstonSuv", DiskVendorID: vendorKingston, IsSSD: true}
	case strings.Contains(model, "KC600"):
		return DeviceType{SmartKeyName: "SmartKingstonKC600", DiskVendorID: vendorKingston, IsSSD: true, HostReadsWritesUnit: "32MB"}
	case strings.Contains(model, "DC500"):
		return DeviceType{SmartKeyName: "SmartKingstonDC500", DiskVendorID: vendorKingston, IsSSD: true}
	case strings.Contains(model, "SA400"):
		return DeviceType{SmartKeyName: "SmartKingstonSA400", DiskVendorID: vendorKingston, IsSSD: true}
	default:
		return DeviceType{SmartKeyName: "SmartKingston", DiskVendorID: vendorKingston, IsSSD: true}
	}
}

func smartAttributeIDs(data []byte) []byte {
	if len(data) < ataSmartSize {
		return nil
	}
	ids := make([]byte, 0, 30)
	for i := 0; i < 30; i++ {
		id := data[2+i*12]
		if id != 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func looksLikeSandForce(ids []byte) bool {
	return hasIDs(ids, 0x01, 0x05, 0x09, 0x0C, 0xAB, 0xAC, 0xB1)
}

func looksLikeJMicron60x(ids []byte) bool {
	return hasIDs(ids, 0x09, 0x0C, 0xC2, 0xE5, 0xE8, 0xE9)
}

func hasIDs(ids []byte, want ...byte) bool {
	for _, w := range want {
		found := false
		for _, id := range ids {
			if id == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(ids) > 0
}

func containsAny(s string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func ataString(buf []byte, wordStart, byteLen int) string {
	start := wordStart * 2
	if start+byteLen > len(buf) {
		return ""
	}
	out := make([]byte, byteLen)
	for i := 0; i < byteLen; i += 2 {
		out[i] = buf[start+i+1]
		out[i+1] = buf[start+i]
	}
	return strings.TrimSpace(string(out))
}

func word(buf []byte, index int) uint16 {
	offset := index * 2
	if offset+2 > len(buf) {
		return 0
	}
	return binary.LittleEndian.Uint16(buf[offset:])
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
