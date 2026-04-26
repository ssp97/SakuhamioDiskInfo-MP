package smart

import (
	"encoding/binary"
	"strings"
)

func parseNVMeIdentify(ctrl, ns []byte, disk *RawDisk) {
	if len(ctrl) >= 4096 {
		disk.Raw.IdentifyController = cloneBytes(ctrl)
		disk.Basic.Serial = cleanASCII(ctrl[4:24])
		disk.Basic.Model = cleanASCII(ctrl[24:64])
		disk.Basic.Firmware = cleanASCII(ctrl[64:72])
	}
	if len(ns) >= 4096 {
		disk.Raw.IdentifyNamespace = cloneBytes(ns)
		sectors := binary.LittleEndian.Uint64(ns[0:8])
		if sectors == 0 {
			sectors = binary.LittleEndian.Uint64(ns[8:16])
		}
		flbas := ns[26] & 0x0f
		lbafOffset := 128 + int(flbas)*4
		if lbafOffset+4 <= len(ns) {
			lbads := ns[lbafOffset+3] & 0x3f
			if lbads > 0 && lbads < 63 {
				disk.CapacityBytes = sectors * (uint64(1) << lbads)
			}
		}
	}

	disk.Basic.Protocol = "NVMe"
	disk.Basic.SmartSupported = true
	disk.Basic.SmartEnabled = true
	disk.Support.Smart = true
	disk.Support.SmartEnabled = true
	disk.Support.Trim = true
	disk.Support.VolatileWriteCache = true
	disk.Support.NVMeHealthLog = true
	disk.DeviceType = DeviceType{SmartKeyName: "SmartNVMe", DiskVendorID: vendorNVMe, IsSSD: true}
}

func cleanASCII(b []byte) string {
	var out strings.Builder
	for _, c := range b {
		if c >= 32 && c < 127 {
			out.WriteByte(c)
		}
	}
	return strings.TrimSpace(out.String())
}
