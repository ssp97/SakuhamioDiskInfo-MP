import { getDisks, refreshAll, refreshDisk, setApiBase } from "./api.js";
import { parseDisks } from "./parsers.js";

let rawDisks = [];
let disks = [];
let themeData = {};
let current = 0;
let diskPage = 0;
let apiBase = "";

const disksPerPage = 8;
const themeStorageKey = "theme";
const $ = (id) => document.getElementById(id);
const healthStates = {
  good: { text: "良好", className: "good" },
  good100: { text: "良好", className: "good100" },
  caution: { text: "警告", className: "caution" },
  bad: { text: "不良", className: "bad" },
  unknown: { text: "未知", className: "unknown" },
};
const themeColorVars = {
  LabelText: ["--label-text", "#000"],
  ButtonText: ["--button-text", "#000"],
  ListText1: ["--list-text1", "#000"],
  ListText2: ["--list-text2", "#000"],
  ListLine1: ["--list-line1", "#e0e0e0"],
  ListLine2: ["--list-line2", "#f0f0f0"],
  ListBk1: ["--list-bk1", "#fff"],
  ListBk2: ["--list-bk2", "#f8f8f8"],
};

function healthText(value) {
  return healthStates[value]?.text || healthStates.unknown.text;
}

function healthClass(value) {
  return healthStates[value]?.className || healthStates.unknown.className;
}

function hasCachedSmart(rawDisk) {
  return Boolean(rawDisk?.raw?.smartReadData || rawDisk?.raw?.smartHealthLog);
}

function temperatureClass(disk) {
  if (!disk || disk.temperatureC <= 0) return "unknown";
  const alarmTemperature = disk.rotationRate === "SSD" || disk.protocol === "NVMe" ? 60 : 50;
  return disk.temperatureC >= alarmTemperature ? "bad" : "good";
}

function applyRawDisks(nextRaw) {
  rawDisks = nextRaw || [];
  disks = parseDisks(rawDisks);
}

async function showDisk(index, read = true) {
  if (!disks.length) {
    current = 0;
    $("model").textContent = "未检测到磁盘";
    $("attributes").innerHTML = "";
    $("health").textContent = "未知";
    $("health").className = "health unknown";
    $("temperature").textContent = "-- °C";
    updateThemeStatusImages("unknown", "unknown");
    fillFields(null);
    renderDiskButtons();
    return;
  }

  current = (index + disks.length) % disks.length;
  diskPage = Math.floor(current / disksPerPage);
  renderCurrent();

  if (read && rawDisks[current]?.id) {
    const id = rawDisks[current].id;
    try {
      const data = await refreshDisk(id, false);
      applyRawDisks(data.disks || rawDisks);
      current = Math.max(0, disks.findIndex((disk) => disk.id === id));
      renderCurrent();
    } catch (err) {
      console.error(err);
    }
  }
}

function renderCurrent() {
  const disk = disks[current];
  renderDiskButtons();
  $("model").textContent = `${disk.model || "Unknown Disk"} : ${formatGB(disk.capacityBytes)}`;
  const health = disk.health === "good" && disk.lifePercent === 100 ? "good100" : disk.health;
  $("health").textContent = healthText(disk.health) + (disk.lifePercent >= 0 ? ` (${disk.lifePercent}%)` : "");
  $("health").className = `health ${healthClass(health)}`;
  $("health").title = disk.healthReason || "";
  const temperature = temperatureClass(disk);
  $("temperature").textContent = disk.temperatureC > 0 ? `${disk.temperatureC} °C` : "-- °C";
  $("temperature").className = `temperature ${healthClass(temperature)}`;
  updateThemeStatusImages(health, temperature);
  fillFields(disk);
  renderAttributes(disk);
}

function fillFields(disk) {
  const empty = "----";
  $("firmware").textContent = disk?.firmware || empty;
  $("serial").textContent = maskSerial(disk?.serial);
  $("protocol").textContent = disk?.protocol || empty;
  $("transfer").textContent = disk?.transferMode || empty;
  $("letters").textContent = disk ? driveLetters(disk) : empty;
  $("standard").textContent = disk?.standard || empty;
  $("features").textContent = disk ? ((disk.features || []).join(", ") || empty) : empty;
  $("rotation").textContent = disk?.rotationRate || empty;
  $("powerCount").textContent = disk?.powerOnCount ? `${disk.powerOnCount} 次` : empty;
  $("powerHours").textContent = disk && disk.powerOnHours >= 0 ? `${disk.powerOnHours} 小时` : empty;
  if (disk?.hostReadsGB) {
    $("outputDual1").textContent = `${disk.hostReadsGB} GB`;
    $("textBufferSize").style.display = "none";
    $("textHostReads").style.display = "";
  } else {
    $("outputDual1").textContent = disk?.bufferSize || empty;
    $("textBufferSize").style.display = "";
    $("textHostReads").style.display = "none";
  }
  if (disk?.hostWritesGB) {
    $("outputDual2").textContent = `${disk.hostWritesGB} GB`;
    $("textNVCachesize").style.display = "none";
    $("textHostWrites").style.display = "";
  } else {
    $("outputDual2").textContent = disk?.nvCacheSize || empty;
    $("textNVCachesize").style.display = "";
    $("textHostWrites").style.display = "none";
  }
}

