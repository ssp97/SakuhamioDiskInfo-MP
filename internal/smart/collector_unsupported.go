//go:build !windows && !linux

package smart

import "runtime"

type nativeCollector struct{}

func NewCollector() Collector {
	return nativeCollector{}
}

func (nativeCollector) RequirePrivilege() error {
	return ErrPrivilege{Message: "unsupported OS: " + runtime.GOOS}
}

func (nativeCollector) Scan(force bool) ([]RawDisk, error) {
	return nil, ErrPrivilege{Message: "unsupported OS: " + runtime.GOOS}
}

func (nativeCollector) Read(index int, force bool, previous *RawDisk) (RawDisk, error) {
	return RawDisk{}, ErrPrivilege{Message: "unsupported OS: " + runtime.GOOS}
}
