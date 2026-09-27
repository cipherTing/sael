import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Network, ArrowRight } from "lucide-react";
import { request } from "../api";
import type { UpstreamConfig } from "../SettingsPage";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { CopyButton, Panel } from "../components/common";
import { notifyError, notifyRetry } from "../notifications";

type Access = { ingress_url: string };
export default function AccessSettings() {
  const access = useQuery({
      queryKey: ["access"],
      queryFn: () => request<Access>("/admin/access"),
    }),
    upstream = useQuery({
      queryKey: ["upstream"],
      queryFn: () => request<UpstreamConfig>("/admin/upstream"),
    });
  const [url, setURL] = useState(""),
    [saving, setSaving] = useState(false);
  useEffect(() => {
    if (upstream.data) setURL(upstream.data.base_url);
  }, [upstream.data]);
  useEffect(() => {
    if (access.error) notifyRetry(access.error, () => void access.refetch());
    if (upstream.error)
      notifyRetry(upstream.error, () => void upstream.refetch());
  }, [access.error, access.refetch, upstream.error, upstream.refetch]);
  const ingress = access.data?.ingress_url || "";
  async function save() {
    let normalized: string;
    try {
      const parsed = new URL(url.trim());
      if (
        !["http:", "https:"].includes(parsed.protocol) ||
        parsed.username ||
        parsed.password ||
        parsed.search ||
        parsed.hash ||
        !["", "/"].includes(parsed.pathname)
      )
        throw new Error();
      normalized = parsed.origin;
    } catch {
      notifyError(new Error("请填写不带路径的 HTTP(S) 地址"));
      return;
    }
    setSaving(true);
    try {
      const saved = await request<UpstreamConfig>("/admin/upstream", {
        method: "PUT",
        body: JSON.stringify({ base_url: normalized }),
      });
      setURL(saved.base_url);
      await upstream.refetch();
    } catch (error) {
      notifyError(error);
    } finally {
      setSaving(false);
    }
  }
  return (
    <>
      <div className="access-grid">
        <Panel title="进网入口" extra={<Network size={15} color="#99a5b8" />}>
          <div className="panel-body">
            <div className="address-line">
              <code>{ingress || "—"}</code>
              {ingress && <CopyButton value={ingress} label="复制进网地址" />}
            </div>
          </div>
        </Panel>
        <Panel
          title="出站地址"
          extra={<ArrowRight size={15} color="#99a5b8" />}
        >
          <form
            className="panel-body"
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
          >
            <div className="address-line" style={{ paddingTop: 0 }}>
              <Input
                aria-label="出站地址"
                placeholder="https://api.example.com"
                value={url}
                onChange={(e) => setURL(e.target.value)}
              />
              <Button
                type="submit"
                size="sm"
                disabled={saving || url === upstream.data?.base_url}
              >
                {saving ? "保存中…" : "保存"}
              </Button>
            </div>
          </form>
        </Panel>
      </div>
    </>
  );
}
