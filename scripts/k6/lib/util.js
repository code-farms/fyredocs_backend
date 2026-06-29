// Small stateless helpers (no k6 imports so it's usable in init + VU context).

// Weighted pick from [{weight, ...}]. Returns the chosen object.
export function weightedPick(items) {
  const total = items.reduce((s, it) => s + (it.weight || 1), 0);
  let r = Math.random() * total;
  for (const it of items) {
    r -= it.weight || 1;
    if (r <= 0) return it;
  }
  return items[items.length - 1];
}

export function randItem(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

// Unique-ish id without external deps (Date.now is allowed in k6 VU context).
export function uid(prefix) {
  return `${prefix || 'lt'}_${Date.now()}_${Math.floor(Math.random() * 1e6)}`;
}
