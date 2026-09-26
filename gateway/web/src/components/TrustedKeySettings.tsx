import { useEffect, useState } from "react";
import { toast } from "sonner";
import type { Policy } from "../policy";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Help, Panel, ErrorState } from "./common";

export function TrustedKeySettings({
  policy,
  onSave,
}: {
  policy: Policy;
  onSave: (value: Policy) => Promise<void>;
}) {
  const current = policy.trusted_key_idle_days || 30;
  const [days, setDays] = useState(String(current));
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  useEffect(() => setDays(String(current)), [current, policy.version]);
  const value = Number(days),
    valid = Number.isInteger(value) && value > 0 && value <= 106751;
  async function save() {
    if (!valid || busy) return;
    setBusy(true);
    setError("");
    try {
      await onSave({ ...policy, trusted_key_idle_days: value });
      toast.success("可信密钥设置已保存");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="session-settings">
      <Panel
        title="可信密钥"
        extra={
          <Help>
            密钥首次获得上游成功响应后加入可信列表。每次请求刷新闲置时间，过期后重新验证。
          </Help>
        }
      >
        <form
          className="trusted-key-settings-row"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <label className="field">
            <span>闲置清除天数</span>
            <Input
              aria-label="闲置清除天数"
              type="number"
              min={1}
              max={106751}
              step={1}
              value={days}
              onChange={(e) => setDays(e.target.value)}
            />
          </label>
          <Button
            type="submit"
            size="sm"
            disabled={busy || !valid || value === current}
          >
            {busy ? "保存中…" : "保存可信密钥设置"}
          </Button>
        </form>
        {error && (
          <div className="panel-body">
            <ErrorState error={error} />
          </div>
        )}
      </Panel>
    </div>
  );
}
