//go:build linux

package smart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	linuxSGIO               = 0x2285
	linuxNVMeIOCTLAdminCmd  = 0xC0484E41
	linuxSGDXferFromDev     = -3
	linuxSenseBufferLen     = 32
	linuxNVMeAdminIdentify  = 0x06
	linuxNVMeAdminGetLog    = 0x02
	linuxNVMeIdentifyNS     = 0
	linuxNVMeIdentifyCtrl   = 1
	linuxNVMeSmartHealthLog = 2
)

type nativeCollector struct{}

type linuxNvmeAdminCmd struct {
	Opcode   uint8
	Flags    uint8
	Rsvd1    uint16
	Nsid     uint32
	Cdw2     uint32
	Cdw3     uint32
	Metadata uint64
	Addr     uint64
	MetaLen  uint32
	DataLen  uint32
	Cdw10    uint32
	Cdw11    uint32
	Cdw12    uint32
	Cdw13    uint32
	Cdw14    uint32
	Cdw15    uint32
	Timeout  uint32
	Result   uint32
}

type linuxSGIOHdr struct {
	InterfaceID    int32
	DxferDirection int32
	CmdLen         uint8
	MxSbLen        uint8
	IovecCount     uint16
	DxferLen       uint32
	Dxferp         uintptr
	Cmdp           uintptr
	Sbp            uintptr
	Timeout        uint32
	Flags          uint32
	PackID         int32
	UsrPtr         uintptr
	Status         uint8
	MaskedStatus   uint8
	MsgStatus      uint8
	SbLenWr        uint8
	HostStatus     uint16
	DriverStatus   uint16
	Resid          int32
	Duration       uint32
	Info           uint32
}

func NewCollector() Collector {
	return nativeCollector{}
}

func (nativeCollector) RequirePrivilege() error {
	if os.Geteuid() != 0 {
		return ErrPrivilege{Message: "需要以 root 身份运行，才能读取物理磁盘 SMART 信息"}
	}
	return nil
}

func (c nativeCollector) Scan(force bool) ([]RawDisk, error) {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, err
	}

	disks := make([]RawDisk, 0)
	for _, entry := range entries {
		name := entry.Name()
		if skipLinuxBlock(name) {
			continue
		}
		path := "/dev/" + name
		var disk RawDisk
		if strings.HasPrefix(name, "nvme") && !strings.Contains(name, "p") {
			disk, err = readLinuxNVMe(path, len(disks))
		} else {
			disk, err = readLinuxSATA(path, len(disks), force, nil)
		}
		if err == nil {
			disk.DriveLetters = []string{name}
			disks = append(disks, disk)
		}
	}
	return disks, nil
}

func (nativeCollector) Read(index int, force bool, previous *RawDisk) (RawDisk, error) {
	path := fmt.Sprintf("/dev/%s", filepath.Base(previousPath(index, previous)))
	if previous != nil && previous.Path != "" {
		path = previous.Path
	}
	if strings.Contains(filepath.Base(path), "nvme") {
		return readLinuxNVMe(path, index)
	}
	return readLinuxSATA(path, index, force, previous)
}

func previousPath(index int, previous *RawDisk) string {
	if previous != nil && previous.Path != "" {
		return previous.Path
	}
	return fmt.Sprintf("sd%c", 'a'+index)
}

func readLinuxNVMe(path string, index int) (RawDisk, error) {
	name := filepath.Base(path)
	isUSB, isRemovable := detectLinuxUSB(name)

	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return RawDisk{}, err
	}
	defer f.Close()

	ns := make([]byte, 4096)
	if err := nvmeAdmin(f.Fd(), linuxNVMeAdminIdentify, 1, linuxNVMeIdentifyNS, ns); err != nil {
		return RawDisk{}, err
	}
	ctrl := make([]byte, 4096)
	if err := nvmeAdmin(f.Fd(), linuxNVMeAdminIdentify, 0, linuxNVMeIdentifyCtrl, ctrl); err != nil {
		return RawDisk{}, err
	}
	log := make([]byte, 512)

	disk := RawDisk{ID: name, Index: index, Path: path, SmartState: SmartStateUnavailable, IsUSB: isUSB, IsRemovable: isRemovable}
	disk.DriveLetters = []string{name}
	parseNVMeIdentify(ctrl, ns, &disk)
	if err := nvmeAdmin(f.Fd(), linuxNVMeAdminGetLog, 0xFFFFFFFF, linuxNVMeSmartHealthLog|(127<<16), log); err != nil {
		disk.SmartState = SmartStateError
		disk.LastUpdateError = err.Error()
		return disk, nil
	}
	disk.Raw.SmartHealthLog = cloneBytes(log)
	disk.SmartState = SmartStateOK
	disk.LastSmartAt = nowUTC()
	return disk, nil
}

