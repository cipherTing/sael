import { useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { Button } from "./ui/button";
import { notifyError } from "../notifications";

export function RefreshButton({
  busy = false,
  onRefresh,
  label = "刷新",
}: {
  busy?: boolean;
  onRefresh: () => Promise<unknown> | void;
  label?: string;
}) {
  const lock = useRef(false);
  const [pending, setPending] = useState(false);
  async function refresh() {
    if (busy || lock.current) return;
    lock.current = true;
    setPending(true);
    try {
      await onRefresh();
    } catch (error) {
      notifyError(error);
    } finally {
      lock.current = false;
      setPending(false);
    }
  }
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="refresh-control"
      aria-label={label}
      disabled={busy || pending}
      onClick={() => void refresh()}
    >
      <RefreshCw size={14} className={busy || pending ? "animate-spin" : ""} />
      {label}
    </Button>
  );
}
