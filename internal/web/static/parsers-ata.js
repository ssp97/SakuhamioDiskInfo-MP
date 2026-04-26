import { b64, rawHex, u16, u48 } from "./binary.js";
import { smartName } from "./i18n.js";

// Device Statistics (GP Log 0x04) page definitions.
// Each page N occupies bytes [N*512 : (N+1)*512] in the raw blob.
// Within a page, entries start at offset 8 and are 8 bytes each.
// Entry i (1-based) is at offset i*8 within the page.
// Byte 7 of each entry: bit7=Supported, bit6=Valid.
// size < 0 means the single byte is a signed integer (used for temperatures).
const DEVSTAT_PAGES = [
  { id: 0x01, entries: [
    { name: "Lifetime Power-On Resets", size: 4 },
    { name: "Power-on Hours", size: 4 },
    { name: "Logical Sectors Written", size: 6 },
    { name: "Number of Write Commands", size: 6 },
    { name: "Logical Sectors Read", size: 6 },
    { name: "Number of Read Commands", size: 6 },
    { name: "Date and Time TimeStamp", size: 6 },
    { name: "Pending Error Count", size: 4 },
    { name: "Workload Utilization", size: 2 },
    { name: "Utilization Usage Rate", size: 6 },
    { name: "Resource Availability", size: 7 },
    { name: "Random Write Resources Used", size: 1 },
  ]},
  { id: 0x02, entries: [
    { name: "Number of Free-Fall Events Detected", size: 4 },
    { name: "Overlimit Shock Events", size: 4 },
  ]},
  { id: 0x03, entries: [
    { name: "Spindle Motor Power-on Hours", size: 4 },
    { name: "Head Flying Hours", size: 4 },
    { name: "Head Load Events", size: 4 },
    { name: "Number of Reallocated Logical Sectors", size: 4 },
    { name: "Read Recovery Attempts", size: 4 },
    { name: "Number of Mechanical Start Failures", size: 4 },
    { name: "Number of Realloc. Candidate Logical Sectors", size: 4 },
    { name: "Number of High Priority Unload Events", size: 4 },
  ]},
  { id: 0x04, entries: [
    { name: "Number of Reported Uncorrectable Errors", size: 4 },
    { name: "Resets Between Cmd Acceptance and Completion", size: 4 },
    { name: "Physical Element Status Changed", size: 4 },
  ]},
  { id: 0x05, entries: [
    { name: "Current Temperature", size: -1 },
    { name: "Average Short Term Temperature", size: -1 },
    { name: "Average Long Term Temperature", size: -1 },
    { name: "Highest Temperature", size: -1 },
    { name: "Lowest Temperature", size: -1 },
    { name: "Highest Average Short Term Temperature", size: -1 },
    { name: "Lowest Average Short Term Temperature", size: -1 },
    { name: "Highest Average Long Term Temperature", size: -1 },
    { name: "Lowest Average Long Term Temperature", size: -1 },
    { name: "Time in Over-Temperature", size: 4 },
    { name: "Specified Maximum Operating Temperature", size: -1 },
    { name: "Time in Under-Temperature", size: 4 },
    { name: "Specified Minimum Operating Temperature", size: -1 },
  ]},
  { id: 0x06, entries: [
    { name: "Number of Hardware Resets", size: 4 },
    { name: "Number of ASR Events", size: 4 },
    { name: "Number of Interface CRC Errors", size: 4 },
  ]},
  { id: 0x07, entries: [
    { name: "Percentage Used Endurance Indicator", size: 1 },
  ]},
];

