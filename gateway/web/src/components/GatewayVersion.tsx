import { useQuery, useQueryClient } from "@tanstack/react-query";
import { request } from "../api";
import { RefreshButton } from "./RefreshButton";

type VersionInfo = {
  current_version: string;
  revision?: string;
  latest_version?: string;
  release_url?: string;
  update_available?: boolean;
  check_error?: string;
};

export default function GatewayVersion() {
  const client = useQueryClient();
  const result = useQuery({
    queryKey: ["gateway-version"],
    queryFn: () => request<VersionInfo>("/admin/version"),
    staleTime: 6 * 60 * 60 * 1000,
    refetchOnWindowFocus: false,
    retry: false,
  });
  const info = result.data;
  return (
    <div className="gateway-version" aria-label="网关版本">
      <div className="gateway-version-status">
        <span title={info?.revision}>
          {info
            ? info.current_version === "devel"
              ? "网关开发构建"
              : `网关 v${info.current_version}`
            : result.isPending
              ? "网关版本加载中"
              : "网关版本未知"}
        </span>
        {info?.update_available && info.release_url && (
          <a href={info.release_url} target="_blank" rel="noreferrer">
            新版 v{info.latest_version}
          </a>
        )}
        {info?.update_available === false && <span>已是最新版</span>}
        {(info?.check_error || result.error) && (
          <span role="status">{info?.check_error || "版本信息加载失败"}</span>
        )}
        <RefreshButton
          label="检查更新"
          busy={result.isFetching}
          onRefresh={async () => {
            const next = await request<VersionInfo>("/admin/version", {
              method: "POST",
            });
            client.setQueryData(["gateway-version"], next);
          }}
        />
      </div>
      {info?.update_available && (
        <details className="gateway-update-help">
          <summary>更新方式</summary>
          <p>在部署目录执行以下命令更新网关：</p>
          <pre>
            <code>docker compose pull gateway</code>
            {"\n"}
            <code>docker compose up -d --no-deps --wait gateway</code>
          </pre>
        </details>
      )}
    </div>
  );
}
