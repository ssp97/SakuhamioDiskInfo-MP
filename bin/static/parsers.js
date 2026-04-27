import { parseATA } from "./parsers-ata.js";
import { parseNVMe } from "./parsers-nvme.js";

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
