import { CDI_LANGUAGES } from "./lang.generated.js";

let current = "Simplified Chinese";

export function setLanguage(name) {
  if (CDI_LANGUAGES[name]) current = name;
}

export function smartName(section, id, fallback = "厂商特定") {
  const key = typeof id === "number"
    ? id.toString(16).toUpperCase().padStart(2, "0")
    : String(id).toUpperCase().padStart(2, "0");
  return get(section, key) || get("Smart", key) || fallback;
}

export function get(section, key, fallback = "") {
  return CDI_LANGUAGES[current]?.[section]?.[key] || CDI_LANGUAGES.English?.[section]?.[key] || fallback;
}