export function parseATA(rawDisk) {
  const data = b64(rawDisk.raw?.smartReadData);
  const thresholds = b64(rawDisk.raw?.smartReadThreshold);
  const disk = baseDisk(rawDisk);
  parseIdentifyDevice(rawDisk, disk);

  if (data.length < 512) return disk;

  const thresholdById = new Map();
  if (thresholds.length >= 512) {
    for (let i = 0; i < 30; i++) {
      const base = 2 + i * 12;
      if (thresholds[base]) thresholdById.set(thresholds[base], thresholds[base + 1]);
    }
  }

  let health = "good";
  let reason = "SMART 属性正常";
  disk.attributes = [];

  for (let i = 0; i < 30; i++) {
    const base = 2 + i * 12;
    const id = data[base];
    if (!id) continue;
    const rawFull = u48(data, base + 5);
    const rawValue = ataDisplayRaw(rawDisk, id, data, base + 5, rawFull);
    const attr = {
      id: id.toString(16).padStart(2, "0").toUpperCase(),
      name: smartName(rawDisk.deviceType?.smartKeyName || "Smart", id),
      current: data[base + 3],
      worst: data[base + 4],
      threshold: thresholdById.get(id) || 0,
      raw: rawHex(data, base + 5, 6),
      rawValue,
      rawFull,
      status: "good",
    };

    if (id === 0x09) disk.powerOnHours = rawValue;
    if (id === 0x0C) disk.powerOnCount = rawValue;
    if ((id === 0xBE || id === 0xC2) && data[base + 5] > 0 && data[base + 5] < 100) disk.temperatureC = data[base + 5];
    if (id === 0xE7 && attr.current > 0 && attr.current <= 100) disk.lifePercent = attr.current;
    if (id === 0xCA && rawValue <= 100) disk.lifePercent = 100 - rawValue;
    if (id === 0xF1 || id === 0xF5) disk.hostWritesGB = Math.floor(rawValue * 512 / 1000 / 1000 / 1000);
    if (id === 0xF2 || id === 0xF6) disk.hostReadsGB = Math.floor(rawValue * 512 / 1000 / 1000 / 1000);

    if (attr.threshold > 0 && attr.current > 0 && attr.current < attr.threshold) {
      attr.status = "bad";
      health = "bad";
      reason = `${attr.name} 低于阈值`;
    }
    if ((id === 0x05 || id === 0xC5 || id === 0xC6) && rawValue > 0 && health !== "bad") {
      attr.status = "caution";
      health = "caution";
      reason = `${attr.name} 原始值非零`;
    }
    disk.attributes.push(attr);
  }

  disk.health = health;
  disk.healthReason = rawDisk.smartState === "asleep" ? (rawDisk.smartMessage || "设备已休眠") : reason;

  parseDeviceStatistics(rawDisk, disk);

  return disk;
}

function parseDeviceStatistics(rawDisk, disk) {
  const raw = b64(rawDisk.raw?.deviceStatistics);
  if (raw.length < 512 * 2) return;

  for (const page of DEVSTAT_PAGES) {
    const pageBase = page.id * 512;
    if (pageBase + 512 > raw.length) continue;
    // Validate page number in header byte 2
    if (raw[pageBase + 2] !== page.id) continue;

    for (let i = 0; i < page.entries.length; i++) {
      const entryIndex = i + 1; // entry 0 is the page header; data entries start at 1
      const entryBase = pageBase + entryIndex * 8;
      if (entryBase + 7 >= raw.length) break;

      const flags = raw[entryBase + 7];
      if (!(flags & 0x80)) continue; // not supported

      const entry = page.entries[i];
      const id = page.id.toString(16).toUpperCase() + entryIndex.toString(16).padStart(2, "0").toUpperCase();

      let rawValue = null;
      if (flags & 0x40) { // valid
        if (entry.size < 0) {
          // signed byte (temperatures)
          const b = raw[entryBase];
          rawValue = b >= 128 ? b - 256 : b;
        } else {
          let val = 0;
          let mul = 1;
          for (let j = 0; j < entry.size; j++) {
            val += raw[entryBase + j] * mul;
            mul *= 256;
          }
          rawValue = val;
        }
      }

      disk.attributes.push({
        id,
        name: entry.name,
        noStats: true,
        current: null,
        worst: null,
        threshold: null,
        raw: null,
        rawValue: rawValue !== null ? rawValue : null,
        rawFull: rawValue,
        status: "good",
      });

      if (rawValue !== null) {
        if (id === "103") disk.hostWritesGB = Math.floor(rawValue * 512 / 1024 / 1024 / 1024);
        if (id === "105") disk.hostReadsGB = Math.floor(rawValue * 512 / 1024 / 1024 / 1024);
      }
    }
  }
}

function ataDisplayRaw(rawDisk, id, data, offset, rawFull) {
  const model = (rawDisk.basic?.model || "").toUpperCase();
  const seagate = model.startsWith("ST") || model.includes("SEAGATE");
  if (seagate) {
    if (id === 0x09 || id === 0xF0) return data[offset] | (data[offset + 1] << 8);
    if (id === 0xBE || id === 0xC2) return data[offset];
  }
  return rawFull;
}

function baseDisk(rawDisk) {
  const basic = rawDisk.basic || {};
  const support = rawDisk.support || {};
  const features = [];
  if (support.smart) features.push("S.M.A.R.T.");
  if (support.trim) features.push("TRIM");
  if (support.ncq) features.push("NCQ");
  if (support.volatileWriteCache) features.push("VolatileWriteCache");
  return {
    raw: rawDisk,
    id: rawDisk.id,
    index: rawDisk.index,
    model: basic.model,
    serial: basic.serial,
    firmware: basic.firmware,
    protocol: basic.protocol,
    transferMode: "",
    standard: "",
    rotationRate: "",
    bufferSize: "",
    nvCacheSize: "",
    capacityBytes: rawDisk.capacityBytes,
    driveLetters: rawDisk.driveLetters || [],
    features,
    powerOnHours: -1,
    powerOnCount: 0,
    hostReadsGB: 0,
    hostWritesGB: 0,
    lifePercent: -1,
    temperatureC: 0,
    health: rawDisk.smartState === "ok" ? "good" : "unknown",
    healthReason: rawDisk.smartMessage || rawDisk.lastUpdateError || "",
    attributes: [],
  };
}

