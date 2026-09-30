import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { PolicyTest, type Simulation } from "./PolicyTest";
import type { Policy, PolicyResponse } from "../policy";
import type { Answer } from "../types";

const active: Policy = {
  enabled: true,
  preview_chars: null,
  retention_days: 30,
  scenes: [
    {
      id: "one",
      name: "风险内容",
      match: "any",
      action: "block",
      conditions: [{ question: "gore", threshold: 1.5 }],
    },
  ],
};
const scores: Answer[] = [{ question: "gore", type: "score", value: 1.9 }];
const sample = {
  text: "测试输入",
  endpoint: "openai_chat",
  model: "model-a",
  scores,
};

function evaluate(policy: Policy, answers: Answer[]): Simulation {
  const scene = policy.scenes[0];
  const value =
    answers.find((item) => item.question === scene?.conditions[0]?.question)
      ?.value ?? 0;
  const matched = !!scene && value > scene.conditions[0].threshold;
  return {
    scores: answers,
    classifier_ms: 0,
    policy_ready: true,
    decision: {
      action: matched ? scene.action : "allow",
      scene_id: matched ? scene.id : undefined,
      scene_name: matched ? scene.name : undefined,
    },
    trace: scene
      ? [
          {
            id: scene.id,
            name: scene.name,
            status: matched ? "effective" : "not_matched",
            conditions: scene.conditions.map((condition) => ({
              ...condition,
              value:
                answers.find((item) => item.question === condition.question)
                  ?.value ?? 0,
              matched: value > condition.threshold,
            })),
          },
        ]
      : [],
  };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("serializes only policy fields when classifying with a real policy response as the active configuration", async () => {
  const response: PolicyResponse = {
    ...active,
    trusted_key_idle_days: 45,
    block_message: "Denied.",
    questions: [{ key: "gore", type: "score", max: 3 }],
  };
  const classified: {
    policy: PolicyResponse;
    compare_policy: PolicyResponse;
  }[] = [];
  vi.spyOn(toast, "error").mockReturnValue("toast");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      if (body.text !== undefined) classified.push(body);
      if (body.policy.questions || body.compare_policy?.questions)
        return new Response("测试参数格式错误", { status: 400 });
      return Response.json(evaluate(body.policy, body.scores || scores));
    }),
  );
  render(
    <PolicyTest
      policy={response}
      activePolicy={response}
      sample={{ ...sample, scores: undefined }}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "分类并试算" }));
  await waitFor(() => expect(classified).toHaveLength(1));
  expect(classified[0].policy).not.toHaveProperty("questions");
  expect(classified[0].compare_policy).not.toHaveProperty("questions");
  expect(classified[0].compare_policy).toMatchObject({
    enabled: true,
    trusted_key_idle_days: 45,
    block_message: "Denied.",
    scenes: active.scenes,
  });
  await waitFor(() =>
    expect(
      within(screen.getByRole("region", { name: "正在使用的结果" })).getByText(
        "拦截",
      ),
    ).toBeTruthy(),
  );
});

it("serializes only policy fields for score reuse with a real active policy response", async () => {
  const response: PolicyResponse = {
    ...active,
    questions: [{ key: "gore", type: "score", max: 3 }],
  };
  const bodies: { policy: PolicyResponse; scores: Answer[] }[] = [];
  vi.spyOn(toast, "error").mockReturnValue("toast");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      bodies.push(body);
      if (body.policy.questions)
        return new Response("测试参数格式错误", { status: 400 });
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(
    <PolicyTest policy={response} activePolicy={response} sample={sample} />,
  );
  await waitFor(() => expect(bodies).toHaveLength(2));
  expect(bodies[0].policy).not.toHaveProperty("questions");
  expect(bodies[1].policy).not.toHaveProperty("questions");
  expect(bodies[0].scores).toEqual(scores);
  await waitFor(() =>
    expect(
      within(screen.getByRole("region", { name: "正在使用的结果" })).getByText(
        "拦截",
      ),
    ).toBeTruthy(),
  );
  expect(
    within(screen.getByRole("region", { name: "当前草稿的结果" })).getByText(
      "拦截",
    ),
  ).toBeTruthy();
});

