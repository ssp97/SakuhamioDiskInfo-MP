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
	busTypeUsb                    = 0x07

	ioctlScsiPassThroughDirect = 0x0004D014
	ioctlScsiPassThrough       = 0x0004D004 // buffered version (CDI approach)
	scsiDataIn                 = 1
	scsiDataOut                = 2
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
	// Use 0 for dwFlagsAndAttributes to match CrystalDiskInfo's CreateFile call
	return syscall.CreateFile(
		path,
		genericRead|genericWrite,
		fileShareRead|fileShareWrite,
		nil,
		openExisting,
		0,
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

	if disk.IsUSB {
		if ctrl, ns, log, err := readUSBDriveNVMe(h); err == nil {
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
	queryBuf := make([]byte, 16)
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

// scsiPassThroughBuffered sends SCSI command via IOCTL_SCSI_PASS_THROUGH (buffered, 0x4D004).
// Uses SCSI_PASS_THROUGH_WITH_BUFFERS layout (matching CrystalDiskInfo):
//
//	SCSI_PASS_THROUGH (MSVC default alignment, x64 = 52, x86 = 44):
//	  Length(2) ScsiStatus(1) PathId(1) TargetId(1) Lun(1) CdbLength(1)
//	  SenseInfoLength(1) DataIn(1) pad(3) DataTransferLength(4) TimeOutValue(4)
//	  pad(4) DataBufferOffset(8) SenseInfoOffset(4) Cdb[16]   (x64)
//	  DataBufferOffset(4) SenseInfoOffset(4) Cdb[16]           (x86)
//	+ ULONG Filler + SenseBuf[32] + DataBuf[N]
func scsiPassThroughBuffered(h syscall.Handle, cdb []byte, dataIn byte, dataLen int) ([]byte, error) {
	const senseLen = 32

	// sizeof with MSVC default alignment: 52 x64, 44 x86
	sptSize := 44
	cdbOff := 28
	dataBufOff := 20 // DataBufferOffset field offset in SPT
	senseOffOff := 24 // SenseInfoOffset field offset in SPT
	if unsafe.Sizeof(uintptr(0)) == 8 {
		sptSize = 52
		cdbOff = 36
		dataBufOff = 24
		senseOffOff = 32
	}

	// SCSI_PASS_THROUGH_WITH_BUFFERS = SPT + Filler(4) + SenseBuf(32) + DataBuf(N)
	senseOff := uint32(sptSize + 4)  // SPT + Filler (ULONG)
	dataOff := senseOff + senseLen    // SPT + Filler + SenseBuf
	totalSize := int(dataOff) + dataLen
	buf := make([]byte, totalSize)

	binary.LittleEndian.PutUint16(buf[0:], uint16(sptSize))
	buf[4] = 0               // TargetId (UCHAR)
	buf[5] = 0               // Lun
	buf[6] = byte(len(cdb))  // CdbLength
	buf[7] = senseLen        // SenseInfoLength
	buf[8] = dataIn          // DataIn (1=read, 0=write)
	binary.LittleEndian.PutUint32(buf[12:], uint32(dataLen))
	binary.LittleEndian.PutUint32(buf[16:], 2)

	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint64(buf[dataBufOff:], uint64(dataOff))
	} else {
		binary.LittleEndian.PutUint32(buf[dataBufOff:], dataOff)
	}
	binary.LittleEndian.PutUint32(buf[senseOffOff:], senseOff)

	copy(buf[cdbOff:], cdb)

	if err := deviceIoControl(h, ioctlScsiPassThrough, buf, buf); err != nil {
		return nil, err
	}
	result := make([]byte, dataLen)
	copy(result, buf[dataOff:])
	return result, nil
}

// scsiPassThroughDirect48 sends SCSI command via IOCTL_SCSI_PASS_THROUGH_DIRECT (0x4D014).
// Uses SCSI_PASS_THROUGH_DIRECT layout (METHOD_BUFFERED, USHORT TargetId):
//
//	Length(2) ScsiStatus(1) PathId(1) TargetId(2) Lun(1) CdbLength(1)
//	SenseInfoLength(1) DataIn(1) pad(2) DataTransferLength(4) TimeOutValue(4)
//	DataBuffer(8/4) SenseInfoOffset(4) Cdb[16]
//	Total: 48 x64 / 44 x86 (with pack(4) alignment)
//
// DataBuffer is a USER-MODE POINTER to the data area within buf.
func scsiPassThroughDirect48(h syscall.Handle, cdb []byte, dataIn byte, dataLen int) ([]byte, error) {
	const senseLen = 32

	headerSize := 48
	if unsafe.Sizeof(uintptr(0)) == 4 {
		headerSize = 44
	}
	totalSize := headerSize + senseLen + dataLen
	buf := make([]byte, totalSize)

	binary.LittleEndian.PutUint16(buf[0:], uint16(headerSize)) // Length
	// buf[4-5] TargetId (USHORT) = 0
	buf[6] = 0                // Lun
	buf[7] = byte(len(cdb))   // CdbLength
	buf[8] = senseLen         // SenseInfoLength
	buf[9] = dataIn           // DataIn
	binary.LittleEndian.PutUint32(buf[12:], uint32(dataLen))
	binary.LittleEndian.PutUint32(buf[16:], 2)

	// DataBuffer = absolute pointer to data area within buf
	dataOff := uint32(headerSize + senseLen)
	dataPtr := uintptr(unsafe.Pointer(&buf[headerSize])) + uintptr(int(dataOff)-headerSize)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint64(buf[20:], uint64(dataPtr))
	} else {
		binary.LittleEndian.PutUint32(buf[20:], uint32(dataPtr))
	}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint32(buf[28:], uint32(headerSize)) // SenseInfoOffset
	} else {
		binary.LittleEndian.PutUint32(buf[24:], uint32(headerSize)) // SenseInfoOffset
	}
	cdbOff := headerSize - 16
	copy(buf[cdbOff:], cdb)

	if err := deviceIoControl(h, ioctlScsiPassThroughDirect, buf, buf); err != nil {
		return nil, err
	}
	result := make([]byte, dataLen)
	copy(result, buf[dataOff:])
	return result, nil
}

