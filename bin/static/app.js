import { getDisks, refreshAll, refreshDisk, getTemperatureHistory, setApiBase } from "./api.js?v=20260429-pcie-transfer-1";
import { parseDisks } from "./parsers.js?v=20260429-pcie-transfer-1";

let rawDisks = [];
let disks = [];
let themeData = {};
let current = 0;
let diskPage = 0;
let apiBase = "";
let tempChartRange = "24h";
let tempChartLoading = false;
let serialVisible = false;

const disksPerPage = 12;
const themeStorageKey = "theme";
const $ = (id) => document.getElementById(id);

const healthStates = {
  good: { text: "良好", className: "good" },
  good100: { text: "良好", className: "good100" },
  caution: { text: "警告", className: "caution" },
  bad: { text: "不良", className: "bad" },
  unknown: { text: "未知", className: "unknown" },
};

const rootVars = {
  pageBg: "--page-bg",
  surface: "--surface",
  surfaceStrong: "--surface-strong",
  surfaceSoft: "--surface-soft",
  border: "--border",
  text: "--text",
  textMuted: "--text-muted",
  textFaint: "--text-faint",
  primary: "--primary",
  primarySoft: "--primary-soft",
  primaryStrong: "--primary-strong",
  success: "--success",
  successSoft: "--success-soft",
  warning: "--warning",
  warningSoft: "--warning-soft",
  danger: "--danger",
  dangerSoft: "--danger-soft",
  cyan: "--cyan",
  cyanSoft: "--cyan-soft",
  listText1: "--list-text1",
  listText2: "--list-text2",
  listBk1: "--list-bk1",
  listBk2: "--list-bk2",
  listLine1: "--list-line1",
  listLine2: "--list-line2",
  listBkSelected: "--list-bk-selected",
  listTextSelected: "--list-text-selected",
};

