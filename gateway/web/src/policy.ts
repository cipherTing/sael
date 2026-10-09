export type Action = "allow" | "block";
export type Match = "any" | "all";
export type ReviewMode = "blocking" | "non_blocking";
export type Question = { key: string; type: "noul" | "score"; max: number };
export type Condition = {
  question: string;
  threshold: number;
  record_only?: boolean;
};
export type Scene = {
  needs_endpoint_selection?: boolean;
  id: string;
  name: string;
  note?: string;
  conditions: Condition[];
  match: Match;
  action: Action;
  review_mode?: ReviewMode;
  enabled?: boolean;
  endpoints?: string[];
  models?: string[];
  session_block_enabled?: boolean;
  session_block_ttl_seconds?: number;
};
export type Policy = {
  trusted_key_idle_days?: number;
  enabled: boolean;
  review_api_enabled?: boolean;
  scenes: Scene[];
  preview_chars: number | null;
  retention_days: number | null;
  block_message?: string;
};
export type PolicyResponse = Policy & { questions: Question[] };

export type PolicyPatch = Partial<Policy>;

export function sceneReviewMode(scene: Scene): ReviewMode {
  return (
    scene.review_mode ||
    (scene.action === "allow" ? "non_blocking" : "blocking")
  );
}

export function normalizeScene(input: Scene): Scene {
  const scene = structuredClone(input);
  if (!scene.review_mode) {
    scene.review_mode = sceneReviewMode(scene);
    if (scene.action === "allow") {
      scene.conditions = scene.conditions.map((c) => ({
        ...c,
        record_only: true,
      }));
      scene.session_block_enabled = false;
    }
  }
  return scene;
}

export function sceneCanReject(scene: Scene): boolean {
  return (
    sceneReviewMode(scene) === "blocking" &&
    (scene.match === "all"
      ? scene.action === "block"
      : scene.conditions.some((c) => !c.record_only))
  );
}

export function changeScene(scene: Scene, patch: Partial<Scene>): Scene {
  const next = { ...scene, ...patch };
  if (patch.match && patch.match !== scene.match) {
    if (patch.match === "all")
      next.action = scene.conditions.every((c) => c.record_only)
        ? "allow"
        : "block";
    else
      next.conditions = scene.conditions.map((c) => ({
        ...c,
        record_only: scene.action === "allow",
      }));
  }
  if (sceneReviewMode(next) === "non_blocking") {
    next.action = "allow";
    next.conditions = next.conditions.map((c) => ({ ...c, record_only: true }));
  }
  if (next.match === "any")
    next.action = next.conditions.some((c) => !c.record_only)
      ? "block"
      : "allow";
  if (!sceneCanReject(next)) next.session_block_enabled = false;
  return next;
}