it("compares active and draft outcomes using the same scores without another classification", async () => {
  const draft = {
    ...active,
    scenes: [
      {
        ...active.scenes[0],
        conditions: [{ question: "gore", threshold: 2.1 }],
      },
    ],
  };
  const payloads: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      payloads.push(body);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(<PolicyTest policy={draft} activePolicy={active} sample={sample} />);
  const live = screen.getByRole("region", { name: "正在使用的结果" });
  const unpublished = screen.getByRole("region", { name: "当前草稿的结果" });
  await waitFor(() => expect(within(live).getByText("拦截")).toBeTruthy());
  expect(within(unpublished).getByText("放行")).toBeTruthy();
  expect(screen.getByText("处理结果不同")).toBeTruthy();
  expect(payloads).toHaveLength(2);
  expect(
    payloads.every(
      (body) => body.text === undefined && Array.isArray(body.scores),
    ),
  ).toBe(true);
});

it("shows missing question names and waits for explicit reclassification instead of reporting errors repeatedly", async () => {
  const draft = {
    ...active,
    scenes: [
      {
        ...active.scenes[0],
        conditions: [{ question: "self_harm", threshold: 0.8 }],
      },
    ],
  };
  const error = vi.spyOn(toast, "error").mockReturnValue("toast");
  const requests: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      requests.push(body);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(<PolicyTest policy={draft} activePolicy={active} sample={sample} />);
  await waitFor(() => expect(screen.getByText("缺少审核项")).toBeTruthy());
  expect(screen.getByText("自伤风险")).toBeTruthy();
  expect(screen.getByRole("button", { name: "重新分类" })).toBeTruthy();
  await new Promise((resolve) => setTimeout(resolve, 240));
  expect(requests).toHaveLength(1);
  expect(error).not.toHaveBeenCalled();
  expect(
    within(screen.getByRole("region", { name: "当前草稿的结果" })).queryByText(
      "放行",
    ),
  ).toBeNull();
});

it("classifies the active and draft question union once and reuses its scores for both results", async () => {
  const draft = {
    ...active,
    scenes: [
      {
        ...active.scenes[0],
        action: "allow" as const,
        conditions: [{ question: "self_harm", threshold: 0.8 }],
      },
    ],
  };
  const allScores: Answer[] = [
    ...scores,
    { question: "self_harm", type: "noul", value: 0.9 },
  ];
  const classified: Record<string, unknown>[] = [];
  const notified = vi.fn();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      if (body.text !== undefined) {
        classified.push(body);
        return Response.json({
          ...evaluate(body.policy, allScores),
          classifier_ms: 321,
        });
      }
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(
    <PolicyTest
      policy={draft}
      activePolicy={active}
      sample={{ ...sample, scores: undefined }}
      onScoresChange={notified}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "分类并试算" }));
  await waitFor(() =>
    expect(
      within(screen.getByRole("region", { name: "正在使用的结果" })).getByText(
        "拦截",
      ),
    ).toBeTruthy(),
  );
  expect(
    within(screen.getByRole("region", { name: "当前草稿的结果" })).getByText(
      "记录放行",
    ),
  ).toBeTruthy();
  expect(classified).toHaveLength(1);
  expect(classified[0]).toMatchObject({
    policy: draft,
    compare_policy: active,
    text: "测试输入",
  });
  expect(notified).toHaveBeenCalledWith(allScores);
});

it("keeps classification scores when thresholds change and clears the old result when a new question is added", async () => {
  const classifyCalls: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      if (body.text !== undefined) classifyCalls.push(body);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  const { rerender } = render(<PolicyTest policy={active} sample={sample} />);
  await screen.findByText("拦截");
  const changed = {
    ...active,
    scenes: [
      {
        ...active.scenes[0],
        conditions: [{ question: "gore", threshold: 2.3 }],
      },
    ],
  };
  rerender(<PolicyTest policy={changed} sample={sample} />);
  await screen.findByText("放行");
  expect(classifyCalls).toHaveLength(0);
  rerender(
    <PolicyTest
      policy={{
        ...changed,
        scenes: [
          {
            ...changed.scenes[0],
            conditions: [{ question: "self_harm", threshold: 0.8 }],
          },
        ],
      }}
      sample={sample}
    />,
  );
  await screen.findByText("缺少审核项");
  expect(screen.queryByText("放行")).toBeNull();
  expect(classifyCalls).toHaveLength(0);
});

