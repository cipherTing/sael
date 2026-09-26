import type { ReactNode } from "react";
import { CircleHelp, Loader2, RotateCcw, Copy, Check } from "lucide-react";
import { useState } from "react";
import { Button } from "./ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "./ui/tooltip";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "./ui/select";
import { endpoints } from "../analytics";

export function Help({ children }: { children: ReactNode }) {
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <button className="help" type="button" aria-label="查看说明">
            <CircleHelp size={13} />
          </button>
        </TooltipTrigger>
        <TooltipContent className="max-w-72 leading-relaxed">
          {children}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
export function PageHeading({
  title,
  children,
}: {
  title: string;
  children?: ReactNode;
}) {
  return (
    <div className="page-heading">
      <h1>{title}</h1>
      <div className="heading-actions">{children}</div>
    </div>
  );
}
export function Panel({
  title,
  extra,
  children,
  className = "",
}: {
  title: string;
  extra?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`panel ${className}`}>
      <div className="panel-heading">
        <h2>{title}</h2>
        {extra}
      </div>
      {children}
    </section>
  );
}
export function Empty({ text = "暂无数据" }: { text?: string }) {
  return <div className="empty-state">{text}</div>;
}
export function Loading() {
  return (
    <div className="loading-state">
      <Loader2 size={20} className="animate-spin" />
      <span>加载中</span>
    </div>
  );
}
export function ErrorState({
  error,
  retry,
}: {
  error: unknown;
  retry?: () => void;
}) {
  return (
    <div role="alert" className="error-state">
      <span>{error instanceof Error ? error.message : String(error)}</span>
      {retry && (
        <Button variant="outline" size="sm" onClick={retry}>
          <RotateCcw />
          重试
        </Button>
      )}
    </div>
  );
}
export function EndpointLabel({
  id,
  compact = false,
}: {
  id: string;
  compact?: boolean;
}) {
  const e = endpoints.find((x) => x.id === id);
  return (
    <span className="endpoint-label">
      {e && <img src={e.icon} width="18" height="18" alt={e.provider} />}
      <span>{e ? (compact ? e.short : e.name) : id}</span>
    </span>
  );
}
export function Choice({
  value,
  onChange,
  options,
  label,
  className = "",
}: {
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: ReactNode }[];
  label: string;
  className?: string;
}) {
  return (
    <Select
      value={value || "__all"}
      onValueChange={(value) => onChange(value === "__all" ? "" : value)}
    >
      <SelectTrigger aria-label={label} className={className}>
        <SelectValue placeholder={label} />
      </SelectTrigger>
      <SelectContent position="popper">
        {options.map((item) => (
          <SelectItem key={item.value || "__all"} value={item.value || "__all"}>
            {item.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
export function CopyButton({
  value,
  label = "复制",
}: {
  value: string;
  label?: string;
}) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      size="icon-sm"
      variant="ghost"
      aria-label={label}
      onClick={async () => {
        await navigator.clipboard.writeText(value);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
    >
      {copied ? <Check /> : <Copy />}
    </Button>
  );
}
export function ActionBadge({ action }: { action: string }) {
  return (
    <span
      className={`action-badge ${action === "block" ? "blocked" : "allowed"}`}
    >
      {action === "block" ? "拦截" : "记录放行"}
    </span>
  );
}

export function SceneModeBadge({ action }: { action: string }) {
  return (
    <span
      className={`action-badge ${action === "block" ? "blocked" : "allowed"}`}
    >
      {action === "block" ? "阻塞性审查" : "非阻塞性审查"}
    </span>
  );
}
