package smart

import "fmt"

type SmartState string

const (
	SmartStateOK          SmartState = "ok"
	SmartStateUnsupported SmartState = "unsupported"
	SmartStateDisabled    SmartState = "disabled"
	SmartStateAsleep      SmartState = "asleep"
	SmartStateError       SmartState = "error"
	SmartStateUnavailable SmartState = "unavailable"
)

type RawDisk struct {
	ID              string     `json:"id"`
	Index           int        `json:"index"`
	Path            string     `json:"path"`
	DriveLetters    []string   `json:"driveLetters"`
	CapacityBytes   uint64     `json:"capacityBytes"`
	Basic           BasicInfo  `json:"basic"`
	Support         Support    `json:"support"`
	DeviceType      DeviceType `json:"deviceType"`
	Raw             RawBlocks  `json:"raw"`
	SmartState      SmartState `json:"smartState"`
	SmartMessage    string     `json:"smartMessage,omitempty"`
	LastSmartAt     string     `json:"lastSmartAt,omitempty"`
	LastUpdateError string     `json:"lastUpdateError,omitempty"`
}

type BasicInfo struct {
	Protocol       string `json:"protocol"`
	Model          string `json:"model"`
	Serial         string `json:"serial"`
	Firmware       string `json:"firmware"`
	SmartSupported bool   `json:"smartSupported"`
	SmartEnabled   bool   `json:"smartEnabled"`
}

type Support struct {
	Smart              bool `json:"smart"`
	SmartEnabled       bool `json:"smartEnabled"`
	SmartThreshold     bool `json:"smartThreshold"`
	Trim               bool `json:"trim"`
	NCQ                bool `json:"ncq"`
	VolatileWriteCache bool `json:"volatileWriteCache"`
	NVMeHealthLog      bool `json:"nvmeHealthLog"`
}

type DeviceType struct {
	SmartKeyName        string `json:"smartKeyName"`
	DiskVendorID        int    `json:"diskVendorId"`
	IsSSD               bool   `json:"isSsd"`
	IsRawValues7        bool   `json:"isRawValues7,omitempty"`
	IsRawValues8        bool   `json:"isRawValues8,omitempty"`
	HostReadsWritesUnit string `json:"hostReadsWritesUnit,omitempty"`
}

type RawBlocks struct {
	IdentifyDevice     []byte `json:"identifyDevice,omitempty"`
	SmartReadData      []byte `json:"smartReadData,omitempty"`
	SmartReadThreshold []byte `json:"smartReadThreshold,omitempty"`
	IdentifyController []byte `json:"identifyController,omitempty"`
	IdentifyNamespace  []byte `json:"identifyNamespace,omitempty"`
	SmartHealthLog     []byte `json:"smartHealthLog,omitempty"`
	DeviceStatistics   []byte `json:"deviceStatistics,omitempty"`
}

type Collector interface {
	RequirePrivilege() error
	Scan(force bool) ([]RawDisk, error)
	Read(index int, force bool, previous *RawDisk) (RawDisk, error)
}

type ErrPrivilege struct {
	Message string
}

func (e ErrPrivilege) Error() string {
	return e.Message
}

func gb(bytes uint64) uint64 {
	return bytes / 1000 / 1000 / 1000
}

func formatCapacity(bytes uint64) string {
	if bytes == 0 {
		return ""
	}
	return fmt.Sprintf("%d GB", gb(bytes))
}
