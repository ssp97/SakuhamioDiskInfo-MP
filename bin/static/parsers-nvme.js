import { b64, u16, u32, u128 } from "./binary.js";
import { smartName } from "./i18n.js";

export function parseNVMe(rawDisk) {
  const basic = rawDisk.basic || {};
  const log = b64(rawDisk.raw?.smartHealthLog);
  const ctrl = b64(rawDisk.raw?.identifyController);
  const disk = {
    raw: rawDisk,
    id: rawDisk.id,
    index: rawDisk.index,
    model: basic.model,
    serial: basic.serial,
    firmware: basic.firmware,
    protocol: "NVMe",
    transferMode: "PCIe | PCIe",
    standard: nvmeStandard(ctrl) || "NVM Express",
    rotationRate: "SSD",
    capacityBytes: rawDisk.capacityBytes,
    driveLetters: rawDisk.driveLetters || [],
    features: ["S.M.A.R.T.", "TRIM", "VolatileWriteCache"],
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
  if (log.length < 512) return disk;
  if (rawDisk.smartState === "asleep") disk.health = "good";

  const critical = log[0];
  const tempK = u16(log, 1);
  const spare = log[3];
  const spareThreshold = log[4];
  const used = log[5];
  disk.temperatureC = tempK > 273 ? tempK - 273 : 0;
  disk.lifePercent = Math.max(0, 100 - used);
  disk.hostReadsGB = Math.floor(u128(log, 32) * 512000 / 1024 / 1024 / 1024);
  disk.hostWritesGB = Math.floor(u128(log, 48) * 512000 / 1024 / 1024 / 1024);
  disk.powerOnCount = u128(log, 112);
  disk.powerOnHours = u128(log, 128);

  const values = [
    [0x01, critical],
    [0x02, tempK],
    [0x03, spare],
    [0x04, spareThreshold],
    [0x05, used],
    [0x06, u128(log, 32)],
    [0x07, u128(log, 48)],
    [0x08, u128(log, 64)],
    [0x09, u128(log, 80)],
    [0x0A, u128(log, 96)],
    [0x0B, disk.powerOnCount],
    [0x0C, disk.powerOnHours],
    [0x0D, u128(log, 144)],
    [0x0E, u128(log, 160)],
    [0x0F, u128(log, 176)],
    [0x10, u32(log, 192)],
    [0x11, u32(log, 196)],
  ];
  for (let i = 0; i < 8; i++) {
    const value = u16(log, 200 + i * 2);
    if (value) values.push([0x12 + i, value]);
  }
  values.push(
    [0x1A, u32(log, 216)],
    [0x1B, u32(log, 220)],
    [0x1C, u32(log, 224)],
    [0x1D, u32(log, 228)],
  );

  disk.attributes = values.map(([id, value]) => ({
    id: id.toString(16).padStart(2, "0").toUpperCase(),
    name: smartName("SmartNVMe", id),
    noStats: true,
    current: 0,
    worst: 0,
    threshold: 0,
    raw: String(value),
    rawValue: value,
    status: "good",
  }));

  if (critical) {
    disk.health = "bad";
    disk.healthReason = "NVMe 严重警告已置位";
    disk.attributes[0].status = "bad";
  } else if (spareThreshold > 0 && spare <= spareThreshold) {
    disk.health = spare < spareThreshold ? "bad" : "caution";
    disk.healthReason = "可用备用空间达到阈值";
    disk.attributes[2].status = disk.health;
  } else if (disk.lifePercent <= 10) {
    disk.health = "caution";
    disk.healthReason = "剩余寿命较低";
    disk.attributes[4].status = "caution";
  }
  return disk;
}

function nvmeStandard(ctrl) {
  if (ctrl.length < 84) return "";
  const ver = u32(ctrl, 80);
  if (!ver) return "NVM Express 1.0/1.1";
  const major = (ver >>> 16) & 0xffff;
  const minor = (ver >>> 8) & 0xff;
  return major ? `NVM Express ${major}.${minor}` : "";
}