// Parse IDENTIFY DEVICE response for display fields:
// transferMode, standard, rotationRate, bufferSize, nvCacheSize.
function parseIdentifyDevice(rawDisk, disk) {
  const buf = b64(rawDisk.raw?.identifyDevice);
  if (buf.length < 512) return;

  const w = (idx) => u16(buf, idx * 2);
  const w76 = w(76), w77 = w(77), w88 = w(88), w222 = w(222);

  // Transfer mode
  if (w222 !== 0 && w222 !== 0xffff && (w222 >> 12) === 0x1) {
    // SATA transport
    const allspeeds = (w76 & 0x0001) === 0 ? (w76 & 0x00fe) : 0;
    const maxspeed = idFindMSB(allspeeds);
    const curspeed = (w77 & 0x0001) === 0 ? (w77 >> 1) & 0x7 : 0;
    disk.transferMode = `${idSATASpeed(maxspeed)} | ${idSATASpeed(curspeed)}`;
  } else if (w88 !== 0 && w88 !== 0xffff) {
    const supported = w88 & 0x7f;
    const active = (w88 >> 8) & 0x7f;
    if (supported !== 0) {
      const maxMode = idFindMSB(supported);
      disk.transferMode = active !== 0
        ? `${idUDMASpeed(maxMode)} | ${idUDMASpeed(idFindMSB(active))}`
        : `${idUDMASpeed(maxMode)} | ----`;
    } else {
      disk.transferMode = "---- | ----";
    }
  } else {
    disk.transferMode = "---- | ----";
  }

  // ATA / SATA standard
  const major = w(80), minor = w(81);
  let ataVer = "";
  if (major !== 0 && major !== 0xffff) {
    const minStr = idATAMinorVersion(minor);
    const majStr = idATAMajorVersion(major);
    if (minStr) {
      ataVer = (majStr && !minStr.startsWith(majStr)) ? majStr + ", " + minStr : minStr;
    } else {
      ataVer = majStr || `Unknown (0x${major.toString(16).padStart(4, "0").toUpperCase()})`;
    }
  }
  if (w222 !== 0 && w222 !== 0xffff && (w222 >> 12) === 0x1) {
    const sataVer = idSATAVersion(w222);
    disk.standard = (sataVer && ataVer) ? ataVer + "; " + sataVer : (sataVer || ataVer);
  } else {
    disk.standard = ataVer;
  }

  // Rotation rate (word 217)
  const rotation = w(217);
  if (rotation === 1) disk.rotationRate = "SSD";
  else if (rotation > 1 && rotation !== 0xffff) disk.rotationRate = `${rotation} RPM`;
  else disk.rotationRate = "----";

  // Buffer size (word 21, 512-byte units)
  const bufWords = w(21);
  if (bufWords > 0 && bufWords !== 0xffff) disk.bufferSize = idFormatBytes(bufWords * 512);

  // NV cache size (word 214 bit 0 = enabled, size in words 215-216 × 512 bytes)
  if (w(214) & 1) {
    const nvBlocks = w(215) | (w(216) << 16);
    if (nvBlocks > 0) disk.nvCacheSize = idFormatBytes(nvBlocks * 512);
  }
}

function idFindMSB(n) {
  for (let bit = 15; bit >= 0; bit--) if (n & (1 << bit)) return bit;
  return -1;
}

function idSATASpeed(speed) {
  return { 1: "SATA/150", 2: "SATA/300", 3: "SATA/600" }[speed] ?? "----";
}

function idUDMASpeed(mode) {
  return [16, 25, 33, 44, 66, 100, 133][mode] !== undefined
    ? `Ultra DMA/${[16, 25, 33, 44, 66, 100, 133][mode]}`
    : "Ultra DMA/???";
}

function idATAMajorVersion(major) {
  const names = {
    15: "ACS >6 (15)", 14: "ACS >6 (14)", 13: "ACS-6", 12: "ACS-5", 11: "ACS-4",
    10: "ACS-3", 9: "ACS-2", 8: "ATA8-ACS", 7: "ATA/ATAPI-7", 6: "ATA/ATAPI-6",
    5: "ATA/ATAPI-5", 4: "ATA/ATAPI-4", 3: "ATA-3", 2: "ATA-2", 1: "ATA-1",
  };
  return names[idFindMSB(major)] || "";
}

