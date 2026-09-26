export type TrafficPoint = {
  time: string;
  endpoint: string;
  model: string;
  outcome: string;
  count: number;
};
export type DistributionPoint = {
  time: string;
  endpoint: string;
  model?: string;
  metric: string;
  upper: number;
  count: number;
};
export type ScenePoint = {
  time: string;
  endpoint: string;
  scene_id: string;
  name: string;
  action: string;
  winner_id: string;
  winner_name: string;
  count: number;
};
export type ErrorPoint = {
  time: string;
  endpoint: string;
  kind: string;
  count: number;
};
export type Analytics = {
  current_rpm?: number;
  since: string;
  until: string;
  step_seconds: number;
  traffic: TrafficPoint[];
  previous: TrafficPoint[];
  distributions: DistributionPoint[];
  scenes: ScenePoint[];
  errors: ErrorPoint[];
  models: string[];
};
export type Summary = {
  total: number;
  checked: number;
  hits: number;
  blocked: number;
  allowed: number;
  clean: number;
  failures: number;
  skipped: number;
  frozen: number;
  hitRate: number | null;
  blockRate: number | null;
  failureRate: number | null;
};
export type Bucket = Summary & {
  time: string;
  start: string;
  end: string;
  label: string;
  endpoints: Record<string, number>;
};
export const endpoints = [
  {
    id: "openai_chat",
    name: "Chat Completions",
    short: "Chat",
    provider: "OpenAI",
    path: "/v1/chat/completions",
    icon: "/brands/openai.svg",
    color: "#516de5",
  },
  {
    id: "openai_responses",
    name: "Responses",
    short: "Responses",
    provider: "OpenAI",
    path: "/v1/responses",
    icon: "/brands/openai.svg",
    color: "#9574d6",
  },
  {
    id: "anthropic",
    name: "Messages",
    short: "Messages",
    provider: "Anthropic",
    path: "/v1/messages",
    icon: "/brands/anthropic.svg",
    color: "#c28a5e",
  },
  {
    id: "openai_images_generations",
    name: "Images Generations",
    short: "Images",
    provider: "OpenAI",
    path: "/v1/images/generations",
    icon: "/brands/openai.svg",
    color: "#4f9d75",
  },
  {
    id: "openai_images_edits",
    name: "Images Edits",
    short: "Image Edits",
    provider: "OpenAI",
    path: "/v1/images/edits",
    icon: "/brands/openai.svg",
    color: "#3f8d9e",
  },
  {
    id: "openai_images_variations",
    name: "Images Variations",
    short: "Image Variations",
    provider: "OpenAI",
    path: "/v1/images/variations",
    icon: "/brands/openai.svg",
    color: "#6f8cba",
  },
];
export const errorNames: Record<string, string> = {
  classifier_timeout: "调用超时",
  classifier_unavailable: "调用失败",
  classifier_invalid_response: "返回结果无效",
  classifier_input_too_long: "输入超限，跳过审查",
  session_blocked: "会话冻结中",
};
export function ratio(part: number, whole: number): number | null {
  return whole ? (part / whole) * 100 : null;
}
export function summarize(points: TrafficPoint[]): Summary {
  const counts: Record<string, number> = {};
  for (const p of points)
    counts[p.outcome] = (counts[p.outcome] || 0) + p.count;
  const clean = counts.clean || 0,
    blocked = counts.blocked || 0,
    allowed = counts.hit_allowed || 0,
    failures = counts.unreviewed || 0;
  const hits = blocked + allowed,
    checked = clean + hits;
  return {
    total: points.reduce((n, p) => n + p.count, 0),
    checked,
    hits,
    blocked,
    allowed,
    clean,
    failures,
    skipped: counts.input_too_long || 0,
    frozen: counts.session_blocked || 0,
    hitRate: ratio(hits, checked),
    blockRate: ratio(blocked, checked),
    failureRate: ratio(failures, checked + failures),
  };
}
export function quantile(
  values: { upper: number; count: number }[],
  q: number,
): number | null {
  const count = values.reduce((n, p) => n + p.count, 0);
  if (!count) return null;
  const target = Math.max(1, Math.ceil(count * q));
  let seen = 0;
  for (const point of [...values].sort((a, b) => a.upper - b.upper)) {
    seen += point.count;
    if (seen >= target) return point.upper;
  }
  return null;
}
export function fullDate(value: string | number): string {
  const d = new Date(value),
    pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}/${pad(d.getMonth() + 1)}/${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
export function groupTraffic(data: Analytics): Bucket[] {
  const start = Date.parse(data.since),
    end = Date.parse(data.until),
    step = data.step_seconds * 1000;
  const grouped = new Map<number, TrafficPoint[]>();
  for (const point of data.traffic) {
    const instant = Date.parse(point.time);
    const group = grouped.get(instant) || [];
    group.push(point);
    grouped.set(instant, group);
  }
  const current = new Date(start);
  if (step === 86400000) current.setHours(0, 0, 0, 0);
  else if (step === 3600000)
    current.setTime(
      start -
        current.getMinutes() * 60000 -
        current.getSeconds() * 1000 -
        current.getMilliseconds(),
    );
  else current.setTime(Math.floor(start / step) * step);
  const buckets: Bucket[] = [];
  while (current.getTime() < end) {
    const instant = current.getTime(),
      points = grouped.get(instant) || [],
      byEndpoint: Record<string, number> = {};
    for (const point of points)
      byEndpoint[point.endpoint] =
        (byEndpoint[point.endpoint] || 0) + point.count;
    const time = current.toISOString();
    if (step === 86400000) current.setDate(current.getDate() + 1);
    else current.setTime(instant + step);
    buckets.push({
      ...summarize(points),
      time,
      start: new Date(Math.max(start, instant)).toISOString(),
      end: new Date(Math.min(end, current.getTime())).toISOString(),
      label: fullDate(time),
      endpoints: byEndpoint,
    });
  }
  return buckets;
}
export function drilldownQuery(
  query: URLSearchParams,
  data: Pick<Analytics, "since" | "until">,
  extra: Record<string, string>,
): URLSearchParams {
  const next = new URLSearchParams();
  for (const key of ["endpoint", "model"])
    if (query.get(key)) next.set(key, query.get(key)!);
  next.set("start", data.since);
  next.set("end", data.until);
  for (const [key, value] of Object.entries(extra))
    if (value) next.set(key, value);
  return next;
}
export function percent(value: number | null) {
  return value === null
    ? "—"
    : `${new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(value)}%`;
}
export function count(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value);
}
export function duration(value: number | null) {
  return value === null
    ? "—"
    : value < 1000
      ? `${count(value)} ms`
      : `${new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(value / 1000)} s`;
}
export function latencySeries(
  data: Analytics,
  buckets: Bucket[],
  metric = "review_ms",
  q = 0.95,
) {
  const groups = new Map<number, DistributionPoint[]>();
  for (const point of data.distributions) {
    if (point.metric !== metric) continue;
    const time = Date.parse(point.time);
    const list = groups.get(time) || [];
    list.push(point);
    groups.set(time, list);
  }
  return buckets.map((bucket) =>
    quantile(groups.get(Date.parse(bucket.time)) || [], q),
  );
}
export function sceneStats(data: Analytics) {
  const groups = new Map<
    string,
    {
      id: string;
      name: string;
      matched: number;
      effective: number;
      blocked: number;
      shadowed: number;
      winners: Record<string, { name: string; count: number }>;
    }
  >();
  for (const p of data.scenes) {
    const g = groups.get(p.scene_id) || {
      id: p.scene_id,
      name: p.name,
      matched: 0,
      effective: 0,
      blocked: 0,
      shadowed: 0,
      winners: {},
    };
    g.matched += p.count;
    if (p.scene_id === p.winner_id) {
      g.effective += p.count;
      if (p.action === "block") g.blocked += p.count;
    } else {
      g.shadowed += p.count;
      const w = g.winners[p.winner_id] || { name: p.winner_name, count: 0 };
      w.count += p.count;
      g.winners[p.winner_id] = w;
    }
    groups.set(p.scene_id, g);
  }
  return [...groups.values()].sort((a, b) => b.effective - a.effective);
}

export function requestsPerMinute(total: number, since: string, until: string) {
  const minutes = (Date.parse(until) - Date.parse(since)) / 60_000;
  return minutes > 0 ? total / minutes : 0;
}
export function rpm(value: number) {
  if (value > 0 && value < 0.01) return "<0.01";
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(
    value,
  );
}
