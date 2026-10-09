import type { ReactNode } from "react";
import { CircleHelp, Loader2, Copy, Check } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
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
import { endpoints, endpointGroup } from "../analytics";

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
export function EndpointLabel({
  id,
  compact = false,
}: {
  id: string;
  compact?: boolean;
}) {
  const e = endpoints.find((x) => x.id === endpointGroup(id));
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
  visibleLabel,
  className = "",
}: {
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: ReactNode }[];
  label: string;
  visibleLabel?: string;
  className?: string;
}) {
  const select = (
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
  return visibleLabel ? (
    <label className="control-field">
      <span className="control-field-label">{visibleLabel}</span>
      {select}
    </label>
  ) : (
    select
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
      type="button"
      size="icon-sm"
      variant="ghost"
      aria-label={label}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") event.stopPropagation();
      }}
      onClick={async (event) => {
        event.stopPropagation();
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          toast.success("已复制");
          setTimeout(() => setCopied(false), 1500);
        } catch {
          setCopied(false);
          toast.error("复制失败");
        }
      }}
    >
      {copied ? <Check /> : <Copy />}
    </Button>
  );
}
export function ActionBadge({
  action,
  reason,
}: {
  action: string;
  reason?: string;
}) {
  return (
    <span
      className={`action-badge ${action === "block" ? "blocked" : "allowed"}`}
    >
      {action === "block"
        ? "拦截"
        : reason === "condition_record_only"
          ? "条件仅记录"
          : reason === "scene_record_only"
            ? "场景仅记录"
            : reason === "non_blocking"
              ? "非阻塞仅记录"
              : "记录放行"}
    </span>
  );
}

export function SceneModeBadge({
  action,
  mode,
}: {
  action?: string;
  mode?: "blocking" | "non_blocking";
}) {
  const blocking = mode ? mode === "blocking" : action === "block";
  return (
    <span className={`action-badge ${blocking ? "blocked" : "allowed"}`}>
      {blocking ? "阻塞性审查" : "非阻塞性审查"}
    </span>
  );
}
