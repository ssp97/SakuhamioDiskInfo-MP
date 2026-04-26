let apiBase = "";

export function setApiBase(value) {
  apiBase = value;
}

function api(path) {
  return `${apiBase}${path}`;
}

export async function getDisks() {
  const res = await fetch(api("/api/disks"));
  return await res.json();
}

export async function refreshAll(force = false) {
  const res = await fetch(api(`/api/refresh?force=${force ? "true" : "false"}`), { method: "POST" });
  return await res.json();
}

export async function refreshDisk(id, force = false) {
  const res = await fetch(api(`/api/refresh?id=${encodeURIComponent(id)}&force=${force ? "true" : "false"}`), { method: "POST" });
  return await res.json();
}
