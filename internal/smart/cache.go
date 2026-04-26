package smart

import "time"

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func mergePreviousSMART(disk *RawDisk, previous *RawDisk) {
	if previous == nil {
		return
	}
	if len(disk.Raw.SmartReadData) == 0 {
		disk.Raw.SmartReadData = cloneBytes(previous.Raw.SmartReadData)
	}
	if len(disk.Raw.SmartReadThreshold) == 0 {
		disk.Raw.SmartReadThreshold = cloneBytes(previous.Raw.SmartReadThreshold)
	}
	if len(disk.Raw.SmartHealthLog) == 0 {
		disk.Raw.SmartHealthLog = cloneBytes(previous.Raw.SmartHealthLog)
	}
	if len(disk.Raw.DeviceStatistics) == 0 {
		disk.Raw.DeviceStatistics = cloneBytes(previous.Raw.DeviceStatistics)
	}
	if disk.LastSmartAt == "" {
		disk.LastSmartAt = previous.LastSmartAt
	}
}
