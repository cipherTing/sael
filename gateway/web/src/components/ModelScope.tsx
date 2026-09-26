import { useId, useState } from "react";
import { Plus, X } from "lucide-react";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Help } from "./common";

export function ModelScope({
  models,
  suggestions,
  onChange,
}: {
  models: string[];
  suggestions: string[];
  onChange: (models: string[]) => void;
}) {
  const [input, setInput] = useState("");
  const id = useId();
  function add(value = input) {
    const names = value
      .split(/[,，\n]+/)
      .map((v) => v.trim())
      .filter(Boolean);
    if (names.length) onChange([...new Set([...models, ...names])]);
    setInput("");
  }
  return (
    <div className="model-scope">
      <div className="editor-block-title">
        <h3>
          生效模型 <Help>按请求中的模型名精确匹配。留空适用于全部模型。</Help>
        </h3>
        {models.length > 0 && (
          <Button variant="ghost" size="sm" onClick={() => onChange([])}>
            全部模型
          </Button>
        )}
      </div>
      <div className="model-tags">
        {!models.length && <span className="subtle-badge">全部模型</span>}
        {models.map((model) => (
          <span className="model-tag" key={model}>
            <span>{model}</span>
            <button
              aria-label={`移除模型 ${model}`}
              onClick={() => onChange(models.filter((m) => m !== model))}
            >
              <X size={12} />
            </button>
          </span>
        ))}
      </div>
      <div className="model-input">
        <Input
          list={id}
          aria-label="添加生效模型"
          placeholder="输入模型名，回车添加"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.nativeEvent.isComposing) {
              e.preventDefault();
              add();
            }
          }}
          onBlur={() => {
            if (input.trim()) add();
          }}
          onPaste={(e) => {
            const text = e.clipboardData.getData("text");
            if (/[,，\n]/.test(text)) {
              e.preventDefault();
              add(input + text);
            }
          }}
        />
        <Button
          variant="outline"
          size="icon-sm"
          aria-label="添加模型"
          disabled={!input.trim()}
          onClick={() => add()}
        >
          <Plus size={14} />
        </Button>
      </div>
      <datalist id={id}>
        {suggestions
          .filter((m) => !models.includes(m))
          .map((m) => (
            <option key={m} value={m} />
          ))}
      </datalist>
    </div>
  );
}
