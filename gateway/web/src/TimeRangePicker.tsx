import { useState } from "react";
import { CalendarDays, ChevronDown } from "lucide-react";
import { zhCN } from "react-day-picker/locale";
import {
  format,
  startOfDay,
  subDays,
  startOfWeek,
  startOfMonth,
  subMonths,
} from "date-fns";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "./components/ui/popover";
import { Calendar } from "./components/ui/calendar";

export type TimeRangeValue = {
  key: string;
  label: string;
  hours: number;
  start?: string;
  end?: string;
  startDate?: string;
  endDate?: string;
};
const relative = [
  { key: "15m", label: "近 15 分钟", hours: 0.25 },
  { key: "1h", label: "近 1 小时", hours: 1 },
  { key: "6h", label: "近 6 小时", hours: 6 },
  { key: "24h", label: "近 24 小时", hours: 24 },
  { key: "3d", label: "近 3 天", hours: 72 },
  { key: "7d", label: "近 7 天", hours: 168 },
  { key: "14d", label: "近 14 天", hours: 336 },
  { key: "30d", label: "近 30 天", hours: 720 },
];
const dateText = (date: Date) => format(date, "yyyy-MM-dd");
const calendarLabels: Record<string, string> = {
  today: "今天",
  yesterday: "昨天",
  week: "本周",
  month: "本月",
  lastMonth: "上月",
};
function calendarRange(key: string, now = new Date()): TimeRangeValue {
  let from = startOfDay(now),
    to = now;
  if (key === "yesterday") {
    from = subDays(from, 1);
    to = startOfDay(now);
  }
  if (key === "week") from = startOfWeek(now, { weekStartsOn: 1 });
  if (key === "month") from = startOfMonth(now);
  if (key === "lastMonth") {
    from = startOfMonth(subMonths(now, 1));
    to = startOfMonth(now);
  }
  return {
    key,
    label: calendarLabels[key],
    hours: (to.getTime() - from.getTime()) / 3600000,
    start: from.toISOString(),
    end: to.toISOString(),
  };
}
export function requestRange(query: URLSearchParams, now = new Date()) {
  const next = new URLSearchParams(query),
    key = next.get("range") || "";
  next.delete("range");
  if (Object.hasOwn(calendarLabels, key)) {
    const value = calendarRange(key, now);
    next.delete("minutes");
    next.set("start", value.start!);
    next.set("end", value.end!);
  }
  return next;
}
export function rangeFromQuery(query: URLSearchParams): TimeRangeValue {
  const key = query.get("range") || "";
  if (Object.hasOwn(calendarLabels, key)) return calendarRange(key);
  const start = query.get("start"),
    end = query.get("end");
  if (start && end)
    return {
      key: "custom",
      label: `${format(new Date(start), "MM/dd HH:mm")} – ${format(new Date(end), "MM/dd HH:mm")}`,
      hours: (Date.parse(end) - Date.parse(start)) / 3600000,
      start,
      end,
      startDate: dateText(new Date(start)),
      endDate: dateText(new Date(Date.parse(end) - 1)),
    };
  const hours = Number(query.get("minutes") || 1440) / 60;
  return (
    relative.find((item) => item.hours === hours) || {
      key: "relative",
      label: `近 ${hours} 小时`,
      hours,
    }
  );
}
export function rangeQuery(query: URLSearchParams, value: TimeRangeValue) {
  const next = new URLSearchParams(query);
  for (const key of ["minutes", "hours", "start", "end", "range"])
    next.delete(key);
  if (Object.hasOwn(calendarLabels, value.key)) next.set("range", value.key);
  else if (value.start && value.end) {
    next.set("start", value.start);
    next.set("end", value.end);
  } else next.set("minutes", String(Math.round(value.hours * 60)));
  return next;
}
export function TimeRangePicker({
  value,
  onChange,
}: {
  value: TimeRangeValue;
  onChange: (v: TimeRangeValue) => void;
}) {
  const [open, setOpen] = useState(false),
    [start, setStart] = useState(
      value.startDate || dateText(subDays(new Date(), 1)),
    ),
    [end, setEnd] = useState(value.endDate || dateText(new Date())),
    [startTime, setStartTime] = useState("00:00"),
    [endTime, setEndTime] = useState("23:59"),
    [error, setError] = useState("");
  function choose(v: TimeRangeValue) {
    onChange(v);
    setOpen(false);
    setError("");
  }
  function apply() {
    const from = new Date(`${start}T${startTime}`),
      to = new Date(new Date(`${end}T${endTime}`).getTime() + 60000);
    if (
      !Number.isFinite(from.getTime()) ||
      !Number.isFinite(to.getTime()) ||
      to <= from
    ) {
      setError("请选择有效的开始和结束时间");
      return;
    }
    if (to.getTime() - from.getTime() > 366 * 86400000) {
      setError("时间范围不能超过 366 天");
      return;
    }
    choose({
      key: "custom",
      label: `${format(from, "MM/dd HH:mm")} – ${format(to, "MM/dd HH:mm")}`,
      hours: (to.getTime() - from.getTime()) / 3600000,
      start: from.toISOString(),
      end: to.toISOString(),
      startDate: start,
      endDate: end,
    });
  }
  function calendarPreset(key: string) {
    choose(calendarRange(key));
  }
  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        if (next && value.start && value.end) {
          const from = new Date(value.start),
            to = new Date(Date.parse(value.end) - 60000);
          setStart(dateText(from));
          setStartTime(format(from, "HH:mm"));
          setEnd(dateText(to));
          setEndTime(format(to, "HH:mm"));
        }
        setOpen(next);
        setError("");
      }}
    >
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          className="time-trigger"
          aria-label={`时间范围：${value.label}`}
        >
          <CalendarDays size={15} />
          {value.label}
          <ChevronDown size={13} />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="end"
        className="time-popover p-0"
        collisionPadding={12}
      >
        <div className="time-presets">
          {relative.map((item) => (
            <Button
              key={item.key}
              variant={value.key === item.key ? "secondary" : "ghost"}
              size="sm"
              onClick={() => choose(item)}
            >
              {item.label}
            </Button>
          ))}
          <div className="preset-divider" />
          {[
            { key: "today", label: "今天" },
            { key: "yesterday", label: "昨天" },
            { key: "week", label: "本周" },
            { key: "month", label: "本月" },
            { key: "lastMonth", label: "上月" },
          ].map((item) => (
            <Button
              key={item.key}
              variant={value.key === item.key ? "secondary" : "ghost"}
              size="sm"
              onClick={() => calendarPreset(item.key)}
            >
              {item.label}
            </Button>
          ))}
        </div>
        <div className="time-calendar">
          <Calendar
            locale={zhCN}
            defaultMonth={start ? new Date(`${start}T00:00`) : undefined}
            mode="range"
            numberOfMonths={2}
            selected={{
              from: start ? new Date(`${start}T00:00`) : undefined,
              to: end ? new Date(`${end}T00:00`) : undefined,
            }}
            onSelect={(range) => {
              setStart(range?.from ? dateText(range.from) : "");
              setEnd(range?.to ? dateText(range.to) : "");
            }}
          />
          <div className="date-apply">
            <label>
              开始
              <Input
                aria-label="开始日期"
                type="date"
                value={start}
                onChange={(e) => setStart(e.target.value)}
              />
              <Input
                aria-label="开始时间"
                type="time"
                value={startTime}
                onChange={(e) => setStartTime(e.target.value)}
              />
            </label>
            <label>
              结束
              <Input
                aria-label="结束日期"
                type="date"
                value={end}
                onChange={(e) => setEnd(e.target.value)}
              />
              <Input
                aria-label="结束时间"
                type="time"
                value={endTime}
                onChange={(e) => setEndTime(e.target.value)}
              />
            </label>
            <Button onClick={apply}>应用</Button>
          </div>
          {error && (
            <p className="field-error" role="alert">
              {error}
            </p>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