const legacyColorVars = {
  LabelText: "--text",
  ButtonText: "--text",
  ListText1: "--list-text1",
  ListText2: "--list-text2",
  ListLine1: "--list-line1",
  ListLine2: "--list-line2",
  ListBk1: "--list-bk1",
  ListBk2: "--list-bk2",
  ListBkSelected: "--list-bk-selected",
  ListTextSelected: "--list-text-selected",
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

function applyRawDisks(nextRaw) {
  rawDisks = nextRaw || [];
  disks = parseDisks(rawDisks);
}

async function showDisk(index, read = true) {
  if (!disks.length) {
    current = 0;
    renderEmptyState();
    return;
  }

  current = (index + disks.length) % disks.length;
  diskPage = Math.floor(current / disksPerPage);
  renderCurrent();
}

function renderEmptyState() {
  $("model").textContent = "未检测到磁盘";
  $("modelName").textContent = "----";
  $("capacity").textContent = "----";
  $("attributes").innerHTML = "";
  $("health").textContent = "未知";
  $("healthReason").textContent = "未检测到可显示的磁盘";
  $("temperature").textContent = "-- °C";
  $("powerHoursMetric").textContent = "--";
  $("powerCountMetric").textContent = "--";
  $("hostReadsMetric").textContent = "--";
  $("hostWritesMetric").textContent = "--";
  $("hostReadsSub").textContent = "--";
  $("hostWritesSub").textContent = "--";
  $("smartSummary").textContent = "0 项";
  setHealthClass("unknown");
  fillFields(null);
  renderTemperatureChart(null);
}

function renderCurrent() {
  const disk = disks[current];
  if (!disk) {
    renderEmptyState();
    return;
  }
  const raw = rawDisks[current] || {};
  const asleep = raw.smartState === "asleep";
  const cached = asleep && hasCachedSmart(raw);
  const health = disk.health === "good" && disk.lifePercent === 100 ? "good100" : disk.health;
  const displayedHealth = asleep && !cached ? "unknown" : health;
  const healthLabel = healthText(disk.health) + (disk.lifePercent >= 0 ? ` (${disk.lifePercent}%)` : "");

  renderDiskButtons();
  $("model").textContent = `${disk.model || "Unknown Disk"} : ${formatCapacity(disk.capacityBytes)}`;
  $("modelName").textContent = disk.model || "Unknown Disk";
  $("capacity").textContent = formatCapacity(disk.capacityBytes);
  $("health").textContent = asleep && !cached ? "未知" : healthLabel;
  $("healthReason").textContent = raw.smartState === "asleep"
    ? (cached ? "设备已休眠，显示缓存的 SMART 信息" : "设备已休眠")
    : (disk.healthReason || "SSD 运行状态良好");
  $("temperature").textContent = disk.temperatureC > 0 && (!asleep || cached) ? `${disk.temperatureC} °C` : "-- °C";
  $("powerHoursMetric").textContent = disk.powerOnHours >= 0 ? compactNumber(disk.powerOnHours) : "--";
  $("powerCountMetric").textContent = disk.powerOnCount ? compactNumber(disk.powerOnCount) : "--";
  $("hostReadsMetric").textContent = disk.hostReadsGB ? formatDataSize(disk.hostReadsGB) : "--";
  $("hostWritesMetric").textContent = disk.hostWritesGB ? formatDataSize(disk.hostWritesGB) : "--";
  $("hostReadsSub").textContent = disk.hostReadsGB ? `${numberWithCommas(disk.hostReadsGB)} GB` : "--";
  $("hostWritesSub").textContent = disk.hostWritesGB ? `${numberWithCommas(disk.hostWritesGB)} GB` : "--";
  setHealthClass(displayedHealth);
  fillFields(disk);
  renderAttributes(disk);
  renderTemperatureChart(disk);
  updateLastUpdate(raw.lastSmartAt);
}

function setHealthClass(health) {
  const card = document.querySelector(".metric-health");
  card.className = `metric-card metric-health health-${healthClass(health)}`;
}

function fillFields(disk) {
  const empty = "----";
  $("firmware").textContent = disk?.firmware || empty;
  renderSerialField(disk?.serial);
  $("protocol").textContent = disk?.protocol || empty;
  $("transfer").textContent = disk?.transferMode || empty;
  $("letters").textContent = disk ? driveLetters(disk) : empty;
  $("standard").textContent = disk?.standard || empty;
  $("features").textContent = disk ? ((disk.features || []).join(", ") || empty) : empty;
}

function renderSerialField(serial) {
  $("serial").textContent = serialVisible ? (serial || "----") : maskSerial(serial);
  $("toggleSerial").textContent = serialVisible ? "隐藏" : "显示";
  $("toggleSerial").setAttribute("aria-pressed", serialVisible ? "true" : "false");
  $("toggleSerial").setAttribute("aria-label", serialVisible ? "隐藏序列号" : "显示序列号");
}

function renderAttributes(disk) {
  const attributes = disk.attributes || [];
  $("smartSummary").textContent = `${attributes.length} 项`;
  $("attributes").innerHTML = attributes.map((attr) => {
    const status = healthClass(attr.status);
    const name = attr.name || "厂商特定";
    if (attr.noStats) {
      return `
        <tr>
          <td><span class="smart-dot ${status}"></span></td>
          <td>${escapeHTML(attr.id)}</td>
          <td colspan="4" title="${escapeHTML(name)}">${escapeHTML(name)}</td>
          <td class="attr-raw ${valueClass(status)}">${escapeHTML(rawText(attr))}</td>
        </tr>
      `;
    }
    return `
      <tr>
        <td><span class="smart-dot ${status}"></span></td>
        <td>${escapeHTML(attr.id)}</td>
        <td title="${escapeHTML(name)}">${escapeHTML(name)}</td>
        <td class="${valueClass(status)}">${num(attr.current)}</td>
        <td>${num(attr.worst)}</td>
        <td>${num(attr.threshold)}</td>
        <td class="attr-raw">${escapeHTML(rawText(attr))}</td>
      </tr>
    `;
  }).join("");
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
    return `<button class="disk-button disk-${healthClass(health)} ${index === current ? "active" : ""}" data-index="${index}"${title} type="button">
      <i class="disk-status-dot" aria-hidden="true"></i>
      <b>${escapeHTML(driveLetters(disk))}</b>
      <span>${escapeHTML(healthText(health))} · ${escapeHTML(temp)}</span>
    </button>`;
  }).join("");
  $("diskButtons").querySelectorAll("button").forEach((button) => {
    button.addEventListener("click", () => showDisk(Number(button.dataset.index), true));
  });
}

