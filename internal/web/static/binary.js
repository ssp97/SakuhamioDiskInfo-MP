export function b64(bytes) {
  if (!bytes) return new Uint8Array();
  const bin = atob(bytes);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function u16(b, offset) {
  return offset + 1 < b.length ? b[offset] | (b[offset + 1] << 8) : 0;
}

export function u32(b, offset) {
  return (u16(b, offset) | (u16(b, offset + 2) << 16)) >>> 0;
}

export function u48(b, offset) {
  let n = 0;
  let mul = 1;
  for (let i = 0; i < 6 && offset + i < b.length; i++) {
    n += b[offset + i] * mul;
    mul *= 256;
  }
  return n;
}

export function u64(b, offset) {
  const lo = BigInt(u32(b, offset));
  const hi = BigInt(u32(b, offset + 4));
  const value = lo | (hi << 32n);
  return value > BigInt(Number.MAX_SAFE_INTEGER) ? Number.MAX_SAFE_INTEGER : Number(value);
}

export function u128(b, offset) {
  let value = 0n;
  for (let i = 15; i >= 0; i--) value = (value << 8n) | BigInt(b[offset + i] || 0);
  return value > BigInt(Number.MAX_SAFE_INTEGER) ? Number.MAX_SAFE_INTEGER : Number(value);
}

export function rawHex(b, offset, len) {
  const parts = [];
  for (let i = len - 1; i >= 0; i--) parts.push((b[offset + i] || 0).toString(16).padStart(2, "0").toUpperCase());
  return parts.join(" ");
}
