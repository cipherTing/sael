import { Trash2 } from "lucide-react";
import type { Condition, Question } from "../policy";
import { questionMeta, questionName } from "../questionMeta";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Slider } from "./ui/slider";
import { Choice, Help } from "./common";

type Props = {
  condition: Condition;
  questions: Question[];
  index: number;
  invalid: boolean;
  sampleScore?: number;
  onChange: (patch: Partial<Condition>) => void;
  onRemove: () => void;
};

export function ConditionEditor({
  condition,
  questions,
  index,
  invalid,
  sampleScore,
  onChange,
  onRemove,
}: Props) {
  const question = questions.find((q) => q.key === condition.question);
  const max =
    question && Number.isFinite(question.max) && question.max > 0
      ? question.max
      : undefined;
  const name = questionName(condition.question);
  const graded = question?.type === "score" && max === 3;
  const thresholdValid =
    max !== undefined &&
    Number.isFinite(condition.threshold) &&
    condition.threshold >= 0 &&
    condition.threshold <= max;
  const hasSample =
    max !== undefined &&
    sampleScore !== undefined &&
    Number.isFinite(sampleScore) &&
    sampleScore >= 0 &&
    sampleScore <= max;
  const sampleMatched =
    hasSample && thresholdValid && sampleScore! > condition.threshold;
  const ticks = graded ? [0, 1, 2, 3] : [0, 0.25, 0.5, 0.75, 1];
  const gradeDescriptions = graded
    ? questionMeta[condition.question]?.description
        .split(/[；;]/)
        .map((part) => part.trim().replace(/[。.]$/, "")) || []
    : [];
  const choices = questions.map((q) => ({
    value: q.key,
    label: questionName(q.key),
  }));
  if (!question) choices.unshift({ value: condition.question, label: name });

  return (
    <div
      className={`condition-card ${graded ? "condition-card-graded" : ""} ${invalid ? "condition-card-invalid" : ""}`}
    >
      <div className="condition-card-heading">
        <Choice
          label={`审核项 ${index + 1}`}
          value={condition.question}
          options={choices}
          onChange={(key) => onChange({ question: key })}
        />
        <Help>
          {questionMeta[condition.question]?.description ||
            "此审核项已不可用，请重新选择。"}
        </Help>
        <Button
          variant="ghost"
          size="icon-xs"
          aria-label={`删除条件 ${index + 1}`}
          onClick={onRemove}
        >
          <Trash2 size={13} />
        </Button>
      </div>
      {max === undefined ? (
        <span className="condition-unavailable">审核项不可用</span>
      ) : (
        <>
          <div className="condition-boundary">
            <span className="condition-scale-label">
              {graded ? "内容等级" : "风险值"}
            </span>
            <label className="condition-value">
              <span>
                阈值 <strong>&gt;</strong>
              </span>
              <Input
                aria-label={`${name}阈值`}
                aria-invalid={invalid || undefined}
                type="number"
                min={0}
                max={max}
                step="any"
                value={
                  Number.isFinite(condition.threshold)
                    ? condition.threshold
                    : ""
                }
                placeholder="填写"
                onChange={(e) =>
                  onChange({
                    threshold:
                      e.target.value === "" ? NaN : Number(e.target.value),
                  })
                }
              />
            </label>
          </div>
          <div
            className={`condition-scale ${graded ? "condition-scale-graded" : ""}`}
          >
            {hasSample && (
              <span
                className={`condition-sample-marker ${sampleMatched ? "matched" : ""}`}
                aria-label={`${name}样本分数 ${sampleScore}，${sampleMatched ? "已满足条件" : "未满足条件"}`}
                style={{ left: `${(sampleScore! / max) * 100}%` }}
              >
                <span />
              </span>
            )}
            <Slider
              min={0}
              max={max}
              step={0.01}
              value={[
                Number.isFinite(condition.threshold)
                  ? Math.max(0, Math.min(max, condition.threshold))
                  : 0,
              ]}
              thumbLabel={`${name}阈值滑杆`}
              onValueChange={([threshold]) => onChange({ threshold })}
            />
            <div className="condition-scale-ticks" aria-hidden="true">
              {ticks.map((tick, i) => (
                <span
                  key={tick}
                  title={graded ? gradeDescriptions[i] : undefined}
                >
                  {tick}
                </span>
              ))}
            </div>
            {graded && (
              <div className="condition-grade-labels">
                {gradeDescriptions.map((label, i) => (
                  <span key={i} title={label}>
                    {label.replace(/^\d\s*/, "")}
                  </span>
                ))}
              </div>
            )}
          </div>
          {hasSample && (
            <div
              className={`condition-sample-result ${sampleMatched ? "matched" : ""}`}
            >
              <span className="condition-sample-dot" />
              样本 <strong>{sampleScore}</strong>
              <span>{sampleMatched ? "满足条件" : "未满足"}</span>
            </div>
          )}
        </>
      )}
    </div>
  );
}