it("invalidates scores on model and text changes instead of reusing a previous sample", async () => {
  const payloads: Record<string, unknown>[] = [];
  const notified = vi.fn();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      payloads.push(body);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(
    <PolicyTest policy={active} sample={sample} onScoresChange={notified} />,
  );
  await screen.findByText("拦截");
  fireEvent.change(screen.getByRole("textbox", { name: "测试模型" }), {
    target: { value: "model-b" },
  });
  expect(screen.queryByText("拦截")).toBeNull();
  expect(screen.queryByText(/复用 1 项分数/)).toBeNull();
  fireEvent.change(screen.getByRole("textbox", { name: "测试文本" }), {
    target: { value: "新的输入" },
  });
  await new Promise((resolve) => setTimeout(resolve, 240));
  expect(payloads).toHaveLength(1);
  expect(notified).toHaveBeenLastCalledWith(undefined);
});

it("limits missing scores to enabled scenes matching the current endpoint and model", async () => {
  const scoped: Policy = {
    ...active,
    enabled: false,
    scenes: [
      active.scenes[0],
      {
        ...active.scenes[0],
        id: "other",
        conditions: [{ question: "self_harm", threshold: 0.8 }],
        endpoints: ["anthropic"],
      },
      {
        ...active.scenes[0],
        id: "paused",
        conditions: [{ question: "sexual", threshold: 1 }],
        enabled: false,
      },
      {
        ...active.scenes[0],
        id: "model",
        conditions: [{ question: "violence", threshold: 0.6 }],
        models: ["other-model"],
      },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(<PolicyTest policy={scoped} sample={sample} />);
  await screen.findByText("拦截");
  expect(screen.queryByText("缺少审核项")).toBeNull();
});

it("discards an in-flight classification after the input changes", async () => {
  let finish: ((response: Response) => void) | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          finish = resolve;
        }),
    ),
  );
  render(
    <PolicyTest policy={active} sample={{ ...sample, scores: undefined }} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "分类并试算" }));
  expect(
    (screen.getByRole("button", { name: "分类中…" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  fireEvent.change(screen.getByRole("textbox", { name: "测试文本" }), {
    target: { value: "已经更换的文本" },
  });
  finish?.(Response.json({ ...evaluate(active, scores), classifier_ms: 100 }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "分类并试算" })).toBeTruthy(),
  );
  expect(screen.queryByText("拦截")).toBeNull();
  expect(screen.queryByText(/复用 1 项分数/)).toBeNull();
});

it("offers retry after a failed classification without rendering an inline error", async () => {
  const error = vi.spyOn(toast, "error").mockReturnValue("toast");
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockResolvedValueOnce(new Response("Jev 不可用", { status: 503 }))
      .mockResolvedValueOnce(Response.json(evaluate(active, scores)))
      .mockResolvedValue(Response.json(evaluate(active, scores))),
  );
  render(
    <PolicyTest policy={active} sample={{ ...sample, scores: undefined }} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "分类并试算" }));
  await waitFor(() => expect(error).toHaveBeenCalledOnce());
  const retry = screen.getByRole("button", {
    name: "分类并试算",
  }) as HTMLButtonElement;
  expect(retry.disabled).toBe(false);
  expect(screen.queryByText("Jev 不可用")).toBeNull();
  fireEvent.click(retry);
  await screen.findByText("拦截");
});

it("shows a return link only for a sample loaded from an actual record", () => {
  render(
    <PolicyTest
      policy={active}
      sample={{
        ...sample,
        scores: undefined,
        record_id: "record-one",
        record_time: "2026-09-30T03:00:00Z",
        return_url: "/records?endpoint=openai_chat&event=record-one",
      }}
    />,
  );
  expect(
    screen.getByRole("link", { name: "返回原记录" }).getAttribute("href"),
  ).toBe("/records?endpoint=openai_chat&event=record-one");
  expect(screen.getByText("历史记录样本")).toBeTruthy();
});

it("shows that live review is disabled without testing the disabled active policy", async () => {
  const bodies: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      bodies.push(body);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(
    <PolicyTest
      policy={active}
      activePolicy={{ ...active, enabled: false }}
      sample={sample}
    />,
  );
  expect(
    within(screen.getByRole("region", { name: "正在使用的结果" })).getByText(
      "审查已关闭",
    ),
  ).toBeTruthy();
  await waitFor(() =>
    expect(
      within(screen.getByRole("region", { name: "当前草稿的结果" })).getByText(
        "拦截",
      ),
    ).toBeTruthy(),
  );
  expect(bodies).toHaveLength(1);
  expect(bodies[0]).toMatchObject({ policy: { enabled: true } });
});

