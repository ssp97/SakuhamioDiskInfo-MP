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

	"golang.org/x/sys/windows"
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
	scsiDataOut                = 0
	scsiMaxSenseLen            = 32

	ataFlagsDRDYRequired = 0x01
	ataFlagsDataIn       = 0x02
	ataFlagsExtCommand   = 0x08 // ATA_FLAGS_48BIT_COMMAND

	ataPassThroughSize = 48
	ataDataOffset      = ataPassThroughSize
	ataBufferSize      = ataPassThroughSize + ataIdentifySize
)

var (
	kernel32                            = syscall.NewLazyDLL("kernel32.dll")
	procDeviceIoControl                 = kernel32.NewProc("DeviceIoControl")
	procGetLogicalDrives                = kernel32.NewProc("GetLogicalDrives")
	setupapiDLL                         = windows.NewLazySystemDLL("setupapi.dll")
	cfgmgr32DLL                         = windows.NewLazySystemDLL("cfgmgr32.dll")
	procSetupDiEnumDeviceInterfaces     = setupapiDLL.NewProc("SetupDiEnumDeviceInterfaces")
	procSetupDiGetDeviceInterfaceDetail = setupapiDLL.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procCMGetParent                     = cfgmgr32DLL.NewProc("CM_Get_Parent")
	procCMGetDevNodeProperty            = cfgmgr32DLL.NewProc("CM_Get_DevNode_PropertyW")
)

var (
	guidDevInterfaceDisk             = windows.GUID{Data1: 0x53F56307, Data2: 0xB6BF, Data3: 0x11D0, Data4: [8]byte{0x94, 0xF2, 0x00, 0xA0, 0xC9, 0x1E, 0xFB, 0x8B}}
	pciDevicePropSeed                = windows.DEVPROPGUID{Data1: 0x3ab22e31, Data2: 0x8264, Data3: 0x4b4e, Data4: [8]byte{0x9a, 0xf5, 0xa8, 0xd2, 0xd8, 0xe3, 0x3e, 0x62}}
	devpkeyPciDeviceCurrentLinkSpeed = windows.DEVPROPKEY{FmtID: pciDevicePropSeed, PID: 9}
	devpkeyPciDeviceCurrentLinkWidth = windows.DEVPROPKEY{FmtID: pciDevicePropSeed, PID: 10}
	devpkeyPciDeviceMaxLinkSpeed     = windows.DEVPROPKEY{FmtID: pciDevicePropSeed, PID: 11}
	devpkeyPciDeviceMaxLinkWidth     = windows.DEVPROPKEY{FmtID: pciDevicePropSeed, PID: 12}
)

type scsiPassThroughHeader struct {
	Length             uint16
	ScsiStatus         byte
	PathID             byte
	TargetID           byte
	Lun                byte
	CdbLength          byte
	SenseInfoLength    byte
	DataIn             byte
	DataTransferLength uint32
	TimeOutValue       uint32
	DataBufferOffset   uintptr
	SenseInfoOffset    uint32
	Cdb                [16]byte
}

type scsiPassThroughDirectHeader struct {
	Length             uint16
	ScsiStatus         byte
	PathID             byte
	TargetID           uint16
	Lun                byte
	CdbLength          byte
	SenseInfoLength    byte
	DataIn             byte
	_                  uint16
	DataTransferLength uint32
	TimeOutValue       uint32
	DataBuffer         uintptr
	SenseInfoOffset    uint32
	Cdb                [16]byte
}

type deviceInterfaceData struct {
	Size               uint32
	InterfaceClassGUID windows.GUID
	Flags              uint32
	Reserved           uintptr
}

type deviceInterfaceDetailData struct {
	Size       uint32
	DevicePath uint16
}

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
		disk.Basic.TransferMode = windowsNVMeTransferMode(index)
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
			if ctrl, ns, log, usbErr := readUSBDriveNVMe(h); usbErr == nil {
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
					disk.IsUSB = true
					return disk, nil
				}
			}
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

