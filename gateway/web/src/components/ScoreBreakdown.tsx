import { useState } from "react";
import { ChevronDown, Check } from "lucide-react";
import type { Answer, Hit } from "../types";
import { questionMeta, questionName } from "../questionMeta";
import "../record-detail.css";

type Comparison = {
  question: string;
  value?: number;
  record_only?: boolean;
  threshold: number;
  matched: boolean;
};

export function ScoreBreakdown({
  scores,
  hits = [],
  comparisons = [],
}: {
  scores: Answer[];
  hits?: Hit[];
  comparisons?: Comparison[];
}) {
  const [expanded, setExpanded] = useState(false);
  const samples = [
    ...scores,
    ...hits
      .filter(
        (hit) =>
          hit.value !== undefined &&
          !scores.some((score) => score.question === hit.question),
      )
      .map((hit) => ({
        question: hit.question,
        value: hit.value!,
        type:
          hit.question === "gore" || hit.question === "sexual"
            ? "score"
            : "noul",
      })),
  ];
  const seen = new Set<string>();
  const rows = samples.flatMap((score) => {
    const max =
      score.question === "gore" || score.question === "sexual" ? 3 : 1;
    if (
      !questionMeta[score.question] ||
      seen.has(score.question) ||
      !Number.isFinite(score.value) ||
      score.value < 0 ||
      score.value > max
    )
      return [];
    seen.add(score.question);
    const hit = hits.find((item) => item.question === score.question),
      comparison = comparisons.find((item) => item.question === score.question);
    const rawThreshold = comparison?.threshold ?? hit?.threshold;
    const threshold =
      rawThreshold !== undefined &&
      Number.isFinite(rawThreshold) &&
      rawThreshold >= 0 &&
      rawThreshold <= max
        ? rawThreshold
        : undefined;
    return [
      {
        ...score,
        max,
        threshold,
        matched: threshold !== undefined && score.value > threshold,
        recordOnly: comparison?.record_only ?? hit?.record_only,
      },
    ];
  });
  if (!rows.length) {
    const facts = comparisons.length
      ? comparisons
      : hits.map((hit) => ({ ...hit, matched: true }));
    if (!facts.length) return null;
    return (
      <section className="score-breakdown">
        <h3>条件判定</h3>
        <p>缓存判定，无原始分数</p>
        <div className="risk-score-grid">
          {facts.map((fact) => (
            <article
              key={fact.question}
              className={`risk-score-card ${fact.matched ? "reached" : ""}`}
            >
              <strong>{questionName(fact.question)}</strong>
              <p>阈值 &gt; {fact.threshold}</p>
              <p>
                {fact.matched ? "已命中" : "未命中"}
                {fact.record_only !== undefined &&
                  ` · ${fact.record_only ? "仅记录" : "拒绝"}`}
              </p>
            </article>
          ))}
        </div>
      </section>
    );
  }
  const reached = rows.filter((row) => row.matched),
    below = rows.filter((row) => !row.matched && row.threshold !== undefined),
    unconfigured = rows.filter((row) => row.threshold === undefined);

  function cards(items: typeof rows) {
    return (
      <div className="risk-score-grid">
        {items.map((row) => (
          <article
            key={row.question}
            aria-label={`${questionName(row.question)}评分`}
            className={`risk-score-card ${row.max === 3 ? "graded" : "probability"} ${row.matched ? "reached" : ""}`}
          >
            <div className="risk-score-card-heading">
              <strong>{questionName(row.question)}</strong>
              <span>{row.max === 3 ? "程度分级" : "风险评分"}</span>
            </div>
            <div className="risk-score-values">
              <b>{row.value}</b>
              <span>/ {row.max}</span>
              <small>
                {row.matched && <Check size={12} />}
                {row.threshold === undefined
                  ? "未配置阈值"
                  : `> ${row.threshold}`}
              </small>
            </div>
            <div
              className="risk-score-track"
              role="img"
              aria-label={`${questionName(row.question)} ${row.value}，原始刻度 0 至 ${row.max}`}
            >
              {row.max === 3 && (
                <div className="risk-score-segments">
                  <span />
                  <span />
                  <span />
                </div>
              )}
              <span
                className="risk-score-fill"
                style={{ width: `${(100 * row.value) / row.max}%` }}
              />
              {row.threshold !== undefined && (
                <span
                  className="risk-threshold-marker"
                  aria-label={`${questionName(row.question)}阈值 ${row.threshold}`}
                  style={{ left: `${(100 * row.threshold) / row.max}%` }}
                />
              )}
              <span
                className="risk-score-dot"
                style={{ left: `${(100 * row.value) / row.max}%` }}
              />
            </div>
            {row.recordOnly !== undefined && (
              <p className="risk-condition-disposition">
                命中处置：{row.recordOnly ? "仅记录" : "拒绝"}
              </p>
            )}
            <div className="risk-score-scale">
              {(row.max === 3 ? [0, 1, 2, 3] : [0, 1]).map((tick) => (
                <span key={tick}>{tick}</span>
              ))}
            </div>
          </article>
        ))}
      </div>
    );
  }
  return (
    <section className="score-breakdown visual-score-breakdown">
      <h3 className="section-label score-heading">
        达到阈值 <span>{reached.length}</span>
      </h3>
      {reached.length > 0 && cards(reached)}
      {(below.length > 0 || unconfigured.length > 0) && (
        <details
          open={expanded}
          onToggle={(event) => setExpanded(event.currentTarget.open)}
          className="remaining-scores"
        >
          <summary>
            <ChevronDown size={14} />
            其余分数 <span>{below.length + unconfigured.length}</span>
          </summary>
          {below.length > 0 && (
            <div>
              <h4>未达到阈值</h4>
              {cards(below)}
            </div>
          )}
          {unconfigured.length > 0 && (
            <div>
              <h4>未配置阈值</h4>
              {cards(unconfigured)}
            </div>
          )}
        </details>
      )}
    </section>
  );
}