function renderAttributes(disk) {
  $("attributes").innerHTML = (disk.attributes || []).map((attr) => attr.noStats ? `
    <tr>
      <td><span class="smart-dot ${healthClass(attr.status)}"></span></td>
      <td>${escapeHTML(attr.id)}</td>
      <td colspan="4" title="${escapeHTML(attr.name)}">${escapeHTML(attr.name)}</td>
      <td class="attr-raw">${escapeHTML(rawText(attr))}</td>
    </tr>
  ` : `
    <tr>
      <td><span class="smart-dot ${healthClass(attr.status)}"></span></td>
      <td>${escapeHTML(attr.id)}</td>
      <td title="${escapeHTML(attr.name)}">${escapeHTML(attr.name)}</td>
      <td>${num(attr.current)}</td>
      <td>${num(attr.worst)}</td>
      <td>${num(attr.threshold)}</td>
      <td class="attr-raw">${escapeHTML(rawText(attr))}</td>
    </tr>
  `).join("");
}

function renderDiskButtons() {
  if (!disks.length) {
    $("diskButtons").innerHTML = "";
    $("preDisk").style.visibility = "hidden";
    $("nextDisk").style.visibility = "hidden";
    return;
  }

  const pages = Math.ceil(disks.length / disksPerPage);
  const start = diskPage * disksPerPage;
  const end = Math.min(start + disksPerPage, disks.length);
  $("preDisk").style.visibility = diskPage > 0 ? "visible" : "hidden";
  $("nextDisk").style.visibility = diskPage < pages - 1 ? "visible" : "hidden";
  $("diskButtons").innerHTML = disks.slice(start, end).map((disk, offset) => {
    const index = start + offset;
    const raw = rawDisks[index] || {};
    const asleep = raw.smartState === "asleep";
    const asleepCached = asleep && hasCachedSmart(raw);
    const health = asleep && !asleepCached ? "unknown" : disk.health;
    const temp = (asleep && !asleepCached) || disk.temperatureC <= 0 ? "-- °C" : `${disk.temperatureC} °C`;
    const title = asleep ? ` title="${asleepCached ? "设备已休眠，显示缓存的 SMART 信息" : "设备已休眠"}"` : "";
    return `<button class="disk-button disk-${healthClass(health)} ${index === current ? "active" : ""}" data-index="${index}"${title}>
      <span>${asleepCached ? "*" : ""}${healthText(health)}${asleepCached ? "*" : ""}</span>
      <span>${temp}</span>
      <span>${driveLetters(disk)}</span>
    </button>`;
  }).join("");
  $("diskButtons").querySelectorAll("button").forEach((button) => {
    button.addEventListener("click", () => showDisk(Number(button.dataset.index), true));
  });
}

async function loadDisks() {
  const data = await getDisks();
  applyRawDisks(data.disks || []);
  if (current >= disks.length) current = 0;
  await showDisk(current, false);
}

async function refreshAllDisks() {
  const data = await refreshAll(false);
  applyRawDisks(data.disks || []);
  if (current >= disks.length) current = 0;
  await showDisk(current, false);
}

async function wakeCurrentDisk() {
  const id = rawDisks[current]?.id;
  if (!id) return;
  const data = await refreshDisk(id, true);
  applyRawDisks(data.disks || rawDisks);
  current = Math.max(0, disks.findIndex((disk) => disk.id === id));
  await showDisk(current, false);
}

function findTheme(themeName) {
  return themeData.Themes?.[themeName] || themeData.Themes?.[themeData.Default];
}

async function loadThemes() {
  try {
    const res = await fetch(apiBase ? `${apiBase}/api/themes` : "/api/themes");
    themeData = await res.json();
  } catch (err) {
    console.error("Failed to load themes:", err);
    themeData = { Default: "Plain", Themes: {} };
  }
  const select = $("themeSelect");
  select.innerHTML = Object.keys(themeData.Themes || {}).map((theme) => `<option value="${escapeHTML(theme)}">${escapeHTML(theme)}</option>`).join("");
  const themeName = new URL(location.href).searchParams.get("theme") || loadThemeName();
  applyTheme(findTheme(themeName));
}

function themeImage(themeName) {
  if (!themeName) return {};
  const images = findTheme(themeName)?.images;
  if (!images) return {};
  const next = {};
  for (const key in images) next[key] = themeName + "/" + images[key];
  return next;
}

