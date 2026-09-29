import { useEffect, useState } from "react";
import { toast } from "sonner";
import { notifyError } from "../notifications";
import type { Policy, PolicyPatch } from "../policy";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Switch } from "./ui/switch";
import { Help, Panel } from "./common";

export function SessionFreezeSettings({
  policy,
  onSave,
}: {
  policy: Policy;
  onSave: (value: PolicyPatch) => Promise<void>;
}) {
  const [blockingEnabled, setBlockingEnabled] = useState(
    Boolean(
      policy.session_block_on_blocking_review ?? policy.session_block_enabled,
    ),
  );
  const [nonblockingEnabled, setNonblockingEnabled] = useState(
    Boolean(policy.session_block_on_nonblocking_review),
  );
  const [minutes, setMinutes] = useState(
    (policy.session_block_ttl_seconds || 3600) / 60,
  );
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setBlockingEnabled(
      Boolean(
        policy.session_block_on_blocking_review ?? policy.session_block_enabled,
      ),
    );
    setNonblockingEnabled(Boolean(policy.session_block_on_nonblocking_review));
    setMinutes((policy.session_block_ttl_seconds || 3600) / 60);
  }, [
    policy.session_block_on_blocking_review,
    policy.session_block_on_nonblocking_review,
    policy.session_block_enabled,
    policy.session_block_ttl_seconds,
  ]);
  const seconds = Math.round(minutes * 60);
  const valid =
    Number.isFinite(minutes) && seconds > 0 && seconds <= 9223372036;
  const dirty =
    blockingEnabled !==
      Boolean(
        policy.session_block_on_blocking_review ?? policy.session_block_enabled,
      ) ||
    nonblockingEnabled !== Boolean(policy.session_block_on_nonblocking_review) ||
    seconds !== (policy.session_block_ttl_seconds || 3600);
  async function save() {
    setBusy(true);
    try {
      await onSave({
        session_block_on_blocking_review: blockingEnabled,
        session_block_on_nonblocking_review: nonblockingEnabled,
        session_block_ttl_seconds: seconds,
      });
      toast.success("会话冻结设置已保存");
    } catch (e) {
      notifyError(e);
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
            阻塞审查命中后冻结当前会话；非阻塞审查在后台命中后冻结后续请求。到期自动恢复，重试不续期。
          </Help>
        }
      >
        <div className="session-settings-row">
          <label className="review-state">
            <Switch
              aria-label="阻塞审查命中后冻结会话"
              checked={blockingEnabled}
              onCheckedChange={setBlockingEnabled}
            />
            <span>阻塞审查命中后冻结会话</span>
          </label>
          <label className="review-state">
            <Switch
              aria-label="非阻塞审查命中后冻结会话"
              checked={nonblockingEnabled}
              onCheckedChange={setNonblockingEnabled}
            />
            <span>非阻塞审查命中后冻结会话</span>
          </label>
          <label className="field">
            <span>冻结时长（分钟）</span>
            <Input
              type="number"
              min={1 / 60}
              step={1}
              disabled={!blockingEnabled && !nonblockingEnabled}
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
            {busy ? "保存中…" : "保存会话冻结"}
          </Button>
        </div>
      </Panel>
    </div>
  );
}
