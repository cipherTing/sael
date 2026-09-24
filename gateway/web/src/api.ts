export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init, credentials: 'same-origin',
    headers: { ...(init.body ? { 'Content-Type': 'application/json' } : {}), ...init.headers }
  })
  if (!response.ok) {
    if (response.status === 401) throw new Error('需要重新登录运维平台')
    if (response.status === 409) throw new Error('策略已被其他管理员修改，请重新载入并核对本地改动')
    throw new Error((await response.text()).trim() || `请求失败 (${response.status})`)
  }
  return response.json() as Promise<T>
}
