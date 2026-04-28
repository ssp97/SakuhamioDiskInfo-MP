//go:build windows

package smart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	maxPhysicalDrives = 32

	genericRead       = 0x80000000
	genericWrite      = 0x40000000
	fileShareRead     = 0x00000001
	fileShareWrite    = 0x00000002
	openExisting      = 3
	fileAttributeNorm = 0x00000080

	ioctlStorageQueryProperty = 0x002D1400
	ioctlAtaPassThrough       = 0x0004D02C
	ioctlDiskGetGeometryEx    = 0x000700A0
	ioctlStorageGetDeviceNum  = 0x002D1080

	storageAdapterProtocolSpecificProperty = 49
	propertyStandardQuery                  = 0
	protocolTypeNvme                       = 3
	nvmeDataTypeIdentify                   = 1
	nvmeDataTypeLogPage                    = 2

	propertyStorageDeviceProperty = 0
	busTypeUsb                    = 0x08

	ioctlScsiPassThroughDirect = 0x0004D014
	scsiDataIn                 = 1
	scsiMaxSenseLen            = 32

	ataFlagsDRDYRequired = 0x01
	ataFlagsDataIn       = 0x02
	ataFlagsExtCommand   = 0x08 // ATA_FLAGS_48BIT_COMMAND

	ataPassThroughSize = 48
	ataDataOffset      = ataPassThroughSize
	ataBufferSize      = ataPassThroughSize + ataIdentifySize
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procDeviceIoControl  = kernel32.NewProc("DeviceIoControl")
	procGetLogicalDrives = kernel32.NewProc("GetLogicalDrives")
)

type nativeCollector struct{}

func NewCollector() Collector {
	return nativeCollector{}
}

func (nativeCollector) RequirePrivilege() error {
	var denied bool
	for i := 0; i < maxPhysicalDrives; i++ {
		h, err := openPhysicalDrive(i)
		if err == nil {
			syscall.CloseHandle(h)
			return nil
		}
		if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
			denied = true
		}
	}
	if denied {
		return ErrPrivilege{Message: "需要以管理员身份运行，才能读取物理磁盘 SMART 信息"}
	}
	return nil
}

func (c nativeCollector) Scan(force bool) ([]RawDisk, error) {
	disks := make([]RawDisk, 0)
	var denied bool

	for i := 0; i < maxPhysicalDrives; i++ {
		h, err := openPhysicalDrive(i)
		if err != nil {
			if errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
				denied = true
			}
			continue
		}
		disk, err := readWindowsDisk(i, h, force, nil)
		syscall.CloseHandle(h)
		if err != nil {
			continue
		}
		disks = append(disks, disk)
	}

	if len(disks) == 0 && denied {
		return nil, ErrPrivilege{Message: "需要以管理员身份运行，才能读取物理磁盘 SMART 信息"}
	}
	return disks, nil
}

func (nativeCollector) Read(index int, force bool, previous *RawDisk) (RawDisk, error) {
	h, err := openPhysicalDrive(index)
	if err != nil {
		return RawDisk{}, err
	}
	defer syscall.CloseHandle(h)
	return readWindowsDisk(index, h, force, previous)
}

func openPhysicalDrive(index int) (syscall.Handle, error) {
	path, err := syscall.UTF16PtrFromString(fmt.Sprintf(`\\.\PhysicalDrive%d`, index))
	if err != nil {
		return syscall.InvalidHandle, err
	}
	return syscall.CreateFile(
		path,
		genericRead|genericWrite,
		fileShareRead|fileShareWrite,
		nil,
		openExisting,
		fileAttributeNorm,
		0,
	)
}