it("does not automatically submit an unfinished draft while keeping the active result usable", async () => {
  const bodies: Record<string, unknown>[] = [];
  const error = vi.spyOn(toast, "error").mockReturnValue("toast");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      bodies.push(body);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  const unfinished = { ...active, scenes: [{ ...active.scenes[0], name: "" }] };
  render(
    <PolicyTest policy={unfinished} activePolicy={active} sample={sample} />,
  );
  await waitFor(() =>
    expect(
      within(screen.getByRole("region", { name: "正在使用的结果" })).getByText(
        "拦截",
      ),
    ).toBeTruthy(),
  );
  expect(
    within(screen.getByRole("region", { name: "当前草稿的结果" })).getByText(
      "场景配置未完成",
    ),
  ).toBeTruthy();
  expect(bodies).toHaveLength(1);
  expect(error).not.toHaveBeenCalled();
});

it("retries failed rule evaluation with existing scores without paying for another classification", async () => {
  const bodies: Record<string, unknown>[] = [];
  vi.spyOn(toast, "error").mockReturnValue("toast");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      bodies.push(body);
      return bodies.length === 1
        ? new Response("temporary", { status: 503 })
        : Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(<PolicyTest policy={active} sample={{ ...sample, text: "" }} />);
  fireEvent.click(await screen.findByRole("button", { name: "重新试算" }));
  await screen.findByText("拦截");
  expect(bodies).toHaveLength(2);
  expect(
    bodies.every(
      (body) => Array.isArray(body.scores) && body.text === undefined,
    ),
  ).toBe(true);
});

it("does not show an old policy result after a newer evaluation has completed", async () => {
  let finishFirst: ((response: Response) => void) | undefined;
  let attempts = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      attempts++;
      if (attempts === 1)
        return new Promise<Response>((resolve) => {
          finishFirst = resolve;
        });
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  const { rerender } = render(<PolicyTest policy={active} sample={sample} />);
  await waitFor(() => expect(attempts).toBe(1));
  rerender(
    <PolicyTest
      policy={{
        ...active,
        scenes: [
          {
            ...active.scenes[0],
            conditions: [{ question: "gore", threshold: 2.4 }],
          },
        ],
      }}
      sample={sample}
    />,
  );
  await screen.findByText("放行");
  finishFirst?.(Response.json(evaluate(active, scores)));
  await waitFor(() => expect(screen.queryByText("拦截")).toBeNull());
});

it("does not evaluate old scores while a new classification is in flight", async () => {
  const bodies: Record<string, unknown>[] = [];
  let finish: ((response: Response) => void) | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      bodies.push(body);
      if (body.text !== undefined)
        return new Promise<Response>((resolve) => {
          finish = resolve;
        });
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(<PolicyTest policy={active} sample={sample} />);
  fireEvent.click(screen.getByRole("button", { name: "重新分类" }));
  await new Promise((resolve) => setTimeout(resolve, 240));
  expect(bodies).toHaveLength(1);
  expect(screen.queryByText("拦截")).toBeNull();
  finish?.(Response.json({ ...evaluate(active, scores), classifier_ms: 200 }));
  await screen.findByText("拦截");
});

it("returns to the registered events route when a historical sample has no saved return URL", () => {
  render(
    <PolicyTest
      policy={active}
      sample={{ ...sample, scores: undefined, record_id: "record one" }}
    />,
  );
  expect(
    screen.getByRole("link", { name: "返回原记录" }).getAttribute("href"),
  ).toBe("/events?event=record%20one");
});

