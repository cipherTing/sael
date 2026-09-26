import { useState } from "react";
import { ChevronDown } from "lucide-react";
import type { Answer, Hit } from "../types";
import { questionName } from "../questionMeta";

type Comparison = {
  question: string;
  value: number;
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
  const rows = scores.map((score) => {
    const hit = hits.find((h) => h.question === score.question),
      comparison = comparisons.find((c) => c.question === score.question);
    const threshold = comparison?.threshold ?? hit?.threshold;
    return {
      ...score,
      threshold,
      matched: threshold !== undefined && score.value > threshold,
    };
  });
  const reached = rows.filter((s) => s.matched),
    below = rows.filter((s) => !s.matched && s.threshold !== undefined),
    unconfigured = rows.filter((s) => s.threshold === undefined);
  function table(items: typeof rows) {
    return (
      <table className="data-table score-table">
        <thead>
          <tr>
            <th>审核项</th>
            <th className="num">分数</th>
            <th className="num">阈值</th>
          </tr>
        </thead>
        <tbody>
          {items.map((s) => (
            <tr key={s.question}>
              <td>
                <span>{questionName(s.question)}</span>
                <div className={`score-meter ${s.matched ? "reached" : ""}`}>
                  <i
                    style={{
                      width: `${(100 * s.value) / (s.type === "score" ? 3 : 1)}%`,
                    }}
                  />
                </div>
              </td>
              <td className={`num ${s.matched ? "score-reached" : ""}`}>
                {s.value}
              </td>
              <td className="num">
                {s.threshold === undefined ? "—" : `> ${s.threshold}`}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    );
  }
  return (
    <section className="score-breakdown">
      <h3 className="section-label score-heading">
        达到阈值 <span>{reached.length}</span>
      </h3>
      {reached.length > 0 && table(reached)}
      {(below.length > 0 || unconfigured.length > 0) && (
        <details
          open={expanded}
          onToggle={(e) => setExpanded(e.currentTarget.open)}
          className="remaining-scores"
        >
          <summary>
            <ChevronDown size={14} />
            其余分数 <span>{below.length + unconfigured.length}</span>
          </summary>
          {below.length > 0 && (
            <div>
              <h4>未达到阈值</h4>
              {table(below)}
            </div>
          )}
          {unconfigured.length > 0 && (
            <div>
              <h4>未配置阈值</h4>
              {table(unconfigured)}
            </div>
          )}
        </details>
      )}
    </section>
  );
}
