import { createServer } from "node:http";

const keys = [
  {
    id: "demo-default",
    masked_key: "sk-demo-default-********00A1",
    upstream: "https://api.example.com",
    first_seen_at: "2026-10-29T08:00:00Z",
    last_seen_at: "2026-11-01T08:30:00Z",
    sample_rate: 100,
  },
  {
    id: "demo-sampled",
    masked_key: "sk-demo-sampled-********00B2",
    upstream: "https://api.example.com",
    first_seen_at: "2026-10-28T11:20:00Z",
    last_seen_at: "2026-11-01T07:10:00Z",
    sample_rate: 45,
  },
  {
    id: "demo-exempt",
    masked_key: "sk-demo-exempt-********00C3",
    upstream: "https://relay.example.net",
    first_seen_at: "2026-10-27T16:40:00Z",
    last_seen_at: "2026-10-31T19:05:00Z",
    sample_rate: 0,
  },
];

function send(response, status, value) {
  response.writeHead(status, {
    "Content-Type": "application/json",
    "Cache-Control": "no-store",
  });
  response.end(JSON.stringify(value));
}

async function readJSON(request) {
  let body = "";
  for await (const chunk of request) body += chunk;
  return JSON.parse(body || "{}");
}

const server = createServer(async (request, response) => {
  const url = new URL(request.url, "http://127.0.0.1");
  if (url.pathname === "/admin/session" && request.method === "GET")
    return send(response, 200, { authenticated: true });
  if (url.pathname === "/admin/policy" && request.method === "GET")
    return send(response, 200, {
      enabled: true,
      review_api_enabled: false,
      scenes: [],
      questions: [],
      trusted_key_idle_days: 30,
      preview_chars: null,
      retention_days: null,
    });
  if (url.pathname === "/admin/trusted-keys" && request.method === "GET") {
    const offset = Math.max(0, Number(url.searchParams.get("offset") || 0));
    const limit = Math.min(
      100,
      Math.max(1, Number(url.searchParams.get("limit") || 50)),
    );
    return send(response, 200, {
      items: keys.slice(offset, offset + limit),
      total: keys.length,
    });
  }
  if (
    url.pathname.startsWith("/admin/key-sampling/") &&
    request.method === "PATCH"
  ) {
    let input;
    try {
      input = await readJSON(request);
    } catch {
      return send(response, 400, { error: "invalid JSON" });
    }
    const id = decodeURIComponent(
      url.pathname.slice("/admin/key-sampling/".length),
    );
    const key = keys.find((item) => item.id === id);
    if (!key) return send(response, 404, { error: "not found" });
    if (
      !Number.isInteger(input.sample_rate) ||
      input.sample_rate < 0 ||
      input.sample_rate > 100
    )
      return send(response, 400, {
        error: "sample_rate must be an integer from 0 to 100",
      });
    key.sample_rate = input.sample_rate;
    return send(response, 200, { sample_rate: key.sample_rate });
  }
  return send(response, 404, { error: "preview endpoint not found" });
});

const port = Number(process.env.PREVIEW_API_PORT || 8080);
server.listen(port, "127.0.0.1", () => {
  process.stdout.write(`Sael preview API ready on 127.0.0.1:${port}\n`);
});