async function renderTemperatureChart(disk) {
  const svg = $("temperatureChart");
  const empty = "-- °C";

  if (!disk?.id) {
    svg.innerHTML = `<text class="chart-empty" x="260" y="95" text-anchor="middle">暂无温度数据</text>`;
    $("tempNow").textContent = empty;
    $("tempMin").textContent = empty;
    $("tempMax").textContent = empty;
    return;
  }

  if (tempChartLoading) return;
  tempChartLoading = true;
  svg.innerHTML = `<text class="chart-empty" x="260" y="95" text-anchor="middle">加载温度数据...</text>`;

  try {
    const data = await getTemperatureHistory(disk.id, tempChartRange);
    const records = data.records || [];

    if (!records.length) {
      svg.innerHTML = `<text class="chart-empty" x="260" y="95" text-anchor="middle">暂无温度数据</text>`;
      $("tempNow").textContent = disk.temperatureC > 0 ? `${disk.temperatureC} °C` : empty;
      $("tempMin").textContent = empty;
      $("tempMax").textContent = empty;
      tempChartLoading = false;
      return;
    }

    const values = records.map((r) => r.avgTemp).filter((v) => v > 0);
    if (!values.length) {
      svg.innerHTML = `<text class="chart-empty" x="260" y="95" text-anchor="middle">暂无温度数据</text>`;
      tempChartLoading = false;
      return;
    }

    const allMax = Math.max(...records.map((r) => r.maxTemp));
    const allMin = Math.min(...records.map((r) => r.minTemp));
    const graphMin = Math.max(0, Math.floor((Math.min(...values) - 8) / 5) * 5);
    const graphMax = Math.max(graphMin + 20, Math.ceil((Math.max(...values) + 8) / 5) * 5);
    const left = 42;
    const right = 502;
    const topY = 18;
    const bottomY = 154;
    const width = right - left;
    const height = bottomY - topY;
    const step = records.length > 1 ? width / (records.length - 1) : width;
    const y = (value) => bottomY - ((value - graphMin) / (graphMax - graphMin)) * height;
    const x = (index) => left + index * step;

    const polyline = records.map((p, i) => `${x(i).toFixed(1)},${y(p.avgTemp).toFixed(1)}`).join(" ");
    const area = `${left},${bottomY} ${polyline} ${right},${bottomY}`;

    const gridValues = [graphMax, Math.round((graphMax + graphMin) / 2), graphMin];
    const grids = gridValues.map((value) => {
      const gy = y(value).toFixed(1);
      return `<line class="chart-grid" x1="${left}" x2="${right}" y1="${gy}" y2="${gy}"></line>
        <text class="chart-axis-label" x="12" y="${Number(gy) + 4}">${value}</text>`;
    }).join("");

    const firstTime = formatChartTime(new Date(records[0].recordedAt));
    const midIdx = Math.floor(records.length / 2);
    const midTime = formatChartTime(new Date(records[midIdx].recordedAt));
    const lastTime = formatChartTime(new Date(records[records.length - 1].recordedAt));

    svg.innerHTML = `
      ${grids}
      <text class="chart-axis-label" x="${left}" y="184" text-anchor="middle">${firstTime}</text>
      <text class="chart-axis-label" x="272" y="184" text-anchor="middle">${midTime}</text>
      <text class="chart-axis-label" x="${right}" y="184" text-anchor="middle">${lastTime}</text>
      <polygon class="chart-area" points="${area}"></polygon>
      <polyline class="chart-line" points="${polyline}"></polyline>
    `;

    $("tempNow").textContent = disk.temperatureC > 0 ? `${disk.temperatureC} °C` : empty;
    $("tempMin").textContent = `${allMin} °C`;
    $("tempMax").textContent = `${allMax} °C`;
  } catch (err) {
    console.error("temperature history error:", err);
    svg.innerHTML = `<text class="chart-empty" x="260" y="95" text-anchor="middle">温度数据加载失败</text>`;
  } finally {
    tempChartLoading = false;
  }
}

async function loadDisks() {
  try {
    setBusy(true);
    const data = await getDisks();
    applyRawDisks(data.disks || []);
    if (current >= disks.length) current = 0;
    await showDisk(current, false);
    if (data.error) setHeaderStatus(data.error);
  } catch (err) {
    console.error(err);
    renderEmptyState();
    setHeaderStatus("连接失败");
  } finally {
    setBusy(false);
  }
}

async function refreshAllDisks() {
  try {
    setBusy(true);
    const data = await refreshAll(false);
    applyRawDisks(data.disks || []);
    if (current >= disks.length) current = 0;
    await showDisk(current, false);
    if (data.error) setHeaderStatus(data.error);
  } catch (err) {
    console.error(err);
    setHeaderStatus("刷新失败");
  } finally {
    setBusy(false);
  }
}

async function wakeCurrentDisk() {
  const id = rawDisks[current]?.id;
  if (!id) return;
  try {
    setBusy(true);
    const data = await refreshDisk(id, true);
    applyRawDisks(data.disks || rawDisks);
    const nextIndex = disks.findIndex((disk) => disk.id === id);
    current = nextIndex >= 0 ? nextIndex : 0;
    await showDisk(current, false);
  } catch (err) {
    console.error(err);
    setHeaderStatus("唤醒失败");
  } finally {
    setBusy(false);
  }
}

