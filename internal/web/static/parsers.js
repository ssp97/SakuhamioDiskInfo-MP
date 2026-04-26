import { parseATA } from "./parsers-ata.js";
import { parseNVMe } from "./parsers-nvme.js";

export function parseDisk(rawDisk) {
  if (rawDisk.basic?.protocol === "NVMe") return parseNVMe(rawDisk);
  return parseATA(rawDisk);
}

export function parseDisks(rawDisks) {
  return (rawDisks || []).map(parseDisk);
}