func readLinuxSATA(path string, index int, force bool, previous *RawDisk) (RawDisk, error) {
	name := filepath.Base(path)
	isUSB, isRemovable := detectLinuxUSB(name)

	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return RawDisk{}, err
	}
	defer f.Close()

	identify, err := ataSGIO(f.Fd(), ataCmdIdentify, 0, 0, 0, 0, 0)
	if err != nil && isUSB {
		identify, err = ataSGIO12(f.Fd(), ataCmdIdentify, 0, 0, 0, 0, 0)
	}
	if err != nil {
		return RawDisk{}, err
	}
	disk := RawDisk{ID: name, Index: index, Path: path, SmartState: SmartStateUnavailable, IsUSB: isUSB, IsRemovable: isRemovable}
	disk.DriveLetters = []string{name}
	parseATAIdentify(identify, &disk)
	if !disk.Support.Smart {
		disk.SmartState = SmartStateUnsupported
		disk.SmartMessage = "设备不支持 S.M.A.R.T."
		return disk, nil
	}
	if !disk.Support.SmartEnabled {
		disk.SmartState = SmartStateDisabled
		disk.SmartMessage = "S.M.A.R.T. 未启用"
		return disk, nil
	}
	if !force {
		active, err := ataCheckPowerModeLinux(f.Fd())
		if err == nil && !active {
			mergePreviousSMART(&disk, previous)
			disk.SmartState = SmartStateAsleep
			disk.SmartMessage = "设备已休眠"
			classifyATADevice(&disk)
			return disk, nil
		}
	}

	smartData, err := ataSGIO(f.Fd(), ataCmdSmart, ataReadAttributes, 1, 1, ataSmartCylinderLo, ataSmartCylinderHi)
	if err != nil && disk.IsUSB {
		smartData, err = ataSGIO12(f.Fd(), ataCmdSmart, ataReadAttributes, 1, 1, ataSmartCylinderLo, ataSmartCylinderHi)
	}
	if err != nil {
		mergePreviousSMART(&disk, previous)
		disk.LastUpdateError = err.Error()
		disk.SmartState = SmartStateError
		return disk, nil
	}
	disk.Raw.SmartReadData = cloneBytes(smartData)
	thresholds, err := ataSGIO(f.Fd(), ataCmdSmart, ataReadThresholds, 1, 1, ataSmartCylinderLo, ataSmartCylinderHi)
	if err != nil && disk.IsUSB {
		thresholds, _ = ataSGIO12(f.Fd(), ataCmdSmart, ataReadThresholds, 1, 1, ataSmartCylinderLo, ataSmartCylinderHi)
	}
	if len(thresholds) > 0 {
		disk.Raw.SmartReadThreshold = cloneBytes(thresholds)
	}
	if stats := readATADeviceStatisticsLinux(f.Fd()); len(stats) > 0 {
		disk.Raw.DeviceStatistics = stats
	}
	disk.SmartState = SmartStateOK
	disk.LastSmartAt = nowUTC()
	classifyATADevice(&disk)
	return disk, nil
}