func readWindowsDisk(index int, h syscall.Handle, force bool, previous *RawDisk) (RawDisk, error) {
	disk := RawDisk{
		ID:         fmt.Sprintf("pd%d", index),
		Index:      index,
		Path:       fmt.Sprintf(`\\.\PhysicalDrive%d`, index),
		SmartState: SmartStateUnavailable,
	}
	disk.DriveLetters = windowsDriveLetters(index)
	if size, err := windowsDiskSize(h); err == nil {
		disk.CapacityBytes = size
	}

	var descVendor, descProduct, descRevision, descSerial string
	if busType, removable, vendor, product, revision, serial, err := queryStorageDeviceDescriptor(h); err == nil {
		disk.IsUSB = busType == busTypeUsb
		disk.IsRemovable = removable
		descVendor = vendor
		descProduct = product
		descRevision = revision
		descSerial = serial
	}

	if ctrl, ns, log, err := readNVMeWindows(h); err == nil {
		size := disk.CapacityBytes
		parseNVMeIdentify(ctrl, ns, &disk)
		if size > 0 {
			disk.CapacityBytes = size
		}
		if len(log) > 0 {
			disk.Raw.SmartHealthLog = cloneBytes(log)
			disk.SmartState = SmartStateOK
			disk.LastSmartAt = nowUTC()
		} else {
			disk.SmartState = SmartStateError
			disk.SmartMessage = "NVMe SMART 读取失败"
		}
		if disk.Basic.Model != "" {
			return disk, nil
		}
	}

	identify, err := ataCommand(h, disk.IsUSB, ataCmdIdentify, 0, 0, 0, 0, 0)
	if err != nil {
		if !disk.IsUSB {
			return disk, err
		}
		model := strings.TrimSpace(descVendor + " " + descProduct)
		if model == "" {
			model = "USB Mass Storage Device"
		}
		disk.Basic.Model = model
		disk.Basic.Serial = descSerial
		disk.Basic.Firmware = descRevision
		disk.Basic.Protocol = "USB"
		disk.SmartState = SmartStateUnsupported
		disk.SmartMessage = "USB 设备不支持 S.M.A.R.T."
		return disk, nil
	}
	size := disk.CapacityBytes
	parseATAIdentify(identify, &disk)
	if size > 0 {
		disk.CapacityBytes = size
	}
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
		active, err := ataCheckPowerModeWindows(h)
		if err == nil && !active {
			mergePreviousSMART(&disk, previous)
			disk.SmartState = SmartStateAsleep
			disk.SmartMessage = "设备已休眠"
			classifyATADevice(&disk)
			return disk, nil
		}
	}

	smartData, err := ataCommand(h, disk.IsUSB, ataCmdSmart, ataReadAttributes, 1, 1, ataSmartCylinderLo, ataSmartCylinderHi)
	if err != nil {
		mergePreviousSMART(&disk, previous)
		disk.LastUpdateError = err.Error()
		disk.SmartState = SmartStateError
		return disk, nil
	}
	disk.Raw.SmartReadData = cloneBytes(smartData)
	thresholds, _ := ataCommand(h, disk.IsUSB, ataCmdSmart, ataReadThresholds, 1, 1, ataSmartCylinderLo, ataSmartCylinderHi)
	if len(thresholds) > 0 {
		disk.Raw.SmartReadThreshold = cloneBytes(thresholds)
	}
	if stats := readATADeviceStatistics(h); len(stats) > 0 {
		disk.Raw.DeviceStatistics = stats
	}
	disk.SmartState = SmartStateOK
	disk.LastSmartAt = nowUTC()
	classifyATADevice(&disk)
	return disk, nil
}

// ataReadLogPage issues READ LOG EXT (0x2F, 48-bit) to read one 512-byte page
// from the GP log at logAddr, page pageNum.
func ataReadLogPage(h syscall.Handle, logAddr, pageNum byte) ([]byte, error) {
	const dataSize = 512
	buf := make([]byte, ataPassThroughSize+dataSize)

	binary.LittleEndian.PutUint16(buf[0:], ataPassThroughSize)
	binary.LittleEndian.PutUint16(buf[2:], ataFlagsDRDYRequired|ataFlagsDataIn|ataFlagsExtCommand)
	binary.LittleEndian.PutUint32(buf[8:], dataSize)
	binary.LittleEndian.PutUint32(buf[12:], 5)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint64(buf[24:], ataPassThroughSize)
	} else {
		binary.LittleEndian.PutUint32(buf[20:], ataPassThroughSize)
	}

	task := 40
	if unsafe.Sizeof(uintptr(0)) == 4 {
		task = 36
	}
	// PreviousTaskFile (task-8) is all zero (high bytes for 48-bit command are 0)
	buf[task+0] = 0            // Features[7:0]
	buf[task+1] = 1            // SectorCount[7:0] = 1 sector
	buf[task+2] = logAddr      // LBA[7:0] = log address
	buf[task+3] = pageNum      // LBA[15:8] = page number
	buf[task+4] = 0            // LBA[23:16]
	buf[task+5] = ataDeviceLBA // Device = 0x40 (LBA mode)
	buf[task+6] = ataCmdReadLogExt

	if err := deviceIoControl(h, ioctlAtaPassThrough, buf, buf); err != nil {
		return nil, err
	}
	out := make([]byte, dataSize)
	copy(out, buf[ataPassThroughSize:])
	return out, nil
}

