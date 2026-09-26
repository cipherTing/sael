import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  useReactTable,
  getCoreRowModel,
  flexRender,
  type ColumnDef,
  type VisibilityState,
} from "@tanstack/react-table";
import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  Columns3,
  FlaskConical,
  RefreshCw,
  Search,
  X,
} from "lucide-react";
import { request } from "../api";
import type { Event } from "../types";
import type { Scene } from "../policy";
import { endpoints, errorNames, fullDate, duration, count } from "../analytics";
import { ScoreBreakdown } from "../components/ScoreBreakdown";
import { questionName } from "../questionMeta";
import {
  TimeRangePicker,
  rangeFromQuery,
  rangeQuery,
  requestRange,
} from "../TimeRangePicker";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "../components/ui/sheet";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuCheckboxItem,
} from "../components/ui/dropdown-menu";
import {
  ActionBadge,
  Choice,
  CopyButton,
  Empty,
  EndpointLabel,
  ErrorState,
  Loading,
  PageHeading,
} from "../components/common";

export default function RecordsPage({ scenes }: { scenes: Scene[] }) {
  const [params, setParams] = useSearchParams(),
    [search, setSearch] = useState(params.get("search") || ""),
    [visibility, setVisibility] = useState<VisibilityState>({
      request_id: false,
      session_id: false,
      user_agent: false,
      reasoning_effort: false,
    });
  const kind = params.get("kind") || "hit";
  const selected = params.get("event") || "",
    offset = Number(params.get("offset") || 0);
  const query = new URLSearchParams(params);
  query.delete("event");
  if (!query.has("kind")) query.set("kind", "hit");
  if (!query.has("minutes") && !query.has("start"))
    query.set("minutes", "1440");
  const result = useQuery({
    queryKey: ["events", query.toString()],
    queryFn: () => request<Event[]>(`/admin/events?${requestRange(query)}`),
  });
  const detail = useQuery({
    queryKey: ["event", selected],
    queryFn: () =>
      request<Event>(`/admin/events/${encodeURIComponent(selected)}`),
    enabled: Boolean(selected),
  });
  function filter(key: string, value: string) {
    const next = new URLSearchParams(params);
    next.delete("offset");
    next.delete("event");
    if (key === "kind") {
      for (const name of ["scene", "action", "error_kind"]) next.delete(name);
    }
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next);
  }
  const columns = useMemo<ColumnDef<Event>[]>(
    () =>
      (
        [
          {
            id: "time",
            header: () => (
              <span>
                时间 <ArrowDown size={10} style={{ display: "inline" }} />
              </span>
            ),
            cell: ({ row }) => (
              <div className="record-time">
                <span>{fullDate(row.original.time).split(" ")[1]}</span>
                <small>{fullDate(row.original.time).split(" ")[0]}</small>
              </div>
            ),
          },
          {
            id: "endpoint",
            header: "端点 / 模型",
            cell: ({ row }) => (
              <div className="record-endpoint">
                <EndpointLabel id={row.original.protocol} />
                <small>{row.original.model || "—"}</small>
              </div>
            ),
          },
          {
            id: "client_ip",
            header: "来源 IP",
            cell: ({ row }) => (
              <span className="tabular">{row.original.client_ip || "—"}</span>
            ),
          },
          {
            id: "session_id",
            header: "会话 ID",
            cell: ({ row }) => (
              <span className="record-id">
                {row.original.session_id || "—"}
              </span>
            ),
          },
          {
            id: "user_agent",
            header: "客户端",
            cell: ({ row }) => (
              <span title={row.original.user_agent} className="truncate-cell">
                {row.original.user_agent || "—"}
              </span>
            ),
          },
          {
            id: "reasoning_effort",
            header: "推理强度",
            cell: ({ row }) => row.original.parameters?.reasoning_effort || "—",
          },
          {
            id: "scene",
            header: kind === "hit" ? "生效场景" : "状态",
            cell: ({ row }) =>
              row.original.kind !== "hit" ? (
                <span style={{ color: "#bf9851" }}>
                  {errorNames[row.original.error_kind || ""] ||
                    (row.original.kind === "warning"
                      ? "网关警告"
                      : "Jev 调用失败")}
                </span>
              ) : (
                row.original.decision.scene_name || "—"
              ),
          },
          {
            id: "risk",
            header: "触发条件",
            cell: ({ row }) => (
              <span>
                {row.original.decision.hits
                  ?.map((h) => questionName(h.question))
                  .join("、") || "—"}
              </span>
            ),
          },
          {
            id: "action",
            header: "处理结果",
            cell: ({ row }) =>
              row.original.kind !== "hit" ? (
                <span className="subtle-badge">
                  {row.original.error_kind === "session_blocked"
                    ? "拦截"
                    : "放行"}
                </span>
              ) : (
                <ActionBadge action={row.original.decision.action} />
              ),
          },
          {
            id: "latency",
            header: "审查耗时",
            cell: ({ row }) => (
              <span className="tabular">
                {row.original.kind === "warning"
                  ? "—"
                  : duration(row.original.classifier_ms)}
              </span>
            ),
          },
          {
            id: "request_id",
            header: "请求 ID",
            cell: ({ row }) => (
              <span className="record-id">{row.original.request_id}</span>
            ),
          },
        ] satisfies ColumnDef<Event>[]
      ).filter((column) => kind === "hit" || column.id !== "risk"),
    [kind],
  );
  const table = useReactTable({
    data: result.data || [],
    columns,
    getCoreRowModel: getCoreRowModel(),
    state: { columnVisibility: visibility },
    onColumnVisibilityChange: setVisibility,
  });
  const current = detail.data;
  return (
    <>
      <PageHeading title="记录">
        <TimeRangePicker
          value={rangeFromQuery(params)}
          onChange={(value) => {
            const next = rangeQuery(params, value);
            next.delete("offset");
            next.delete("event");
            setParams(next);
          }}
        />
        <Button
          variant="outline"
          size="icon-sm"
          aria-label="刷新记录"
          onClick={() => void result.refetch()}
        >
          <RefreshCw size={14} />
        </Button>
      </PageHeading>
      <div className="section-tabs">
        <button
          className={`section-tab ${kind === "hit" ? "active" : ""}`}
          onClick={() => filter("kind", "hit")}
        >
          场景命中
        </button>
        <button
          className={`section-tab ${kind === "failure" ? "active" : ""}`}
          onClick={() => filter("kind", "failure")}
        >
          Jev 错误
        </button>
        <button
          className={`section-tab ${kind === "warning" ? "active" : ""}`}
          onClick={() => filter("kind", "warning")}
        >
          网关警告
        </button>
      </div>
      {(params.get("client_ip") || params.get("session_id")) && (
        <div className="filterbar">
          {["client_ip", "session_id"]
            .filter((k) => params.get(k))
            .map((key) => (
              <button
                key={key}
                className="filter-chip"
                onClick={() => filter(key, "")}
              >
                {key === "client_ip" ? "IP" : "会话"}：{params.get(key)}
                <X size={12} />
              </button>
            ))}
        </div>
      )}
      <div className="panel">
        <div className="records-toolbar">
          <form
            style={{ display: "flex", gap: 6, flex: "1 1 250px" }}
            onSubmit={(event) => {
              event.preventDefault();
              filter("search", search);
            }}
          >
            <Input
              aria-label="搜索记录"
              placeholder="搜索文本、模型、IP 或会话 ID"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <Button
              variant="outline"
              size="icon-sm"
              aria-label="查询记录"
              type="submit"
            >
              <Search size={14} />
            </Button>
          </form>
          <Choice
            label="记录端点"
            value={params.get("endpoint") || ""}
            onChange={(value) => filter("endpoint", value)}
            options={[
              { value: "", label: "全部端点" },
              ...endpoints.map((e) => ({
                value: e.id,
                label: <EndpointLabel id={e.id} compact />,
              })),
            ]}
          />
          {kind === "hit" && (
            <Choice
              label="记录场景"
              value={params.get("scene") || ""}
              onChange={(value) => filter("scene", value)}
              options={[
                { value: "", label: "全部场景" },
                ...scenes.map((s) => ({ value: s.id, label: s.name })),
                ...(params.get("scene") &&
                !scenes.some((s) => s.id === params.get("scene"))
                  ? [{ value: params.get("scene")!, label: "历史场景" }]
                  : []),
              ]}
            />
          )}
          {kind !== "hit" && (
            <Choice
              label="错误原因"
              value={params.get("error_kind") || ""}
              onChange={(value) => filter("error_kind", value)}
              options={[
                { value: "", label: "全部原因" },
                ...Object.entries(errorNames)
                  .filter(([key]) =>
                    kind === "warning"
                      ? [
                          "classifier_input_too_long",
                          "session_blocked",
                        ].includes(key)
                      : ![
                          "classifier_input_too_long",
                          "session_blocked",
                        ].includes(key),
                  )
                  .map(([value, label]) => ({
                    value,
                    label,
                  })),
              ]}
            />
          )}
          {params.get("model") && (
            <button
              className="filter-chip"
              onClick={() => filter("model", "")}
              aria-label="清除模型筛选"
            >
              {params.get("model")}
              <X size={12} />
            </button>
          )}
          {kind === "hit" && (
            <Choice
              label="处理结果"
              value={params.get("action") || ""}
              onChange={(value) => filter("action", value)}
              options={[
                { value: "", label: "全部结果" },
                { value: "block", label: "拦截" },
                { value: "allow", label: "记录放行" },
              ]}
            />
          )}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="icon-sm" variant="ghost" aria-label="显示列">
                <Columns3 size={14} />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              {table.getAllLeafColumns().map((column) => (
                <DropdownMenuCheckboxItem
                  key={column.id}
                  checked={column.getIsVisible()}
                  onCheckedChange={(value) => column.toggleVisibility(value)}
                >
                  {typeof column.columnDef.header === "string"
                    ? column.columnDef.header
                    : "时间"}
                </DropdownMenuCheckboxItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
        {result.isError ? (
          <div className="panel-body">
            <ErrorState
              error={result.error}
              retry={() => void result.refetch()}
            />
          </div>
        ) : result.isPending ? (
          <Loading />
        ) : (
          <>
            <div className="table-scroll">
              <table className="data-table table-clickable">
                <thead>
                  {table.getHeaderGroups().map((group) => (
                    <tr key={group.id}>
                      {group.headers.map((header) => (
                        <th key={header.id}>
                          {flexRender(
                            header.column.columnDef.header,
                            header.getContext(),
                          )}
                        </th>
                      ))}
                    </tr>
                  ))}
                </thead>
                <tbody>
                  {table.getRowModel().rows.map((row) => (
                    <tr
                      key={row.original.id}
                      tabIndex={0}
                      aria-label={`查看请求 ${row.original.request_id}`}
                      onClick={() => {
                        const next = new URLSearchParams(params);
                        next.set("event", row.original.id);
                        setParams(next);
                      }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          const next = new URLSearchParams(params);
                          next.set("event", row.original.id);
                          setParams(next);
                        }
                      }}
                    >
                      {row.getVisibleCells().map((cell) => (
                        <td
                          key={cell.id}
                          style={
                            cell.column.id === "risk"
                              ? {
                                  maxWidth: 190,
                                  overflow: "hidden",
                                  textOverflow: "ellipsis",
                                }
                              : undefined
                          }
                        >
                          {flexRender(
                            cell.column.columnDef.cell,
                            cell.getContext(),
                          )}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
              {!result.data?.length && <Empty />}
            </div>
            <div className="pagination">
              <span>
                {result.data?.length
                  ? `${offset + 1}–${offset + result.data.length} 条`
                  : "0 条"}
              </span>
              <div className="heading-actions">
                <Button
                  variant="outline"
                  size="icon-sm"
                  aria-label="上一页"
                  disabled={offset === 0}
                  onClick={() => {
                    const next = new URLSearchParams(params);
                    next.set("offset", String(Math.max(0, offset - 50)));
                    setParams(next);
                  }}
                >
                  <ArrowLeft size={13} />
                </Button>
                <span>第 {offset / 50 + 1} 页</span>
                <Button
                  variant="outline"
                  size="icon-sm"
                  aria-label="下一页"
                  disabled={(result.data?.length || 0) < 50}
                  onClick={() => {
                    const next = new URLSearchParams(params);
                    next.set("offset", String(offset + 50));
                    setParams(next);
                  }}
                >
                  <ArrowRight size={13} />
                </Button>
              </div>
            </div>
          </>
        )}
      </div>
      <Sheet
        open={Boolean(selected)}
        onOpenChange={(open) => {
          if (!open) {
            const next = new URLSearchParams(params);
            next.delete("event");
            setParams(next);
          }
        }}
      >
        <SheetContent className="w-full sm:max-w-[620px] p-0 gap-0">
          <SheetHeader className="border-b px-5 py-5">
            <SheetTitle>请求详情</SheetTitle>
            <SheetDescription className="sr-only">
              当前用户文本、请求参数与场景判定
            </SheetDescription>
          </SheetHeader>
          {detail.isError ? (
            <div className="panel-body">
              <ErrorState error={detail.error} />
            </div>
          ) : !current ? (
            <Loading />
          ) : (
            <div className="detail-content">
              <div className="editor-block-title">
                <EndpointLabel id={current.protocol} />
                {current.kind !== "hit" ? (
                  <span className="subtle-badge">
                    {errorNames[current.error_kind || ""] || "Jev 错误"}
                  </span>
                ) : (
                  <ActionBadge action={current.decision.action} />
                )}
              </div>
              <dl className="detail-facts record-summary">
                <div>
                  <dt>请求时间</dt>
                  <dd>{fullDate(current.time)}</dd>
                </div>
                <div>
                  <dt>模型</dt>
                  <dd>{current.model || "—"}</dd>
                </div>
                <div>
                  <dt>来源 IP</dt>
                  <dd>
                    {current.client_ip ? (
                      <button
                        className="detail-link"
                        aria-label={`查看 IP ${current.client_ip} 的记录`}
                        onClick={() => filter("client_ip", current.client_ip!)}
                      >
                        {current.client_ip}
                        <Search size={12} />
                      </button>
                    ) : (
                      "—"
                    )}
                  </dd>
                </div>
                <div>
                  <dt>会话 ID</dt>
                  <dd>
                    {current.session_id ? (
                      <button
                        className="detail-link"
                        aria-label={`查看会话 ${current.session_id} 的记录`}
                        onClick={() =>
                          filter("session_id", current.session_id!)
                        }
                      >
                        {current.session_id}
                        <Search size={12} />
                      </button>
                    ) : (
                      "—"
                    )}
                  </dd>
                </div>
                <div>
                  <dt>生效场景</dt>
                  <dd>{current.decision.scene_name || "—"}</dd>
                </div>
                <div>
                  <dt>审查耗时</dt>
                  <dd>
                    {current.kind === "warning"
                      ? "—"
                      : duration(current.classifier_ms)}
                  </dd>
                </div>
              </dl>
              {current.error_kind === "classifier_input_too_long" && (
                <dl className="detail-facts">
                  <div>
                    <dt>估算输入</dt>
                    <dd>{count(current.input_tokens_estimated || 0)} Token</dd>
                  </div>
                  <div>
                    <dt>送审上限</dt>
                    <dd>{count(current.jev_input_limit || 0)} Token</dd>
                  </div>
                </dl>
              )}
              <div className="editor-block-title">
                <h3 className="section-label">当前用户文本</h3>
                {(current.text ||
                  current.text_preview ||
                  current.scores?.length) && (
                  <Button asChild variant="outline" size="sm">
                    <Link
                      to={`/scenes?sample=${encodeURIComponent(current.id)}${current.decision.scene_id ? `&scene=${encodeURIComponent(current.decision.scene_id)}` : ""}`}
                    >
                      <FlaskConical size={13} />
                      用此记录试算
                    </Link>
                  </Button>
                )}
              </div>
              <div className="request-text">
                {current.text || current.text_preview || "未记录文本"}
              </div>
              {!!current.scores?.length && (
                <ScoreBreakdown
                  key={current.id}
                  scores={current.scores}
                  hits={current.decision.hits}
                  comparisons={
                    current.trace?.find((t) => t.status === "effective")
                      ?.conditions
                  }
                />
              )}
              <details className="record-parameters">
                <summary>请求参数</summary>
                <dl className="detail-facts">
                  <div>
                    <dt>端点</dt>
                    <dd>
                      {current.endpoint ||
                        endpoints.find((e) => e.id === current.protocol)?.path}
                    </dd>
                  </div>
                  <div>
                    <dt>响应方式</dt>
                    <dd>{current.stream ? "流式" : "非流式"}</dd>
                  </div>
                  <div className="full-fact">
                    <dt>请求 ID</dt>
                    <dd className="heading-actions">
                      <span>{current.request_id}</span>
                      <CopyButton
                        value={current.request_id}
                        label="复制请求 ID"
                      />
                    </dd>
                  </div>
                  {current.client_request_id && (
                    <div className="full-fact">
                      <dt>客户端请求 ID</dt>
                      <dd>{current.client_request_id}</dd>
                    </div>
                  )}
                  <div className="full-fact">
                    <dt>客户端</dt>
                    <dd>{current.user_agent || "—"}</dd>
                  </div>
                  {current.session_id && (
                    <div className="full-fact">
                      <dt>会话 ID</dt>
                      <dd className="heading-actions">
                        <span>{current.session_id}</span>
                        <CopyButton
                          value={current.session_id}
                          label="复制会话 ID"
                        />
                      </dd>
                    </div>
                  )}
                  {Object.entries(current.parameters || {})
                    .filter(
                      ([key, value]) =>
                        value !== undefined &&
                        value !== "" &&
                        parameterLabels[key],
                    )
                    .map(([key, value]) => (
                      <div
                        key={key}
                        className={key.includes("_id") ? "full-fact" : ""}
                      >
                        <dt>{parameterLabels[key]}</dt>
                        <dd>
                          {typeof value === "number"
                            ? count(value)
                            : String(value)}
                        </dd>
                      </div>
                    ))}
                </dl>
              </details>
              {current.trace && (
                <details style={{ marginTop: 20 }}>
                  <summary className="section-label">当时的匹配过程</summary>
                  {current.trace.map((trace) => (
                    <div className="trace-item" key={trace.id}>
                      <span>{trace.name}</span>
                      <span className="trace-status">
                        {
                          {
                            effective: "最终生效",
                            shadowed: "优先级未采用",
                            not_matched: "未匹配",
                            endpoint_skipped: "端点不适用",
                            disabled: "已暂停",
                            model_skipped: "模型不适用",
                          }[trace.status]
                        }
                      </span>
                    </div>
                  ))}
                </details>
              )}
            </div>
          )}
        </SheetContent>
      </Sheet>
    </>
  );
}

const parameterLabels: Record<string, string> = {
  reasoning_effort: "推理强度",
  service_tier: "服务等级",
  max_tokens: "最大输出 Token",
  max_output_tokens: "最大输出 Token",
  max_completion_tokens: "最大生成 Token",
  temperature: "采样温度",
  top_p: "采样概率",
  seed: "随机种子",
  tool_count: "工具数量",
  tool_choice: "工具选择",
  response_format: "输出格式",
  thinking_type: "思考模式",
  thinking_budget: "思考预算",
  previous_response_id: "前序响应 ID",
  conversation_id: "对话 ID",
};
