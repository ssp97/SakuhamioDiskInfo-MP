let disks = [];
let current = 0;
let diskPage = 0;
let apiBase = "";

const disksPerPage = 8;
const $ = (id) => document.getElementById(id);

const nvmeNames = {
  "01": "严重警告标志",
  "02": "综合温度",
  "03": "可用备用空间",
  "04": "可用备用空间阈值",
  "05": "已用寿命百分比",
  "06": "读取单位计数",
  "07": "写入单位计数",
  "08": "主机读取命令计数",
  "09": "主机写入命令计数",
  "0A": "控制器忙状态时间",
  "0B": "启动-关闭循环次数",
  "0C": "通电时间",
  "0D": "不安全关机计数",
  "0E": "介质与数据完整性错误计数",
  "0F": "错误日志项数",
};

function api(path) {
  return `${apiBase}${path}`;
}

function healthText(value) {
  return { good: "良好", caution: "警告", bad: "不良", unknown: "未知" }[value] || "未知";
}

function healthClass(value) {
  return { good: "good", caution: "caution", bad: "bad", unknown: "unknown" }[value] || "unknown";
}

function showDisk(index) {
  if (!disks.length) {
    current = 0;
    $("model").textContent = "未检测到磁盘";
    $("attributes").innerHTML = "";
    $("health").textContent = "未知";
    $("health").className = "health unknown";
    $("temperature").textContent = "-- °C";
    $("life").textContent = "";
    fillFields(null);
    renderDiskButtons();
    return;
  }
  current = (index + disks.length) % disks.length;
  diskPage = Math.floor(current / disksPerPage);
  const disk = disks[current];
  renderDiskButtons();
  $("model").textContent = `${disk.model || "Unknown Disk"} : ${formatGB(disk.capacityBytes)}`;
  $("health").textContent = healthText(disk.health);
  $("health").className = `health ${healthClass(disk.health)}`;
  $("health").title = disk.healthReason || "";
  $("temperature").textContent = disk.temperatureC > 0 ? `${disk.temperatureC} °C` : "-- °C";
  $("life").textContent = disk.lifePercent >= 0 ? `Life ${disk.lifePercent}%` : "";
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
  $("bufferSize").textContent = disk?.bufferSize || empty;
  $("nvCacheSize").textContent = disk?.nvCacheSize || empty;
  $("rotation").textContent = disk?.rotationRate || empty;
  $("powerCount").textContent = disk?.powerOnCount ? `${disk.powerOnCount} 次` : empty;
  $("powerHours").textContent = disk && disk.powerOnHours >= 0 ? `${disk.powerOnHours} 小时` : empty;
  $("hostReads").textContent = disk?.hostReadsGB ? `${disk.hostReadsGB} GB` : empty;
  $("hostWrites").textContent = disk?.hostWritesGB ? `${disk.hostWritesGB} GB` : empty;
}

function renderAttributes(disk) {
  $("attributes").innerHTML = (disk.attributes || []).map((attr) => `
    <tr>
      <td><span class="smart-dot ${healthClass(attr.status)}"></span></td>
      <td>${escapeHTML(attr.id)}</td>
      <td title="${escapeHTML(attrName(disk, attr))}">${escapeHTML(attrName(disk, attr))}</td>
      <td>${num(attr.current)}</td>
      <td>${num(attr.worst)}</td>
      <td>${num(attr.threshold)}</td>
      <td>${escapeHTML(rawText(attr))}</td>
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
    const temp = disk.temperatureC > 0 ? `${disk.temperatureC} °C` : "-- °C";
    return `<button class="disk-button disk-${healthClass(disk.health)} ${index === current ? "active" : ""}" data-index="${index}">
      <span class="disk-dot"></span>
      <b>${healthText(disk.health)}</b>
      <span>${temp}</span>
      <small>${driveLetters(disk)}</small>
    </button>`;
  }).join("");
  $("diskButtons").querySelectorAll("button").forEach((button) => {
    button.addEventListener("click", () => showDisk(Number(button.dataset.index)));
  });
}

async function loadDisks(refresh = false) {
  const res = await fetch(api(refresh ? "/api/refresh" : "/api/disks"), { method: refresh ? "POST" : "GET" });
  const data = await res.json();
  disks = data.disks || [];
  if (current >= disks.length) current = 0;
  showDisk(current);
}

function connectServer() {
  const value = prompt("输入服务器地址，例如 http://192.168.1.10:8080。留空则使用当前服务器。", apiBase || location.origin);
  if (value === null) return;
  const next = value.trim().replace(/\/$/, "");
  apiBase = next && next !== location.origin ? next : "";
  loadDisks(true);
}

function popupWindow() {
  const url = new URL(location.href);
  url.searchParams.set("popup", "1");
  window.open(url.toString(), "cdi_mp", "popup=yes,width=700,height=720,resizable=yes,scrollbars=no");
}

function attrName(disk, attr) {
  if (disk.protocol === "NVMe" && nvmeNames[attr.id]) return nvmeNames[attr.id];
  return attr.name || "厂商特定";
}

function rawText(attr) {
  if (attr.raw && !attr.raw.includes(" ")) return attr.raw;
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
$("refresh").addEventListener("click", () => loadDisks(true));
$("popup").addEventListener("click", popupWindow);
$("preDisk").addEventListener("click", () => {
  if (diskPage > 0) showDisk((diskPage - 1) * disksPerPage);
});
$("nextDisk").addEventListener("click", () => {
  if (diskPage < Math.ceil(disks.length / disksPerPage) - 1) showDisk((diskPage + 1) * disksPerPage);
});

loadDisks();