// readATADeviceStatistics reads GP Log 0x04 (Device Statistics) pages 1-7.
// Returns a 4096-byte blob where page N occupies bytes [N*512 : (N+1)*512],
// or nil if no page could be read.
func readATADeviceStatistics(h syscall.Handle) []byte {
	result := make([]byte, 8*512)
	anyValid := false
	for p := byte(1); p <= 7; p++ {
		data, err := ataReadLogPage(h, 0x04, p)
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

func windowsDiskSize(h syscall.Handle) (uint64, error) {
	buf := make([]byte, 64)
	if err := deviceIoControl(h, ioctlDiskGetGeometryEx, nil, buf); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(buf[24:32]), nil
}

func windowsDriveLetters(physicalDrive int) []string {
	mask, _, _ := procGetLogicalDrives.Call()
	letters := make([]string, 0)
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		letter := string(rune('A' + i))
		h, err := openVolumeLetter(letter)
		if err != nil {
			continue
		}
		deviceNumber, err := storageDeviceNumber(h)
		syscall.CloseHandle(h)
		if err == nil && deviceNumber == uint32(physicalDrive) {
			letters = append(letters, letter+":")
		}
	}
	return letters
}

func openVolumeLetter(letter string) (syscall.Handle, error) {
	path, err := syscall.UTF16PtrFromString(`\\.\` + letter + `:`)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	return syscall.CreateFile(path, 0, fileShareRead|fileShareWrite, nil, openExisting, fileAttributeNorm, 0)
}

func storageDeviceNumber(h syscall.Handle) (uint32, error) {
	buf := make([]byte, 12)
	if err := deviceIoControl(h, ioctlStorageGetDeviceNum, nil, buf); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(buf[4:8]), nil
}

func readNVMeWindows(h syscall.Handle) ([]byte, []byte, []byte, error) {
	ns, err := nvmeStorageQuery(h, nvmeDataTypeIdentify, 0, 1)
	if err != nil {
		return nil, nil, nil, err
	}
	ctrl, err := nvmeStorageQuery(h, nvmeDataTypeIdentify, 1, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	log, err := nvmeStorageQuery(h, nvmeDataTypeLogPage, 2, 0)
	if err != nil {
		log, err = nvmeStorageQuery(h, nvmeDataTypeLogPage, 2, 0xFFFFFFFF)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return ctrl, ns, log, nil
}

func nvmeStorageQuery(h syscall.Handle, dataType, requestValue, requestSubValue uint32) ([]byte, error) {
	buf := make([]byte, 8+40+4096)
	binary.LittleEndian.PutUint32(buf[0:], storageAdapterProtocolSpecificProperty)
	binary.LittleEndian.PutUint32(buf[4:], propertyStandardQuery)
	binary.LittleEndian.PutUint32(buf[8:], protocolTypeNvme)
	binary.LittleEndian.PutUint32(buf[12:], dataType)
	binary.LittleEndian.PutUint32(buf[16:], requestValue)
	binary.LittleEndian.PutUint32(buf[20:], requestSubValue)
	binary.LittleEndian.PutUint32(buf[24:], 40)
	binary.LittleEndian.PutUint32(buf[28:], 4096)

	if err := deviceIoControl(h, ioctlStorageQueryProperty, buf, buf); err != nil {
		return nil, err
	}
	out := make([]byte, 4096)
	copy(out, buf[48:])
	return out, nil
}

func ataPassThrough(h syscall.Handle, command, feature, sectorCount, sectorNumber, cylinderLo, cylinderHi byte) ([]byte, error) {
	buf := make([]byte, ataBufferSize)
	binary.LittleEndian.PutUint16(buf[0:], ataPassThroughSize)
	binary.LittleEndian.PutUint16(buf[2:], ataFlagsDRDYRequired|ataFlagsDataIn)
	binary.LittleEndian.PutUint32(buf[8:], ataIdentifySize)
	binary.LittleEndian.PutUint32(buf[12:], 5)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint64(buf[24:], ataDataOffset)
	} else {
		binary.LittleEndian.PutUint32(buf[20:], ataDataOffset)
	}

	task := 40
	if unsafe.Sizeof(uintptr(0)) == 4 {
		task = 36
	}
	buf[task+0] = feature
	buf[task+1] = sectorCount
	buf[task+2] = sectorNumber
	buf[task+3] = cylinderLo
	buf[task+4] = cylinderHi
	buf[task+5] = ataDriveHead
	buf[task+6] = command

	if err := deviceIoControl(h, ioctlAtaPassThrough, buf, buf); err != nil {
		return nil, err
	}
	out := make([]byte, ataIdentifySize)
	copy(out, buf[ataDataOffset:])
	return out, nil
}

func ataCheckPowerModeWindows(h syscall.Handle) (bool, error) {
	buf := make([]byte, ataPassThroughSize)
	binary.LittleEndian.PutUint16(buf[0:], ataPassThroughSize)
	binary.LittleEndian.PutUint16(buf[2:], ataFlagsDRDYRequired)
	binary.LittleEndian.PutUint32(buf[12:], 5)

	task := 40
	if unsafe.Sizeof(uintptr(0)) == 4 {
		task = 36
	}
	buf[task+5] = ataDriveHead
	buf[task+6] = ataCmdCheckPowerMode

	if err := deviceIoControl(h, ioctlAtaPassThrough, buf, buf); err != nil {
		return true, err
	}
	sectorCount := buf[task+1]
	return sectorCount == 0xff || sectorCount == 0x80, nil
}

func queryStorageDeviceDescriptor(h syscall.Handle) (busType uint32, removable bool, vendor, product, revision, serial string, err error) {
	queryBuf := make([]byte, 8)
	binary.LittleEndian.PutUint32(queryBuf[0:], propertyStandardQuery)
	binary.LittleEndian.PutUint32(queryBuf[4:], propertyStorageDeviceProperty)

	outBuf := make([]byte, 1024)
	if err := deviceIoControl(h, ioctlStorageQueryProperty, queryBuf, outBuf); err != nil {
		return 0, false, "", "", "", "", err
	}
	busType = binary.LittleEndian.Uint32(outBuf[28:32])
	removable = outBuf[10] != 0
	vendor = descString(outBuf, 12)
	product = descString(outBuf, 16)
	revision = descString(outBuf, 20)
	serial = descString(outBuf, 24)
	return
}

func descString(buf []byte, offsetOff int) string {
	off := int(binary.LittleEndian.Uint32(buf[offsetOff:]))
	if off <= 0 || off >= len(buf) {
		return ""
	}
	end := off
	for end < len(buf) && buf[end] != 0 {
		end++
	}
	return strings.TrimSpace(string(buf[off:end]))
}

func scsiAtaPassThrough16(h syscall.Handle, command, feature, sectorCount, sectorNumber, cylinderLo, cylinderHi byte) ([]byte, error) {
	const dataSize = 512

	headerSize := 48
	if unsafe.Sizeof(uintptr(0)) == 4 {
		headerSize = 44
	}

	buf := make([]byte, headerSize+scsiMaxSenseLen+dataSize)

	binary.LittleEndian.PutUint16(buf[0:], uint16(headerSize))
	binary.LittleEndian.PutUint16(buf[6:], uint16(16)|(uint16(scsiMaxSenseLen)<<8))
	buf[8] = scsiDataIn
	binary.LittleEndian.PutUint32(buf[12:], dataSize)
	binary.LittleEndian.PutUint32(buf[16:], 10)

	dataOff := uint32(headerSize + scsiMaxSenseLen)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint64(buf[20:], uint64(dataOff))
		binary.LittleEndian.PutUint32(buf[28:], uint32(headerSize))
	} else {
		binary.LittleEndian.PutUint32(buf[20:], dataOff)
		binary.LittleEndian.PutUint32(buf[24:], uint32(headerSize))
	}

	cdbOff := headerSize - 16
	buf[cdbOff+0] = 0x85
	buf[cdbOff+1] = 0x08
	buf[cdbOff+2] = 0x0e
	buf[cdbOff+4] = feature
	buf[cdbOff+6] = sectorCount
	buf[cdbOff+8] = sectorNumber
	buf[cdbOff+10] = cylinderLo
	buf[cdbOff+12] = cylinderHi
	buf[cdbOff+13] = ataDriveHead
	buf[cdbOff+14] = command

	if err := deviceIoControl(h, ioctlScsiPassThroughDirect, buf, buf); err != nil {
		return nil, err
	}
	out := make([]byte, dataSize)
	copy(out, buf[dataOff:])
	return out, nil
}

func ataCommand(h syscall.Handle, isUSB bool, command, feature, sectorCount, sectorNumber, cylinderLo, cylinderHi byte) ([]byte, error) {
	data, err := ataPassThrough(h, command, feature, sectorCount, sectorNumber, cylinderLo, cylinderHi)
	if err != nil && isUSB {
		data, err = scsiAtaPassThrough16(h, command, feature, sectorCount, sectorNumber, cylinderLo, cylinderHi)
	}
	return data, err
}

func deviceIoControl(h syscall.Handle, code uint32, in, out []byte) error {
	var returned uint32
	var inPtr, outPtr uintptr
	if len(in) > 0 {
		inPtr = uintptr(unsafe.Pointer(&in[0]))
	}
	if len(out) > 0 {
		outPtr = uintptr(unsafe.Pointer(&out[0]))
	}
	r1, _, errno := procDeviceIoControl.Call(
		uintptr(h),
		uintptr(code),
		inPtr,
		uintptr(uint32(len(in))),
		outPtr,
		uintptr(uint32(len(out))),
		uintptr(unsafe.Pointer(&returned)),
		0,
	)
	if r1 == 0 {
		if errno != nil {
			return errno
		}
		return os.NewSyscallError("DeviceIoControl", syscall.EINVAL)
	}
	return nil
}
