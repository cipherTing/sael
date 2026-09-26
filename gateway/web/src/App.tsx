import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import {
  Link,
  NavLink,
  Navigate,
  Route,
  Routes,
  useLocation,
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
  Network,
  Settings2,
  Shield,
  Workflow,
} from "lucide-react";
import { request, APIError } from "./api";
import type { Policy, PolicyResponse } from "./policy";
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
import { ErrorState, Loading } from "./components/common";
import { Toaster } from "./components/ui/sonner";
import { toast } from "sonner";
import { TrustedKeySettings } from "./components/TrustedKeySettings";
import { SessionFreezeSettings } from "./components/SessionFreezeSettings";
import { PageBoundary } from "./components/PageBoundary";
const DashboardPage = lazy(() => import("./pages/DashboardPage"));
const RecordsPage = lazy(() => import("./pages/RecordsPage"));
const ScenesPage = lazy(() => import("./SettingsPage"));
const AccessPage = lazy(() => import("./pages/AccessPage"));
const JevSettingsPage = lazy(() => import("./JevSettingsPage"));
const navigation = [
  { path: "/", name: "总览", icon: LayoutDashboard },
  { path: "/scenes", name: "场景", icon: Workflow },
  { path: "/events", name: "记录", icon: ListFilter },
  { path: "/access", name: "接入", icon: Network },
  { path: "/settings", name: "设置", icon: Settings2 },
];

function Login({ onLogin }: { onLogin: () => void }) {
  const [password, setPassword] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
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
    setError("");
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
      setError(
        e instanceof APIError && e.status === 401
          ? "密码不正确"
          : e instanceof Error
            ? e.message
            : String(e),
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
        {error && <ErrorState error={error} />}
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
  onSave: (p: Policy) => Promise<void>;
  onReload: () => void;
}) {
  const [params] = useSearchParams(),
    id = params.get("sample") || "";
  const sampleQuery = useQuery({
    queryKey: ["event", id],
    queryFn: () => request<Event>(`/admin/events/${encodeURIComponent(id)}`),
    enabled: Boolean(id),
  });
  const sample = useMemo(
    () =>
      sampleQuery.data
        ? {
            text: sampleQuery.data.text || sampleQuery.data.text_preview,
            scores: sampleQuery.data.scores,
            endpoint: sampleQuery.data.protocol,
            model: sampleQuery.data.model,
          }
        : undefined,
    [sampleQuery.data],
  );
  return (
    <>
      {sampleQuery.isError && <ErrorState error={sampleQuery.error} />}
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
function JevRoute({
  policy,
  onSavePolicy,
}: {
  policy: PolicyResponse;
  onSavePolicy: (p: Policy) => Promise<void>;
}) {
  const client = useQueryClient(),
    settings = useQuery({
      queryKey: ["jev"],
      queryFn: () => request<JevConfig>("/admin/jev"),
    }),
    runtime = useQuery({
      queryKey: ["runtime"],
      queryFn: () => request<JevRuntime>("/admin/runtime"),
    });
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
    const value = await request<JevTestResult>("/admin/jev/test", {
      method: "POST",
      body: JSON.stringify({ text, connection }),
    });
    return value;
  }
  if (settings.isError)
    return (
      <ErrorState
        error={settings.error}
        retry={() => void settings.refetch()}
      />
    );
  return settings.data ? (
    <>
      <JevSettingsPage
        config={settings.data}
        runtime={runtime.data}
        onSave={save}
        onTest={test}
      />
      <div className="settings-policies-grid">
        <TrustedKeySettings
          policy={policy}
          onSave={async (next) => {
            const { questions, ...value } = next as PolicyResponse;
            await onSavePolicy(value);
          }}
        />
        <SessionFreezeSettings
          policy={policy}
          onSave={async (next) => {
            const { questions, ...value } = next as PolicyResponse;
            await onSavePolicy(value);
          }}
        />
      </div>
    </>
  ) : (
    <Loading />
  );
}
function Console() {
  const client = useQueryClient(),
    location = useLocation(),
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
  if (session.isPending) return <Loading />;
  if (!authenticated) {
    if (
      session.error &&
      !(session.error instanceof APIError && session.error.status === 401)
    )
      return (
        <div className="login-page">
          <ErrorState
            error={session.error}
            retry={() => void session.refetch()}
          />
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
  async function savePolicy(next: Policy) {
    const value = await request<Policy>("/admin/policy", {
      method: "PUT",
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
        toast.error("退出失败，请重试");
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
            <Link className="small muted" to="/access">
              接入配置
            </Link>
          </div>
        </header>
        <main className="content">
          {policy.isError ? (
            <ErrorState
              error={policy.error}
              retry={() => void policy.refetch()}
            />
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
                        onSave={savePolicy}
                        onReload={() => void policy.refetch()}
                      />
                    }
                  />
                  <Route
                    path="/events"
                    element={<RecordsPage scenes={policy.data.scenes} />}
                  />
                  <Route path="/access" element={<AccessPage />} />
                  <Route
                    path="/settings"
                    element={
                      <JevRoute
                        policy={policy.data}
                        onSavePolicy={savePolicy}
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
      <Toaster theme="light" position="bottom-right" richColors />
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
    </QueryClientProvider>
  );
}
