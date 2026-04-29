import { parseATA } from "./parsers-ata.js?v=20260429-pcie-transfer-1";
import { parseNVMe } from "./parsers-nvme.js?v=20260429-pcie-transfer-1";

export function parseDisk(rawDisk) {
  const disk = rawDisk.basic?.protocol === "NVMe" ? parseNVMe(rawDisk) : parseATA(rawDisk);
  if (rawDisk.currentTemp != null) {
    disk.temperatureC = rawDisk.currentTemp;
  }
  return disk;
}

export function parseDisks(rawDisks) {
  return (rawDisks || []).map(parseDisk);
}