function idATAMinorVersion(minor) {
  const map = {
    0x0001: "ATA-1 X3T9.2/781D prior to revision 4",
    0x0002: "ATA-1 published, ANSI X3.221-1994",
    0x0003: "ATA-1 X3T9.2/781D revision 4",
    0x0004: "ATA-2 published, ANSI X3.279-1996",
    0x0005: "ATA-2 X3T10/948D prior to revision 2k",
    0x0006: "ATA-3 X3T10/2008D revision 1",
    0x0007: "ATA-2 X3T10/948D revision 2k",
    0x0008: "ATA-3 X3T10/2008D revision 0",
    0x0009: "ATA-2 X3T10/948D revision 3",
    0x000a: "ATA-3 published, ANSI X3.298-1997",
    0x000b: "ATA-3 X3T10/2008D revision 6",
    0x000c: "ATA-3 X3T13/2008D revision 7 and 7a",
    0x000d: "ATA/ATAPI-4 X3T13/1153D revision 6",
    0x000e: "ATA/ATAPI-4 T13/1153D revision 13",
    0x000f: "ATA/ATAPI-4 X3T13/1153D revision 7",
    0x0010: "ATA/ATAPI-4 T13/1153D revision 18",
    0x0011: "ATA/ATAPI-4 T13/1153D revision 15",
    0x0012: "ATA/ATAPI-4 published, ANSI NCITS 317-1998",
    0x0013: "ATA/ATAPI-5 T13/1321D revision 3",
    0x0014: "ATA/ATAPI-4 T13/1153D revision 14",
    0x0015: "ATA/ATAPI-5 T13/1321D revision 1",
    0x0016: "ATA/ATAPI-5 published, ANSI NCITS 340-2000",
    0x0017: "ATA/ATAPI-4 T13/1153D revision 17",
    0x0018: "ATA/ATAPI-6 T13/1410D revision 0",
    0x0019: "ATA/ATAPI-6 T13/1410D revision 3a",
    0x001a: "ATA/ATAPI-7 T13/1532D revision 1",
    0x001b: "ATA/ATAPI-6 T13/1410D revision 2",
    0x001c: "ATA/ATAPI-6 T13/1410D revision 1",
    0x001d: "ATA/ATAPI-7 published, ANSI INCITS 397-2005",
    0x001e: "ATA/ATAPI-7 T13/1532D revision 0",
    0x001f: "ACS-3 T13/2161-D revision 3b",
    0x0021: "ATA/ATAPI-7 T13/1532D revision 4a",
    0x0022: "ATA/ATAPI-6 published, ANSI INCITS 361-2002",
    0x0025: "ACS-6 T13/BSR INCITS 574 revision 7",
    0x0027: "ATA8-ACS T13/1699-D revision 3c",
    0x0028: "ATA8-ACS T13/1699-D revision 6",
    0x0029: "ATA8-ACS T13/1699-D revision 4",
    0x0030: "ACS-5 T13/BSR INCITS 558 revision 10",
    0x0031: "ACS-2 T13/2015-D revision 2",
    0x0033: "ATA8-ACS T13/1699-D revision 3e",
    0x0039: "ATA8-ACS T13/1699-D revision 4c",
    0x0042: "ATA8-ACS T13/1699-D revision 3f",
    0x0052: "ATA8-ACS T13/1699-D revision 3b",
    0x005e: "ACS-4 T13/BSR INCITS 529 revision 5",
    0x006d: "ACS-3 T13/2161-D revision 5",
    0x0070: "ACS-6 T13/BSR INCITS 574 revision 11",
    0x0073: "ACS-6 T13/BSR INCITS 574 revision 2",
    0x0082: "ACS-2 published, ANSI INCITS 482-2012",
    0x009c: "ACS-4 published, ANSI INCITS 529-2018",
    0x0107: "ATA8-ACS T13/1699-D revision 2d",
    0x010a: "ACS-3 published, ANSI INCITS 522-2014",
    0x0110: "ACS-2 T13/2015-D revision 3",
    0x011b: "ACS-3 T13/2161-D revision 4",
  };
  return map[minor] || "";
}

function idSATAVersion(word222) {
  const names = {
    11: "SATA >3.5 (11)", 10: "SATA 3.5", 9: "SATA 3.4", 8: "SATA 3.3",
    7: "SATA 3.2", 6: "SATA 3.1", 5: "SATA 3.0", 4: "SATA 2.6",
    3: "SATA 2.5", 2: "SATA II Ext", 1: "SATA 1.0a", 0: "ATA8-AST",
  };
  return names[idFindMSB(word222 & 0x0fff)] || "";
}

function idFormatBytes(bytes) {
  if (bytes >= 1000 * 1000) return `${Math.floor(bytes / 1000 / 1000)} MB`;
  if (bytes >= 1000) return `${Math.floor(bytes / 1000)} KB`;
  return `${bytes} B`;
}
