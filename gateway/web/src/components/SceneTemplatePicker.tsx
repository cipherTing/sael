import { useState } from "react";
import { Copy, Search } from "lucide-react";
import type { Scene } from "../policy";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogHeader,
  DialogDescription,
  DialogTrigger,
} from "./ui/dialog";
import { SceneModeBadge, Empty } from "./common";

export function SceneTemplatePicker({
  scenes,
  onSelect,
}: {
  scenes: Scene[];
  onSelect: (scene: Scene) => void;
}) {
  const [open, setOpen] = useState(false),
    [search, setSearch] = useState("");
  const matches = scenes.filter((s) =>
    `${s.name} ${s.note || ""}`
      .toLocaleLowerCase()
      .includes(search.toLocaleLowerCase()),
  );
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        setOpen(value);
        setSearch("");
      }}
    >
      <DialogTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          aria-label="从场景模板创建"
          disabled={!scenes.length}
        >
          <Copy size={13} />
          使用场景模板
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>选择场景模板</DialogTitle>
          <DialogDescription className="sr-only">
            将所选场景的配置复制到当前编辑器
          </DialogDescription>
        </DialogHeader>
        <div className="template-search">
          <Search size={15} />
          <Input
            aria-label="搜索场景模板"
            placeholder="搜索名称或注释"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <div className="template-list">
          {matches.length ? (
            matches.map((s) => (
              <button
                key={s.id}
                className="template-item"
                aria-label={`使用模板 ${s.name}`}
                onClick={() => {
                  onSelect(s);
                  setOpen(false);
                }}
              >
                <div>
                  <strong>{s.name}</strong>
                  {s.note && <p>{s.note}</p>}
                </div>
                <SceneModeBadge action={s.action} />
              </button>
            ))
          ) : (
            <Empty />
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