// ataSGIO48 issues an ATA PASS-THROUGH(16) command with EXTEND=1 (48-bit).
// Used for READ LOG EXT (command 0x2F).
func ataSGIO48(fd uintptr, command, logAddr, pageNum byte) ([]byte, error) {
	data := make([]byte, ataSmartSize)
	sense := make([]byte, linuxSenseBufferLen)
	cdb := make([]byte, 16)
	cdb[0] = 0x85 // ATA PASS-THROUGH(16)
	cdb[1] = 0x09 // PIO data-in (protocol 4 << 1), EXTEND=1
	cdb[2] = 0x0e // T_DIR=from device, BYT_BLOK=1, T_LENGTH=2 (sector count)
	// cdb[3] = Features[15:8] = 0
	// cdb[4] = Features[7:0]  = 0
	// cdb[5] = SectorCount[15:8] = 0
	cdb[6] = 1 // SectorCount[7:0] = 1 sector
	// cdb[7] = LBA[31:24] = 0
	cdb[8] = logAddr // LBA[7:0] = log address
	// cdb[9] = LBA[39:32] = 0
	cdb[10] = pageNum // LBA[15:8] = page number
	// cdb[11] = LBA[47:40] = 0
	// cdb[12] = LBA[23:16] = 0
	cdb[13] = ataDeviceLBA // Device = 0x40 (LBA mode)
	cdb[14] = command

	hdr := linuxSGIOHdr{
		InterfaceID:    int32('S'),
		DxferDirection: linuxSGDXferFromDev,
		CmdLen:         uint8(len(cdb)),
		MxSbLen:        uint8(len(sense)),
		DxferLen:       uint32(len(data)),
		Dxferp:         uintptr(unsafe.Pointer(&data[0])),
		Cmdp:           uintptr(unsafe.Pointer(&cdb[0])),
		Sbp:            uintptr(unsafe.Pointer(&sense[0])),
		Timeout:        10_000,
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, linuxSGIO, uintptr(unsafe.Pointer(&hdr)))
	if errno != 0 {
		return nil, errno
	}
	if hdr.Status != 0 || hdr.HostStatus != 0 || hdr.DriverStatus != 0 {
		return nil, fmt.Errorf("SG_IO status=%d host=%d driver=%d", hdr.Status, hdr.HostStatus, hdr.DriverStatus)
	}
	return data, nil
}

// readATADeviceStatisticsLinux reads GP Log 0x04 (Device Statistics) pages 1-7.
// Returns a 4096-byte blob where page N occupies bytes [N*512 : (N+1)*512],
// or nil if no page could be read.
func readATADeviceStatisticsLinux(fd uintptr) []byte {
	result := make([]byte, 8*512)
	anyValid := false
	for p := byte(1); p <= 7; p++ {
		data, err := ataSGIO48(fd, ataCmdReadLogExt, 0x04, p)
		if err != nil || len(data) < 512 || data[2] != p {
			continue
		}
		copy(result[int(p)*512:], data)
		anyValid = true
	}
	if !anyValid {
		return nil
	}
	return result
}

func nvmeAdmin(fd uintptr, opcode uint8, nsid, cdw10 uint32, data []byte) error {
	if len(data) == 0 {
		return errors.New("empty nvme buffer")
	}
	cmd := linuxNvmeAdminCmd{
		Opcode:  opcode,
		Nsid:    nsid,
		Addr:    uint64(uintptr(unsafe.Pointer(&data[0]))),
		DataLen: uint32(len(data)),
		Cdw10:   cdw10,
		Timeout: 10_000,
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, linuxNVMeIOCTLAdminCmd, uintptr(unsafe.Pointer(&cmd)))
	if errno != 0 {
		return errno
	}
	return nil
}

func ataSGIO(fd uintptr, command, feature, sectorCount, sectorNumber, cylinderLo, cylinderHi byte) ([]byte, error) {
	data := make([]byte, ataSmartSize)
	sense := make([]byte, linuxSenseBufferLen)
	cdb := make([]byte, 16)
	cdb[0] = 0x85
	cdb[1] = 0x08
	cdb[2] = 0x0e
	cdb[4] = feature
	cdb[6] = sectorCount
	cdb[8] = sectorNumber
	cdb[10] = cylinderLo
	cdb[12] = cylinderHi
	cdb[13] = ataDriveHead
	cdb[14] = command

	hdr := linuxSGIOHdr{
		InterfaceID:    int32('S'),
		DxferDirection: linuxSGDXferFromDev,
		CmdLen:         uint8(len(cdb)),
		MxSbLen:        uint8(len(sense)),
		DxferLen:       uint32(len(data)),
		Dxferp:         uintptr(unsafe.Pointer(&data[0])),
		Cmdp:           uintptr(unsafe.Pointer(&cdb[0])),
		Sbp:            uintptr(unsafe.Pointer(&sense[0])),
		Timeout:        10_000,
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, linuxSGIO, uintptr(unsafe.Pointer(&hdr)))
	if errno != 0 {
		return nil, errno
	}
	if hdr.Status != 0 || hdr.HostStatus != 0 || hdr.DriverStatus != 0 {
		return nil, fmt.Errorf("SG_IO status=%d host=%d driver=%d", hdr.Status, hdr.HostStatus, hdr.DriverStatus)
	}
	_ = binary.LittleEndian
	return data, nil
}

