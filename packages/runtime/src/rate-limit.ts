/** A bounded sliding window. Route factories share one instance across aliases. */
export function createSlidingWindowLimiter(options: { limit: number; windowMilliseconds: number; maxKeys?: number; now?: () => number }) {
  const entries = new Map<string, number[]>(), maxKeys = options.maxKeys ?? 10000, clock = options.now ?? Date.now;
  let sweptAt = 0;
  return {
    allow(key: string) {
      const now = clock(), cutoff = now - options.windowMilliseconds;
      const live = (entries.get(key) ?? []).filter((time) => time > cutoff);
      if (!live.length) entries.delete(key);
      if (!live.length && entries.size >= maxKeys && now - sweptAt >= 1000) {
        for (const [id, times] of entries) if (times[times.length - 1]! <= cutoff) entries.delete(id);
        sweptAt = now;
      }
      if (!live.length && entries.size >= maxKeys) return { allowed: false, retrySeconds: Math.ceil(options.windowMilliseconds / 1000) };
      if (live.length >= options.limit) {
        entries.set(key, live);
        return { allowed: false, retrySeconds: Math.max(1, Math.ceil((live[0]! + options.windowMilliseconds - now) / 1000)) };
      }
      entries.set(key, [...live, now]);
      return { allowed: true, retrySeconds: 0 };
    },
  };
}
