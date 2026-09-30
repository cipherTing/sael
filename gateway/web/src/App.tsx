import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import {
  Link,
  NavLink,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useSearchParams,
} from "react-router";
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import {
  Activity,
  ChevronRight,
  LayoutDashboard,
  ListFilter,
  LogOut,
  Menu,
  Settings2,
  Shield,
  Workflow,
} from "lucide-react";
import { request, APIError, AUTH_EXPIRED_EVENT } from "./api";
import type { Policy, PolicyPatch, PolicyResponse } from "./policy";
import { endpointGroup } from "./analytics";
import type { Event } from "./types";
import type {
  JevConfig,
  JevInput,
  JevRuntime,
  JevTestResult,
} from "./JevSettingsPage";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "./components/ui/sheet";
import { Loading } from "./components/common";
import { Toaster } from "./components/ui/sonner";
import { toast } from "sonner";
import { notifyError, notifyRetry } from "./notifications";

import { PageBoundary } from "./components/PageBoundary";
const DashboardPage = lazy(() => import("./pages/DashboardPage"));
const RecordsPage = lazy(() => import("./pages/RecordsPage"));
const ScenesPage = lazy(() => import("./SettingsPage"));
const SettingsConsole = lazy(() => import("./pages/SettingsConsole"));
const JevSettingsPage = lazy(() => import("./JevSettingsPage"));
const navigation = [
  { path: "/", name: "总览", icon: LayoutDashboard },
  { path: "/scenes", name: "场景", icon: Workflow },
  { path: "/events", name: "记录", icon: ListFilter },
  { path: "/settings", name: "设置", icon: Settings2 },
];

