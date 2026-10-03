import { useEffect, useState } from "react";
import { toast } from "sonner";
import { notifyError } from "../notifications";
import type { Policy, PolicyPatch } from "../policy";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Help } from "./common";

export function TrustedKeySettings({
  policy,
  onSave,
}: {
  policy: Policy;
  onSave: (value: PolicyPatch) => Promise<void>;
}) {
  const current = policy.trusted_key_idle_days || 30;
  const [days, setDays] = useState(String(current));
  const [busy, setBusy] = useState(false);
  useEffect(() => setDays(String(current)), [current]);
  const value = Number(days),
    valid = Number.isInteger(value) && value > 0 && value <= 106751;
  async function save() {
    if (!valid || busy) return;
    setBusy(true);
    try {
      await onSave({ trusted_key_idle_days: value });
      toast.success("可信密钥设置已保存");
    } catch (e) {
      notifyError(e);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form
      className="trusted-key-settings-row"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <label className="trusted-key-expiry-field">
        <span>闲置清除</span>
        <Help>
          密钥首次获得上游成功响应后加入可信列表。每次请求刷新闲置时间，过期后重新验证。
        </Help>
        <Input
          aria-label="闲置清除天数"
          type="number"
          min={1}
          max={106751}
          step={1}
          value={days}
          onChange={(e) => setDays(e.target.value)}
          disabled={busy}
        />
        <span>天</span>
      </label>
      <Button
        type="submit"
        size="sm"
        aria-label="保存可信密钥设置"
        disabled={busy || !valid || value === current}
      >
        {busy ? "保存中…" : "保存"}
      </Button>
    </form>
  );
}
