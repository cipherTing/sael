import { useEffect, useState } from "react";
import { toast } from "sonner";
import type { Policy } from "../policy";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Switch } from "./ui/switch";
import { Help, Panel, ErrorState } from "./common";

export function SessionFreezeSettings({
  policy,
  onSave,
}: {
  policy: Policy;
  onSave: (value: Policy) => Promise<void>;
}) {
  const [enabled, setEnabled] = useState(Boolean(policy.session_block_enabled));
  const [minutes, setMinutes] = useState(
    (policy.session_block_ttl_seconds || 3600) / 60,
  );
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  useEffect(() => {
    setEnabled(Boolean(policy.session_block_enabled));
    setMinutes((policy.session_block_ttl_seconds || 3600) / 60);
  }, [policy.version]);
  const seconds = Math.round(minutes * 60);
  const valid =
    Number.isFinite(minutes) && seconds > 0 && seconds <= 9223372036;
  const dirty =
    enabled !== Boolean(policy.session_block_enabled) ||
    seconds !== (policy.session_block_ttl_seconds || 3600);
  async function save() {
    setBusy(true);
    setError("");
    try {
      await onSave({
        ...policy,
        session_block_enabled: enabled,
        session_block_ttl_seconds: seconds,
      });
      toast.success("会话策略已保存");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="session-settings">
      <Panel
        title="会话冻结"
        extra={
          <Help>
            命中拦截场景后，冻结同一调用凭据下的会话。到期自动恢复，重试不续期；请求必须携带凭据和显式会话
            ID。
          </Help>
        }
      >
        <div className="session-settings-row">
          <label className="review-state">
            <Switch
              aria-label="启用会话冻结"
              checked={enabled}
              onCheckedChange={setEnabled}
            />
            <span>拦截后冻结会话</span>
          </label>
          <label className="field">
            <span>冻结时长（分钟）</span>
            <Input
              type="number"
              min={1 / 60}
              step={1}
              disabled={!enabled}
              aria-label="冻结时长（分钟）"
              value={minutes || ""}
              onChange={(e) => setMinutes(Number(e.target.value))}
            />
          </label>
          <Button
            size="sm"
            disabled={!dirty || !valid || busy}
            onClick={() => void save()}
          >
            {busy ? "保存中…" : "保存会话策略"}
          </Button>
        </div>
        {error && (
          <div className="panel-body">
            <ErrorState error={error} />
          </div>
        )}
      </Panel>
    </div>
  );
}