func windowsNVMeTransferMode(physicalDrive int) string {
	devInfo, err := windows.SetupDiGetClassDevsEx(&guidDevInterfaceDisk, "", 0, windows.DIGCF_PRESENT|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return ""
	}
	defer devInfo.Close()

	for i := 0; ; i++ {
		iface, err := setupDiEnumDeviceInterfaces(devInfo, &guidDevInterfaceDisk, uint32(i))
		if err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
				break
			}
			continue
		}

		path, info, err := setupDiGetDeviceInterfaceDetail(devInfo, iface)
		if err != nil {
			continue
		}

		h, err := openDevicePath(path)
		if err != nil {
			continue
		}
		deviceNumber, err := storageDeviceNumber(syscall.Handle(h))
		windows.CloseHandle(h)
		if err != nil || deviceNumber != uint32(physicalDrive) {
			continue
		}

		return windowsPCIeTransferMode(info.DevInst)
	}
	return ""
}

func setupDiEnumDeviceInterfaces(devInfo windows.DevInfo, classGUID *windows.GUID, memberIndex uint32) (*deviceInterfaceData, error) {
	data := &deviceInterfaceData{Size: uint32(unsafe.Sizeof(deviceInterfaceData{}))}
	r1, _, e1 := procSetupDiEnumDeviceInterfaces.Call(
		uintptr(devInfo),
		0,
		uintptr(unsafe.Pointer(classGUID)),
		uintptr(memberIndex),
		uintptr(unsafe.Pointer(data)),
	)
	if r1 == 0 {
		if e1 != nil && e1 != syscall.Errno(0) {
			return nil, e1
		}
		return nil, syscall.EINVAL
	}
	return data, nil
}

func setupDiGetDeviceInterfaceDetail(devInfo windows.DevInfo, iface *deviceInterfaceData) (string, *windows.DevInfoData, error) {
	var requiredSize uint32
	info := &windows.DevInfoData{}
	*(*uint32)(unsafe.Pointer(info)) = uint32(unsafe.Sizeof(*info))

	r1, _, e1 := procSetupDiGetDeviceInterfaceDetail.Call(
		uintptr(devInfo),
		uintptr(unsafe.Pointer(iface)),
		0,
		0,
		uintptr(unsafe.Pointer(&requiredSize)),
		uintptr(unsafe.Pointer(info)),
	)
	if r1 == 0 && !errors.Is(e1, windows.ERROR_INSUFFICIENT_BUFFER) {
		if e1 != nil && e1 != syscall.Errno(0) {
			return "", nil, e1
		}
		return "", nil, syscall.EINVAL
	}
	if requiredSize == 0 {
		return "", nil, syscall.EINVAL
	}

	buf := make([]byte, requiredSize)
	detail := (*deviceInterfaceDetailData)(unsafe.Pointer(&buf[0]))
	detail.Size = deviceInterfaceDetailDataSize()
	info = &windows.DevInfoData{}
	*(*uint32)(unsafe.Pointer(info)) = uint32(unsafe.Sizeof(*info))

	r1, _, e1 = procSetupDiGetDeviceInterfaceDetail.Call(
		uintptr(devInfo),
		uintptr(unsafe.Pointer(iface)),
		uintptr(unsafe.Pointer(detail)),
		uintptr(requiredSize),
		uintptr(unsafe.Pointer(&requiredSize)),
		uintptr(unsafe.Pointer(info)),
	)
	if r1 == 0 {
		if e1 != nil && e1 != syscall.Errno(0) {
			return "", nil, e1
		}
		return "", nil, syscall.EINVAL
	}

	pathPtr := (*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(detail)) + unsafe.Offsetof(deviceInterfaceDetailData{}.DevicePath)))
	return windows.UTF16PtrToString(pathPtr), info, nil
}

func deviceInterfaceDetailDataSize() uint32 {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return 8
	}
	return 6
}

func openDevicePath(path string) (windows.Handle, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(pathPtr, 0, fileShareRead|fileShareWrite, nil, openExisting, 0, 0)
}

func windowsPCIeTransferMode(devInst windows.DEVINST) string {
	for devInst != 0 {
		maxSpeed, maxSpeedOK := cmGetDevNodeUint32(devInst, &devpkeyPciDeviceMaxLinkSpeed)
		maxWidth, maxWidthOK := cmGetDevNodeUint32(devInst, &devpkeyPciDeviceMaxLinkWidth)
		curSpeed, curSpeedOK := cmGetDevNodeUint32(devInst, &devpkeyPciDeviceCurrentLinkSpeed)
		curWidth, curWidthOK := cmGetDevNodeUint32(devInst, &devpkeyPciDeviceCurrentLinkWidth)

		maxMode := ""
		curMode := ""
		if maxSpeedOK || maxWidthOK {
			maxMode = formatPCIeTransferMode(pcieSpeedName(maxSpeed), int(maxWidth))
		}
		if curSpeedOK || curWidthOK {
			curMode = formatPCIeTransferMode(pcieSpeedName(curSpeed), int(curWidth))
		}
		switch {
		case maxMode != "" && curMode != "":
			return maxMode + " | " + curMode
		case curMode != "":
			return curMode + " | " + curMode
		case maxMode != "":
			return maxMode + " | " + maxMode
		}

		parent, err := cmGetParent(devInst)
		if err != nil || parent == devInst {
			break
		}
		devInst = parent
	}
	return ""
}