type scsiPassFn func(h syscall.Handle, cdb []byte, dataIn byte, dataLen int) ([]byte, error)

// readUSBDriveNVMe attempts to read NVMe Identify + SMART data from a USB-attached
// NVMe drive using vendor-specific CDBs (matching CrystalDiskInfo approach):
//
//	Realtek RTL9210:  CDB 0xE4 (16-byte), single-step
//	ASMedia ASM2362:  CDB 0xE6 (16-byte), single-step
//	JMicron JMS583:   CDB 0xA1 (12-byte), two-step (OUT then DMA-IN)
//
// Tries IOCTL_SCSI_PASS_THROUGH (buffered) first, then IOCTL_SCSI_PASS_THROUGH_DIRECT.
// NOTE: On some Windows systems, USB storage class drivers (usbstor.sys) may
// return ERROR_REVISION_MISMATCH for SCSI pass-through IOCTLs, causing this
// function to silently fall back to the "USB unsupported" path.
func readUSBDriveNVMe(h syscall.Handle) (ctrl, ns, log []byte, err error) {
	fns := []scsiPassFn{scsiPassThroughBuffered, scsiPassThroughDirect48}

	// Try both IOCTL types and both bridge protocols
	for _, send := range fns {
		// Realtek RTL9210: CDB 0xE4
		if ctrl, ns, log, err = tryRealtekNVMe(h, send); err == nil {
			return
		}
		// ASMedia ASM2362/2364: CDB 0xE6
		if ctrl, ns, log, err = tryASMediaNVMe(h, send); err == nil {
			return
		}
	}
	return nil, nil, nil, errors.New("USB NVMe passthrough failed")
}

// tryRealtekNVMe tries Realtek RTL9210 protocol (CDB 0xE4)
func tryRealtekNVMe(h syscall.Handle, send scsiPassFn) (ctrl, ns, log []byte, err error) {
	// Identify Controller: CDB 0xE4, cmd=0x06, CNS=1
	cdb := make([]byte, 16)
	tlen := uint16(4096)
	cdb[0] = 0xE4
	cdb[1] = byte(tlen)
	cdb[2] = byte(tlen >> 8)
	cdb[3] = 0x06
	cdb[4] = 0x01

	ctrl, err = send(h, cdb, scsiDataIn, 4096)
	if err != nil || len(ctrl) < 512 || cleanASCII(ctrl[24:64]) == "" {
		return nil, nil, nil, errors.New("Realtek Identify failed")
	}

	// Identify Namespace: CNS=0, NSID=1
	cdb[4] = 0x00
	cdb[7] = 0x01
	ns, err = send(h, cdb, scsiDataIn, 4096)
	if err != nil {
		ns = nil // non-fatal
	}

	// Get Log Page (SMART): cmd=0x02, LID=2
	tlen = uint16(512)
	cdb[1] = byte(tlen)
	cdb[2] = byte(tlen >> 8)
	cdb[3] = 0x02
	cdb[4] = 0x02
	cdb[7] = 0

	log, err = send(h, cdb, scsiDataIn, 512)
	if err != nil || log == nil || len(log) < 512 {
		return nil, nil, nil, errors.New("Realtek SMART failed")
	}
	// Validate: checksum should be non-zero
	sum := 0
	for _, b := range log {
		sum += int(b)
	}
	if sum == 0 {
		return nil, nil, nil, errors.New("Realtek SMART zero data")
	}
	return
}

// tryASMediaNVMe tries ASMedia ASM2362/2364 protocol (CDB 0xE6)
func tryASMediaNVMe(h syscall.Handle, send scsiPassFn) (ctrl, ns, log []byte, err error) {
	cdb := make([]byte, 16)
	cdb[0] = 0xE6
	cdb[1] = 0x06 // Identify
	cdb[3] = 0x01 // CNS=1

	ctrl, err = send(h, cdb, scsiDataIn, 4096)
	if err != nil || len(ctrl) < 512 || cleanASCII(ctrl[24:64]) == "" {
		return nil, nil, nil, errors.New("ASMedia Identify failed")
	}

	// Get Log Page (SMART): cmd=0x02, LID=2, NUMD=0x7F
	cdb[1] = 0x02
	cdb[3] = 0x02
	cdb[7] = 0x7F

	log, err = send(h, cdb, scsiDataIn, 512)
	if err != nil || log == nil || len(log) < 512 {
		return nil, nil, nil, errors.New("ASMedia SMART failed")
	}
	sum := 0
	for _, b := range log {
		sum += int(b)
	}
	if sum == 0 {
		return nil, nil, nil, errors.New("ASMedia SMART zero data")
	}
	return
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