it("marks a changed winning scene even when both configurations block", async () => {
  const draft = {
    ...active,
    scenes: [{ ...active.scenes[0], id: "new-scene", name: "新风险场景" }],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(<PolicyTest policy={draft} activePolicy={active} sample={sample} />);
  await screen.findByText("处理结果不同");
  expect(
    within(screen.getByRole("region", { name: "正在使用的结果" })).getByText(
      "拦截",
    ),
  ).toBeTruthy();
  expect(
    within(screen.getByRole("region", { name: "当前草稿的结果" })).getByText(
      "拦截",
    ),
  ).toBeTruthy();
});

it("marks a changed session freeze switch even when the same scene blocks in both configurations", async () => {
  const draft = {
    ...active,
    scenes: [
      {
        ...active.scenes[0],
        session_block_enabled: true,
        session_block_ttl_seconds: 600,
      },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(<PolicyTest policy={draft} activePolicy={active} sample={sample} />);
  await screen.findByText("处理结果不同");
  const live = screen.getByRole("region", { name: "正在使用的结果" });
  const unpublished = screen.getByRole("region", { name: "当前草稿的结果" });
  expect(within(live).getByText("拦截")).toBeTruthy();
  expect(within(live).queryByText(/冻结后续会话/)).toBeNull();
  expect(within(unpublished).getByText("冻结后续会话 · 10 分钟")).toBeTruthy();
});

it("compares the final scene freeze duration rather than only its blocking action", async () => {
  const live = {
    ...active,
    scenes: [
      {
        ...active.scenes[0],
        session_block_enabled: true,
        session_block_ttl_seconds: 3600,
      },
    ],
  };
  const draft = {
    ...live,
    scenes: [{ ...live.scenes[0], session_block_ttl_seconds: 7200 }],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(<PolicyTest policy={draft} activePolicy={live} sample={sample} />);
  await screen.findByText("处理结果不同");
  expect(
    within(screen.getByRole("region", { name: "正在使用的结果" })).getByText(
      "冻结后续会话 · 60 分钟",
    ),
  ).toBeTruthy();
  expect(
    within(screen.getByRole("region", { name: "当前草稿的结果" })).getByText(
      "冻结后续会话 · 120 分钟",
    ),
  ).toBeTruthy();
});

it("shows a nonblocking hit freezing subsequent requests but never freezes an unmatched sample", async () => {
  const recording = {
    ...active,
    scenes: [
      {
        ...active.scenes[0],
        action: "allow" as const,
        session_block_enabled: true,
        session_block_ttl_seconds: 1800,
      },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  const { rerender } = render(
    <PolicyTest policy={recording} sample={sample} />,
  );
  await screen.findByText("记录放行");
  expect(screen.getByText("冻结后续会话 · 30 分钟")).toBeTruthy();
  rerender(
    <PolicyTest
      policy={{
        ...recording,
        scenes: [
          {
            ...recording.scenes[0],
            conditions: [{ question: "gore", threshold: 2.5 }],
          },
        ],
      }}
      sample={sample}
    />,
  );
  await screen.findByText("放行");
  expect(screen.queryByText(/冻结后续会话/)).toBeNull();
});

it.each([undefined, 0, NaN, 59, 60.5, Infinity, 9223372037])(
  "does not submit a draft with enabled freeze and invalid TTL %s while preserving the active result",
  async (ttl) => {
    const bodies: { policy: Policy }[] = [];
    const error = vi.spyOn(toast, "error").mockReturnValue("toast");
    const draft = {
      ...active,
      scenes: [
        {
          ...active.scenes[0],
          session_block_enabled: true,
          session_block_ttl_seconds: ttl,
        },
      ],
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_path: string, init: RequestInit) => {
        const body = JSON.parse(init.body as string);
        bodies.push(body);
        return Response.json(evaluate(body.policy, body.scores));
      }),
    );
    render(<PolicyTest policy={draft} activePolicy={active} sample={sample} />);
    await waitFor(() =>
      expect(
        within(
          screen.getByRole("region", { name: "正在使用的结果" }),
        ).getByText("拦截"),
      ).toBeTruthy(),
    );
    expect(
      within(screen.getByRole("region", { name: "当前草稿的结果" })).getByText(
        "场景配置未完成",
      ),
    ).toBeTruthy();
    expect(bodies).toHaveLength(1);
    expect(error).not.toHaveBeenCalled();
  },
);

it("ignores a leftover invalid TTL when session freeze is disabled", async () => {
  const draft = {
    ...active,
    scenes: [
      {
        ...active.scenes[0],
        session_block_enabled: false,
        session_block_ttl_seconds: NaN,
      },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_path: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string);
      return Response.json(evaluate(body.policy, body.scores));
    }),
  );
  render(<PolicyTest policy={draft} activePolicy={active} sample={sample} />);
  await screen.findByText("处理结果相同");
  expect(
    within(screen.getByRole("region", { name: "当前草稿的结果" })).getByText(
      "拦截",
    ),
  ).toBeTruthy();
  expect(screen.queryByText("场景配置未完成")).toBeNull();
  expect(screen.queryByText(/冻结后续会话/)).toBeNull();
});