function Login({ onLogin }: { onLogin: () => void }) {
  const [password, setPassword] = useState(""),
    [busy, setBusy] = useState(false);
  const [retryUntil, setRetryUntil] = useState(0),
    [remaining, setRemaining] = useState(0);
  useEffect(() => {
    if (!retryUntil) return;
    const update = () =>
      setRemaining(Math.max(0, Math.ceil((retryUntil - Date.now()) / 1000)));
    update();
    const timer = window.setInterval(update, 1000);
    return () => window.clearInterval(timer);
  }, [retryUntil]);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy || retryUntil > Date.now()) return;
    setBusy(true);
    try {
      await request("/admin/login", {
        method: "POST",
        body: JSON.stringify({ password }),
      });
      onLogin();
    } catch (e) {
      if (e instanceof APIError && e.status === 429) {
        const seconds = e.retryAfterSeconds || 60;
        setRemaining(seconds);
        setRetryUntil(Date.now() + seconds * 1000);
      }
      notifyError(
        e instanceof APIError && e.status === 401 ? new Error("密码不正确") : e,
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="login-page">
      <form className="login-form" onSubmit={(event) => void submit(event)}>
        <div className="brand">
          <span className="brand-mark">
            <Shield size={19} />
          </span>
          Sael
        </div>
        <h1>登录运维平台</h1>
        <label className="field">
          <span>管理员密码</span>
          <Input
            autoFocus
            type="password"
            autoComplete="current-password"
            aria-label="管理员密码"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            required
          />
        </label>
        <Button disabled={busy || remaining > 0} type="submit">
          {remaining > 0 ? `${remaining} 秒后重试` : busy ? "登录中…" : "登录"}
        </Button>
      </form>
    </div>
  );
}
function SceneRoute({
  policy,
  onSave,
  onReload,
}: {
  policy: PolicyResponse;
  onSave: (p: Pick<Policy, "scenes">) => Promise<void>;
  onReload: () => void;
}) {
  const [params] = useSearchParams(),
    id = params.get("sample") || "";
  const sampleQuery = useQuery({
    queryKey: ["event", id],
    queryFn: () => request<Event>(`/admin/events/${encodeURIComponent(id)}`),
    enabled: Boolean(id),
  });
  const fullTextQuery = useQuery({
    queryKey: ["event-text", id],
    queryFn: ({ signal }) =>
      request<{ text: string }>(
        `/admin/events/${encodeURIComponent(id)}/text`,
        { signal },
      ),
    enabled: Boolean(id && sampleQuery.data?.text_available),
    staleTime: Infinity,
  });
  const sample = useMemo(() => {
    const event = sampleQuery.data;
    if (!event) return undefined;
    const metadata = {
      scores: event.scores,
      endpoint: event.endpoint_group || endpointGroup(event.protocol),
      model: event.model,
      record_id: event.id,
      record_time: event.time,
      return_url:
        params.get("return")?.startsWith("/events?") ||
        params.get("return") === "/events"
          ? params.get("return")!
          : `/events?event=${encodeURIComponent(event.id)}`,
    };
    if (event.text_available) {
      if (fullTextQuery.isPending || fullTextQuery.isError) return undefined;
      return {
        text: fullTextQuery.data?.text || "",
        ...metadata,
      };
    }
    if (event.text) return { text: event.text, ...metadata };
    if (event.scores?.length) return { text: "", ...metadata };
    return undefined;
  }, [
    sampleQuery.data,
    fullTextQuery.data,
    fullTextQuery.isError,
    fullTextQuery.isPending,
    params,
  ]);
  useEffect(() => {
    if (sampleQuery.error)
      notifyRetry(sampleQuery.error, () => void sampleQuery.refetch());
  }, [sampleQuery.error, sampleQuery.refetch]);
  useEffect(() => {
    if (fullTextQuery.error)
      notifyRetry(fullTextQuery.error, () => void fullTextQuery.refetch());
  }, [fullTextQuery.error, fullTextQuery.refetch]);
  if (id && sampleQuery.isPending) return <Loading />;
  if (id && (sampleQuery.isError || fullTextQuery.isError))
    return (
      <div className="empty-state">
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            void (sampleQuery.isError
              ? sampleQuery.refetch()
              : fullTextQuery.refetch())
          }
        >
          重新加载
        </Button>
      </div>
    );
  if (id && sampleQuery.data?.text_available && fullTextQuery.isPending)
    return <Loading />;
  if (id && !sample)
    return (
      <div className="empty-state">
        <p>该记录没有可用于试算的全文或分数</p>
        <Button variant="outline" size="sm" asChild>
          <Link to={`/events?event=${encodeURIComponent(id)}`}>返回记录</Link>
        </Button>
      </div>
    );
  return (
    <>
      <ScenesPage
        policy={policy}
        onSave={onSave}
        onReload={onReload}
        storageKey="sael-scene-draft"
        sample={sample}
        initialScene={params.get("scene") || ""}
        initialEndpoint={params.get("endpoint") || ""}
      />
    </>
  );
}
function JevRoute() {
  const client = useQueryClient(),
    settings = useQuery({
      queryKey: ["jev"],
      queryFn: () => request<JevConfig>("/admin/jev"),
    }),
    runtime = useQuery({
      queryKey: ["runtime"],
      queryFn: () => request<JevRuntime>("/admin/runtime"),
    });
  useEffect(() => {
    if (settings.error)
      notifyRetry(settings.error, () => void settings.refetch());
    if (runtime.error) notifyRetry(runtime.error, () => void runtime.refetch());
  }, [settings.error, settings.refetch, runtime.error, runtime.refetch]);
  async function save(input: JevInput) {
    const value = await request<JevConfig>("/admin/jev", {
      method: "PUT",
      body: JSON.stringify(input),
    });
    client.setQueryData(["jev"], value);
    await client.invalidateQueries({ queryKey: ["runtime"] });
    return value;
  }
  async function test(text: string, connection?: JevInput) {
    try {
      return await request<JevTestResult>("/admin/jev/test", {
        method: "POST",
        body: JSON.stringify({
          text,
          connection,
          policy: {
            enabled: false,
            scenes: [],
            preview_chars: null,
            retention_days: null,
          } satisfies Policy,
        }),
      });
    } finally {
      if (!connection)
        await client.invalidateQueries({ queryKey: ["runtime"] });
    }
  }
  if (settings.isError)
    return (
      <div className="empty-state">
        <Button
          variant="outline"
          size="sm"
          onClick={() => void settings.refetch()}
        >
          重新加载
        </Button>
      </div>
    );
  return settings.data ? (
    <>
      <JevSettingsPage
        config={settings.data}
        runtime={runtime.data}
        onSave={save}
        onTest={test}
      />
    </>
  ) : (
    <Loading />
  );
}
function Console() {
  const client = useQueryClient(),
    location = useLocation(),
    navigate = useNavigate(),
    authExpiryHandled = useRef(false),
    [menu, setMenu] = useState(false);
  const session = useQuery({
    queryKey: ["session"],
    queryFn: () => request<{ authenticated: boolean }>("/admin/session"),
    staleTime: Infinity,
    retry: false,
  });
  const authenticated = Boolean(session.data?.authenticated);
  const policy = useQuery({
    queryKey: ["policy"],
    queryFn: () => request<PolicyResponse>("/admin/policy"),
    enabled: authenticated,
    staleTime: Infinity,
  });
  useEffect(() => {
    const onExpired = () => {
      if (!authenticated || authExpiryHandled.current) return;
      authExpiryHandled.current = true;
      client.removeQueries({
        predicate: (query) => query.queryKey[0] !== "session",
      });
      client.setQueryData(["session"], { authenticated: false });
      toast.error("登录已失效，请重新登录", { id: "auth-expired" });
      navigate("/", { replace: true });
    };
    window.addEventListener(AUTH_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(AUTH_EXPIRED_EVENT, onExpired);
  }, [authenticated, client, navigate]);
  useEffect(() => {
    if (authenticated) authExpiryHandled.current = false;
  }, [authenticated]);
  useEffect(() => {
    if (
      session.error &&
      !(session.error instanceof APIError && session.error.status === 401)
    )
      notifyRetry(session.error, () => void session.refetch());
  }, [session.error, session.refetch]);
  useEffect(() => {
    if (policy.error) notifyRetry(policy.error, () => void policy.refetch());
  }, [policy.error, policy.refetch]);
  if (session.isPending) return <Loading />;
  if (!authenticated) {
    if (
      session.error &&
      !(session.error instanceof APIError && session.error.status === 401)
    )
      return (
        <div className="login-page">
          <Button
            variant="outline"
            size="sm"
            onClick={() => void session.refetch()}
          >
            重新连接
          </Button>
        </div>
      );
    return (
      <Login
        onLogin={() =>
          client.setQueryData(["session"], { authenticated: true })
        }
      />
    );
  }
  async function savePolicy(next: PolicyPatch) {
    const value = await request<Policy>("/admin/policy", {
      method: "PATCH",
      body: JSON.stringify(next),
    });
    client.setQueryData(["policy"], {
      ...value,
      questions: policy.data?.questions || [],
    });
    await client.invalidateQueries({ queryKey: ["analytics"] });
  }
  async function logout() {
    try {
      await request("/admin/logout", { method: "POST" });
    } catch (error) {
      if (!(error instanceof APIError && error.status === 401)) {
        notifyError(error);
        return;
      }
    }
    sessionStorage.removeItem("sael-scene-draft");
    client.removeQueries({
      predicate: (query) => query.queryKey[0] !== "session",
    });
    client.setQueryData(["session"], { authenticated: false });
  }
  const title =
    navigation.find((item) => item.path === location.pathname)?.name || "总览";
  const nav = (
    <>
      <div className="brand">
        <span className="brand-mark">
          <Shield size={18} />
        </span>
        Sael
      </div>
      <div className="nav-section">工作空间</div>
      <nav className="side-nav">
        {navigation.map((item) => (
          <NavLink
            className={({ isActive }) => `nav-item ${isActive ? "active" : ""}`}
            key={item.path}
            to={item.path}
            end={item.path === "/"}
            onClick={() => setMenu(false)}
          >
            <item.icon size={17} />
            <span>{item.name}</span>
          </NavLink>
        ))}
      </nav>
      <div className="sidebar-footer">
        <div className="review-state">
          <i className={`state-dot ${policy.data?.enabled ? "on" : ""}`} />
          {policy.data?.enabled ? "审查已开启" : "审查已关闭"}
        </div>
        <Button
          size="sm"
          variant="ghost"
          className="justify-start text-muted-foreground"
          onClick={() => void logout()}
        >
          <LogOut size={14} />
          退出登录
        </Button>
      </div>
    </>
  );
  return (
    <div className="app-shell">
      <aside className="sidebar">{nav}</aside>
      <div className="shell-main">
        <header className="topbar">
          <div className="topbar-location">
            <Button
              className="mobile-nav-trigger"
              size="icon-sm"
              variant="ghost"
              aria-label="打开导航"
              onClick={() => setMenu(true)}
            >
              <Menu size={17} />
            </Button>
            <span>控制台</span>
            <ChevronRight size={12} />
            <strong>{title}</strong>
          </div>
          <div className="topbar-actions">
            <Activity size={14} color="#9aa8c0" />
            <Link className="small muted" to="/settings?tab=access">
              接入配置
            </Link>
          </div>
        </header>
        <main className="content">
          {policy.isError ? (
            <div className="empty-state">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void policy.refetch()}
              >
                重新加载
              </Button>
            </div>
          ) : !policy.data ? (
            <Loading />
          ) : (
            <PageBoundary key={location.pathname}>
              <Suspense fallback={<Loading />}>
                <Routes>
                  <Route
                    path="/"
                    element={<DashboardPage enabled={policy.data.enabled} />}
                  />
                  <Route
                    path="/scenes"
                    element={
                      <SceneRoute
                        policy={policy.data}
                        onSave={(next) =>
                          savePolicy({
                            scenes: next.scenes,
                          })
                        }
                        onReload={() => void policy.refetch()}
                      />
                    }
                  />
                  <Route
                    path="/events"
                    element={<RecordsPage scenes={policy.data.scenes} />}
                  />
                  <Route
                    path="/access"
                    element={<Navigate to="/settings?tab=access" replace />}
                  />
                  <Route
                    path="/settings"
                    element={
                      <SettingsConsole
                        policy={policy.data}
                        onSave={savePolicy}
                        jev={<JevRoute />}
                      />
                    }
                  />
                  <Route path="*" element={<Navigate to="/" replace />} />
                </Routes>
              </Suspense>
            </PageBoundary>
          )}
        </main>
      </div>
      <Sheet open={menu} onOpenChange={setMenu}>
        <SheetContent side="left" className="mobile-sheet w-64 px-3">
          <SheetHeader className="sr-only">
            <SheetTitle>导航</SheetTitle>
            <SheetDescription>Sael 运维页面</SheetDescription>
          </SheetHeader>
          {nav}
        </SheetContent>
      </Sheet>
    </div>
  );
}
export default function App() {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            retry: false,
            staleTime: 15000,
            refetchOnWindowFocus: false,
          },
        },
      }),
  );
  return (
    <QueryClientProvider client={client}>
      <Console />
      <Toaster theme="light" position="bottom-right" richColors />
    </QueryClientProvider>
  );
}
