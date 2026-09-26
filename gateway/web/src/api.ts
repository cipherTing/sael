export class APIError extends Error {
  constructor(
    public status: number,
    message: string,
    public retryAfterSeconds?: number,
  ) {
    super(message);
    this.name = "APIError";
  }
}
export async function request<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: {
      ...(init.body ? { "Content-Type": "application/json" } : {}),
      ...init.headers,
    },
  });
  if (!response.ok) {
    if (response.status === 429) {
      const raw = response.headers.get("Retry-After");
      const seconds =
        raw && /^\d+$/.test(raw)
          ? Number(raw)
          : raw
            ? Math.ceil((Date.parse(raw) - Date.now()) / 1000)
            : 60;
      throw new APIError(
        429,
        "登录尝试过于频繁，请稍后重试",
        Number.isFinite(seconds) ? Math.max(1, seconds) : 60,
      );
    }

    if (response.status === 401)
      throw new APIError(401, "需要重新登录运维平台");
    if (response.status === 409)
      throw new APIError(409, "策略已发生变化，请核对本地改动或载入线上策略");
    throw new APIError(
      response.status,
      (await response.text()).trim() || `请求失败 (${response.status})`,
    );
  }
  return response.json() as Promise<T>;
}