function findTheme(themeName) {
  return themeData.Themes?.[themeName] || themeData.Themes?.[themeData.Default];
}

function shouldReadCurrentDisk() {
  const raw = rawDisks[current];
  if (!raw?.id) return false;
  if (raw.smartState === "asleep" && !hasCachedSmart(raw)) return false;
  return !hasCachedSmart(raw) || !disks[current]?.attributes?.length;
}

async function loadThemes() {
  try {
    const res = await fetch(apiBase ? `${apiBase}/api/themes` : "/api/themes");
    themeData = await res.json();
  } catch (err) {
    console.error("Failed to load themes:", err);
    themeData = { Default: "Sakuhamio", Themes: {} };
  }

  const names = Object.keys(themeData.Themes || {});
  renderThemeOptions(names.length ? names : ["Sakuhamio"]);
  const themeName = new URL(location.href).searchParams.get("theme") || loadThemeName() || themeData.Default || names[0];
  applyTheme(findTheme(themeName) || fallbackTheme());
}

function renderThemeOptions(names) {
  $("themeOptions").innerHTML = names.map((theme) => `
    <button class="theme-option" type="button" role="option" data-theme="${escapeHTML(theme)}">${escapeHTML(theme)}</button>
  `).join("");
  $("themeOptions").querySelectorAll(".theme-option").forEach((button) => {
    button.addEventListener("click", () => {
      applyTheme(findTheme(button.dataset.theme) || fallbackTheme());
      closeThemePicker();
      closeMenu();
    });
  });
}

function themeImage(themeName) {
  if (!themeName) return {};
  const images = findTheme(themeName)?.images;
  if (!images) return {};
  const next = {};
  for (const key in images) next[key] = themeName + "/" + images[key];
  return next;
}

function findThemeAsset(images, ...keys) {
  for (const key of keys) {
    if (images[key]) return images[key];
  }
  for (const suffix of keys) {
    const matched = Object.keys(images).find((key) => key.endsWith(suffix));
    if (matched) return images[matched];
  }
  return "";
}

function applyTheme(theme) {
  if (!theme) return;
  saveThemeName(theme.name);
  $("themeCurrent").textContent = theme.name || "Sakuhamio";
  $("themeOptions").querySelectorAll(".theme-option").forEach((button) => {
    const active = button.dataset.theme === theme.name;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", active ? "true" : "false");
  });

  const root = document.documentElement.style;
  const colors = theme.webui?.colors || {};
  for (const key in rootVars) {
    if (colors[key]) root.setProperty(rootVars[key], colors[key]);
  }
  for (const key in legacyColorVars) {
    if (theme.colors?.[key]) root.setProperty(legacyColorVars[key], theme.colors[key]);
  }

  const images = Object.assign({},
    themeImage(themeData.Default),
    themeImage(theme.parentTheme2),
    themeImage(theme.parentTheme1),
    themeImage(theme.name),
  );
  const webAssets = theme.webui?.assets || {};
  const mainCharacterImage = webAssets.characterMain || findThemeAsset(images, "CharacterMain");
  const mascotImage = webAssets.avatar || findThemeAsset(images, "CharacterMini", "Mascot", "SDdiskStatusGood", "SDdiskStatusGood100");
  setImage($("characterMain"), mainCharacterImage);
  setImage($("themeMascot"), mascotImage);

  $("headerSubtitle").textContent = theme.name ? `${theme.name} Theme` : "Sakuhamio DiskInfo MP";
}

function fallbackTheme() {
  return {
    name: "Sakuhamio",
  };
}

function setImage(element, path) {
  if (path) {
    element.src = `themes/${path}`;
  } else {
    element.removeAttribute("src");
  }
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
  closeMenu();
}

function popupWindow() {
  const url = new URL(location.href);
  url.searchParams.set("popup", "1");
  window.open(url.toString(), "cdi_mp", "popup=yes,width=1280,height=820,resizable=yes,scrollbars=yes");
  closeMenu();
}

function setBusy(busy) {
  $("refresh").disabled = busy;
  $("wake").disabled = busy;
}

function updateLastUpdate(value) {
  const fallback = new Date();
  const date = value ? new Date(value) : fallback;
  if (Number.isNaN(date.getTime())) {
    $("lastUpdate").textContent = `最近更新：${formatDateTime(fallback)}`;
    return;
  }
  $("lastUpdate").textContent = `最近更新：${formatDateTime(date)}`;
}

