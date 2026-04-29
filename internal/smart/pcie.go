package smart

import (
	"fmt"
	"strconv"
	"strings"
)

func formatPCIeTransferMode(speed string, width int) string {
	speed = strings.TrimSpace(speed)
	gen := pcieGenerationFromSpeed(speed)

	var parts []string
	if gen != "" {
		parts = append(parts, gen)
	}
	if width > 0 {
		parts = append(parts, fmt.Sprintf("x%d", width))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func pcieGenerationFromSpeed(speed string) string {
	if speed == "" {
		return ""
	}
	fields := strings.Fields(speed)
	if len(fields) == 0 {
		return ""
	}
	gt, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return strings.TrimSpace(speed)
	}

	switch {
	case gt >= 63.9:
		return "PCIe 6.0"
	case gt >= 31.9:
		return "PCIe 5.0"
	case gt >= 15.9:
		return "PCIe 4.0"
	case gt >= 7.9:
		return "PCIe 3.0"
	case gt >= 4.9:
		return "PCIe 2.0"
	case gt >= 2.4:
		return "PCIe 1.0"
	default:
		return strings.TrimSpace(speed)
	}
}