func cmGetParent(devInst windows.DEVINST) (windows.DEVINST, error) {
	var parent windows.DEVINST
	r1, _, _ := procCMGetParent.Call(
		uintptr(unsafe.Pointer(&parent)),
		uintptr(devInst),
		0,
	)
	if windows.CONFIGRET(r1) != windows.CR_SUCCESS {
		return 0, fmt.Errorf("CM_Get_Parent failed: %#x", uint32(r1))
	}
	return parent, nil
}

func cmGetDevNodeUint32(devInst windows.DEVINST, key *windows.DEVPROPKEY) (uint32, bool) {
	var propertyType windows.DEVPROPTYPE
	var value uint32
	size := uint32(unsafe.Sizeof(value))
	r1, _, _ := procCMGetDevNodeProperty.Call(
		uintptr(devInst),
		uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&propertyType)),
		uintptr(unsafe.Pointer(&value)),
		uintptr(unsafe.Pointer(&size)),
		0,
	)
	if windows.CONFIGRET(r1) != windows.CR_SUCCESS || propertyType != windows.DEVPROP_TYPE_UINT32 {
		return 0, false
	}
	return value, true
}

func pcieSpeedName(value uint32) string {
	switch value {
	case 1:
		return "2.5 GT/s"
	case 2:
		return "5.0 GT/s"
	case 3:
		return "8.0 GT/s"
	case 4:
		return "16.0 GT/s"
	case 5:
		return "32.0 GT/s"
	case 6:
		return "64.0 GT/s"
	default:
		return ""
	}
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
	return scsiPassThroughBufferedData(h, cdb, dataIn, make([]byte, dataLen))
}

func scsiPassThroughBufferedData(h syscall.Handle, cdb []byte, dataIn byte, payload []byte) ([]byte, error) {
	const senseLen = 32

	sptSize := int(unsafe.Sizeof(scsiPassThroughHeader{}))
	cdbOff := int(unsafe.Offsetof(scsiPassThroughHeader{}.Cdb))
	dataBufOff := int(unsafe.Offsetof(scsiPassThroughHeader{}.DataBufferOffset))
	senseOffOff := int(unsafe.Offsetof(scsiPassThroughHeader{}.SenseInfoOffset))

	// SCSI_PASS_THROUGH_WITH_BUFFERS = SPT + Filler(4) + SenseBuf(32) + DataBuf(N)
	senseOff := uint32(sptSize + 4) // SPT + Filler (ULONG)
	dataOff := senseOff + senseLen  // SPT + Filler + SenseBuf
	dataLen := len(payload)
	totalSize := int(dataOff) + dataLen
	buf := make([]byte, totalSize)

	binary.LittleEndian.PutUint16(buf[0:], uint16(sptSize))
	buf[4] = 0              // TargetId (UCHAR)
	buf[5] = 0              // Lun
	buf[6] = byte(len(cdb)) // CdbLength
	buf[7] = senseLen       // SenseInfoLength
	buf[8] = dataIn         // DataIn (1=read, 0=write)
	binary.LittleEndian.PutUint32(buf[12:], uint32(dataLen))
	binary.LittleEndian.PutUint32(buf[16:], 2)

	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint64(buf[dataBufOff:], uint64(dataOff))
	} else {
		binary.LittleEndian.PutUint32(buf[dataBufOff:], dataOff)
	}
	binary.LittleEndian.PutUint32(buf[senseOffOff:], senseOff)

	copy(buf[cdbOff:], cdb)
	copy(buf[dataOff:], payload)

	if err := deviceIoControlPartial(h, ioctlScsiPassThrough, buf, uint32(sptSize), buf, uint32(totalSize)); err != nil {
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
	return scsiPassThroughDirect48Data(h, cdb, dataIn, make([]byte, dataLen))
}