func ataSGIO12(fd uintptr, command, feature, sectorCount, sectorNumber, cylinderLo, cylinderHi byte) ([]byte, error) {
	data := make([]byte, ataSmartSize)
	sense := make([]byte, linuxSenseBufferLen)
	cdb := make([]byte, 12)
	cdb[0] = 0xA1
	cdb[1] = 0x04
	cdb[2] = 0x0e
	cdb[4] = feature
	cdb[5] = sectorCount
	cdb[6] = sectorNumber
	cdb[7] = cylinderLo
	cdb[8] = cylinderHi
	cdb[9] = ataDriveHead
	cdb[10] = command

	hdr := linuxSGIOHdr{
		InterfaceID:    int32('S'),
		DxferDirection: linuxSGDXferFromDev,
		CmdLen:         uint8(len(cdb)),
		MxSbLen:        uint8(len(sense)),
		DxferLen:       uint32(len(data)),
		Dxferp:         uintptr(unsafe.Pointer(&data[0])),
		Cmdp:           uintptr(unsafe.Pointer(&cdb[0])),
		Sbp:            uintptr(unsafe.Pointer(&sense[0])),
		Timeout:        10_000,
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, linuxSGIO, uintptr(unsafe.Pointer(&hdr)))
	if errno != 0 {
		return nil, errno
	}
	if hdr.Status != 0 || hdr.HostStatus != 0 || hdr.DriverStatus != 0 {
		return nil, fmt.Errorf("SG_IO status=%d host=%d driver=%d", hdr.Status, hdr.HostStatus, hdr.DriverStatus)
	}
	return data, nil
}

func ataCheckPowerModeLinux(fd uintptr) (bool, error) {
	sense := make([]byte, linuxSenseBufferLen)
	cdb := make([]byte, 16)
	cdb[0] = 0x85
	cdb[1] = 0x06
	cdb[2] = 0x20
	cdb[13] = ataDriveHead
	cdb[14] = ataCmdCheckPowerMode

	hdr := linuxSGIOHdr{
		InterfaceID:    int32('S'),
		DxferDirection: -1,
		CmdLen:         uint8(len(cdb)),
		MxSbLen:        uint8(len(sense)),
		Cmdp:           uintptr(unsafe.Pointer(&cdb[0])),
		Sbp:            uintptr(unsafe.Pointer(&sense[0])),
		Timeout:        10_000,
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, linuxSGIO, uintptr(unsafe.Pointer(&hdr)))
	if errno != 0 {
		return true, errno
	}
	if sc, ok := ataReturnSectorCount(sense); ok {
		return sc == 0xff || sc == 0x80, nil
	}
	return true, nil
}

func ataReturnSectorCount(sense []byte) (byte, bool) {
	for i := 0; i+13 < len(sense); i++ {
		if sense[i] == 0x09 && sense[i+1] >= 0x0c {
			return sense[i+5], true
		}
	}
	return 0, false
}

func skipLinuxBlock(name string) bool {
	if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
		return true
	}
	if strings.HasPrefix(name, "md") || strings.HasPrefix(name, "sr") || strings.HasPrefix(name, "zram") {
		return true
	}
	if strings.HasPrefix(name, "nvme") && strings.Contains(name, "p") {
		return true
	}
	return false
}

func detectLinuxUSB(name string) (isUSB bool, isRemovable bool) {
	if data, err := os.ReadFile("/sys/block/" + name + "/removable"); err == nil {
		isRemovable = strings.TrimSpace(string(data)) == "1"
	}
	if target, err := os.Readlink("/sys/block/" + name); err == nil {
		isUSB = strings.Contains(target, "/usb")
	}
	return
}