function applyTheme(theme) {
  if (!theme) return;
  saveThemeName(theme.name);
  $("themeSelect").value = theme.name;
  const root = document.documentElement.style;
  const colors = theme.colors || {};
  for (const key in themeColorVars) {
    const [name, fallback] = themeColorVars[key];
    root.setProperty(name, colors[key] || fallback);
  }
  root.setProperty("--list-bk-selected", colors.ListBkSelected || colors.ListBk2 || "#fffde0");
  root.setProperty("--list-text-selected", colors.ListTextSelected || colors.ListText1 || "#000");
  if (colors.Glass) root.setProperty("--glass", hexToRgba(colors.Glass, theme.glassAlpha ?? theme.GlassAlpha ?? 128));
  if (colors.ListBk1) document.body.style.background = colors.ListBk1;

  const win = $("window");
  const images = Object.assign({},
    themeImage(themeData.Default),
    themeImage(theme.parentTheme2),
    themeImage(theme.parentTheme1),
    themeImage(theme.name),
  );
  win.classList.toggle("theme-wide", Boolean(images.ShizukuBackground));
  win.classList.toggle("character-right", theme.position === 1);
  win.classList.toggle("character-left", theme.position !== 1);
  for (const key in images) win.style.setProperty(`--img-${key}`, imageURL(images[key]));
  if (images.ShizukuCopyright) $("copyright").src = "themes/" + images.ShizukuCopyright;
  updateThemeStatusImages(disks[current]?.health || "unknown", temperatureClass(disks[current]));
}

function updateThemeStatusImages(health, temperature = "unknown") {
  const root = $("window").classList;
  root.remove(...Object.keys(healthStates).map((state) => `status-${state}`));
  root.remove(...Object.keys(healthStates).map((state) => `temperature-status-${state}`));
  root.add(`status-${healthClass(health)}`);
  root.add(`temperature-status-${healthClass(temperature)}`);
}

function imageURL(path) {
  return `url("${encodeURI("themes/" + path)}")`;
}

function loadThemeName() {
  try {
    return localStorage.getItem(themeStorageKey);
  } catch {
    return themeData.Default;
  }
}

function saveThemeName(name) {
  try {
    localStorage.setItem(themeStorageKey, name);
  } catch {}
}

function hexToRgba(hex, alpha) {
  if (!hex?.startsWith("#")) return hex;
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  return `rgba(${r}, ${g}, ${b}, ${(alpha / 255).toFixed(2)})`;
}

function connectServer() {
  const value = prompt("输入服务器地址，例如 http://192.168.1.10:8080。留空则使用当前服务器。", apiBase || location.origin);
  if (value === null) return;
  const next = value.trim().replace(/\/$/, "");
  apiBase = next && next !== location.origin ? next : "";
  setApiBase(apiBase);
  const u = new URL(location.href);
  if (apiBase) u.searchParams.set("server", apiBase);
  else u.searchParams.delete("server");
  history.pushState(null, "", u.toString());
  loadThemes().then(loadDisks);
}

function popupWindow() {
  const url = new URL(location.href);
  url.searchParams.set("popup", "1");
  window.open(url.toString(), "cdi_mp", "popup=yes,width=1000,height=720,resizable=yes,scrollbars=no");
}

function rawText(attr) {
  if (attr.raw && !String(attr.raw).includes(" ")) return attr.raw;
  return String(attr.rawValue ?? "");
}

function formatGB(bytes) {
  return bytes ? `${(bytes / 1000 / 1000 / 1000).toFixed(1)} GB` : "---- GB";
}

function driveLetters(disk) {
  return disk.driveLetters?.length ? disk.driveLetters.join(" ") : "----";
}

function maskSerial(serial) {
  if (!serial) return "----";
  return "*".repeat(Math.min(16, serial.length));
}

function num(value) {
  return value === 0 ? "0" : (value ? escapeHTML(value) : "");
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[ch]));
}

if (new URL(location.href).searchParams.get("popup") === "1") {
  document.body.classList.add("popup-mode");
}

$("connect").addEventListener("click", connectServer);
$("refresh").addEventListener("click", refreshAllDisks);
$("wake").addEventListener("click", wakeCurrentDisk);
$("popup").addEventListener("click", popupWindow);
$("preDisk").addEventListener("click", () => {
  if (diskPage > 0) showDisk((diskPage - 1) * disksPerPage, true);
});
$("nextDisk").addEventListener("click", () => {
  if (diskPage < Math.ceil(disks.length / disksPerPage) - 1) showDisk((diskPage + 1) * disksPerPage, true);
});
$("themeSelect").addEventListener("change", () => applyTheme(findTheme($("themeSelect").value)));
{
  const serverParam = new URL(location.href).searchParams.get("server");
  if (serverParam) {
    apiBase = serverParam.trim().replace(/\/$/, "");
    setApiBase(apiBase);
  }
}
loadThemes().then(loadDisks);