func scsiPassThroughDirect48Data(h syscall.Handle, cdb []byte, dataIn byte, payload []byte) ([]byte, error) {
	const senseLen = 32

	headerSize := int(unsafe.Sizeof(scsiPassThroughDirectHeader{}))
	dataLen := len(payload)
	totalSize := headerSize + senseLen + dataLen
	buf := make([]byte, totalSize)

	binary.LittleEndian.PutUint16(buf[0:], uint16(headerSize)) // Length
	// buf[4-5] TargetId (USHORT) = 0
	buf[6] = 0              // Lun
	buf[7] = byte(len(cdb)) // CdbLength
	buf[8] = senseLen       // SenseInfoLength
	buf[9] = dataIn         // DataIn
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
	copy(buf[dataOff:], payload)

	if err := deviceIoControl(h, ioctlScsiPassThroughDirect, buf, buf); err != nil {
		return nil, err
	}
	result := make([]byte, dataLen)
	copy(result, buf[dataOff:])
	return result, nil
}

type scsiPassFn func(h syscall.Handle, cdb []byte, dataIn byte, dataLen int) ([]byte, error)
type scsiPassDataFn func(h syscall.Handle, cdb []byte, dataIn byte, payload []byte) ([]byte, error)

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
	dataFns := []scsiPassDataFn{scsiPassThroughBufferedData, scsiPassThroughDirect48Data}

	for _, sendData := range dataFns {
		// JMicron JMS583/JMS586: CDB 0xA1, two-step OUT + DMA-IN
		if ctrl, ns, log, err = tryJMicronNVMe(h, sendData); err == nil {
			return
		}
	}

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

func tryJMicronNVMe(h syscall.Handle, send scsiPassDataFn) (ctrl, ns, log []byte, err error) {
	cdbOut := []byte{0xA1, 0x80, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	cdbInIdentify := []byte{0xA1, 0x82, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	cdbInSMART := []byte{0xA1, 0x82, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	identifyCmd := make([]byte, 512)
	copy(identifyCmd[:4], []byte("NVME"))
	identifyCmd[8] = 0x06
	identifyCmd[0x30] = 0x01

	if _, err = send(h, cdbOut, scsiDataOut, identifyCmd); err != nil {
		return nil, nil, nil, errors.New("JMicron Identify submit failed")
	}
	ctrl, err = send(h, cdbInIdentify, scsiDataIn, make([]byte, 4096))
	if err != nil || len(ctrl) < 512 || cleanASCII(ctrl[24:64]) == "" {
		return nil, nil, nil, errors.New("JMicron Identify fetch failed")
	}
	if sum := byteSum(ctrl[:512]); sum == 0 || sum == 317 {
		return nil, nil, nil, errors.New("JMicron Identify invalid data")
	}

	smartCmd := make([]byte, 512)
	copy(smartCmd[:4], []byte("NVME"))
	smartCmd[8] = 0x02
	smartCmd[10] = 0x56
	smartCmd[12] = 0xFF
	smartCmd[13] = 0xFF
	smartCmd[14] = 0xFF
	smartCmd[15] = 0xFF
	smartCmd[0x21] = 0x40
	smartCmd[0x22] = 0x7A
	smartCmd[0x30] = 0x02
	smartCmd[0x32] = 0x7F

	if _, err = send(h, cdbOut, scsiDataOut, smartCmd); err != nil {
		return nil, nil, nil, errors.New("JMicron SMART submit failed")
	}
	log, err = send(h, cdbInSMART, scsiDataIn, make([]byte, 512))
	if err != nil || len(log) < 512 {
		return nil, nil, nil, errors.New("JMicron SMART fetch failed")
	}
	if byteSum(log[:512]) == 0 {
		return nil, nil, nil, errors.New("JMicron SMART zero data")
	}
	return ctrl, nil, log, nil
}

func byteSum(buf []byte) int {
	sum := 0
	for _, b := range buf {
		sum += int(b)
	}
	return sum
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
	return deviceIoControlPartial(h, code, in, uint32(len(in)), out, uint32(len(out)))
}

func deviceIoControlPartial(h syscall.Handle, code uint32, in []byte, inLen uint32, out []byte, outLen uint32) error {
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
		uintptr(inLen),
		outPtr,
		uintptr(outLen),
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