function setHeaderStatus(text) {
  $("lastUpdate").textContent = text ? `状态：${text}` : "最近更新：--";
}

function rawText(attr) {
  if (attr.raw && !String(attr.raw).includes(" ")) return attr.raw;
  return String(attr.rawValue ?? "");
}

function formatCapacity(bytes) {
  if (!bytes) return "----";
  const gb = bytes / 1000 / 1000 / 1000;
  return gb >= 1000 ? `${(gb / 1000).toFixed(2)} TB` : `${gb.toFixed(1)} GB`;
}

function formatDataSize(gb) {
  if (!gb) return "--";
  return gb >= 1000 ? `${(gb / 1000).toFixed(2)} TB` : `${numberWithCommas(gb)} GB`;
}

function compactNumber(value) {
  if (!Number.isFinite(Number(value))) return "--";
  return numberWithCommas(value);
}

function numberWithCommas(value) {
  return String(value).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

function driveLetters(disk) {
  return disk.driveLetters?.length ? disk.driveLetters.join(" ") : "----";
}

function maskSerial(serial) {
  if (!serial) return "----";
  return "*".repeat(Math.min(24, serial.length));
}

function toggleSerialVisibility() {
  serialVisible = !serialVisible;
  renderSerialField(disks[current]?.serial);
}

function num(value) {
  return value === 0 ? "0" : (value ? escapeHTML(value) : "");
}

function valueClass(status) {
  return status === "good" ? "value-good" : status === "caution" ? "value-caution" : status === "bad" ? "value-bad" : "";
}

function formatDateTime(date) {
  const pad = (value) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

function formatChartTime(date) {
  if (!date || !(date instanceof Date) || isNaN(date)) return "--:--";
  return `${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
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

function closeMenu() {
  $("commandMenu").classList.remove("open");
}

function toggleThemePicker() {
  const picker = $("themePicker");
  const open = !picker.classList.contains("open");
  picker.classList.toggle("open", open);
  $("themeTrigger").setAttribute("aria-expanded", open ? "true" : "false");
}

function closeThemePicker() {
  $("themePicker").classList.remove("open");
  $("themeTrigger").setAttribute("aria-expanded", "false");
}

function setRangeTab(range) {
  tempChartRange = range;
  document.querySelectorAll(".range-tabs button").forEach((btn) => {
    const isActive = btn.textContent.trim() === rangeLabel(range);
    btn.classList.toggle("active", isActive);
  });
  if (disks[current]) renderTemperatureChart(disks[current]);
}

function rangeLabel(range) {
  const map = { "1h": "1小时", "24h": "24小时", "7d": "7天" };
  return map[range] || "24小时";
}

if (new URL(location.href).searchParams.get("popup") === "1") {
  document.body.classList.add("popup-mode");
}

$("connect").addEventListener("click", connectServer);
$("refresh").addEventListener("click", refreshAllDisks);
$("wake").addEventListener("click", wakeCurrentDisk);
$("popup").addEventListener("click", popupWindow);
$("menuButton").addEventListener("click", () => $("commandMenu").classList.toggle("open"));
$("toggleSerial").addEventListener("click", toggleSerialVisibility);
$("themeTrigger").addEventListener("click", (event) => {
  event.stopPropagation();
  toggleThemePicker();
});
$("themePicker").addEventListener("click", (event) => event.stopPropagation());
$("preDisk").addEventListener("click", () => {
  if (diskPage > 0) showDisk((diskPage - 1) * disksPerPage, true);
});
$("nextDisk").addEventListener("click", () => {
  if (diskPage < Math.ceil(disks.length / disksPerPage) - 1) showDisk((diskPage + 1) * disksPerPage, true);
});
document.addEventListener("click", (event) => {
  closeThemePicker();
  if (!$("commandMenu").contains(event.target) && !$("menuButton").contains(event.target)) closeMenu();
});
document.querySelectorAll(".range-tabs button").forEach((button) => {
  button.addEventListener("click", () => {
    const text = button.textContent.trim();
    const range = text === "1小时" ? "1h" : text === "7天" ? "7d" : "24h";
    setRangeTab(range);
  });
});

{
  const serverParam = new URL(location.href).searchParams.get("server");
  if (serverParam) {
    apiBase = serverParam.trim().replace(/\/$/, "");
    setApiBase(apiBase);
  }
}

loadThemes().then(loadDisks);
