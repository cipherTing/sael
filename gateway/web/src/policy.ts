export type Action = "allow" | "block";
export type Match = "any" | "all";
export type Question = { key: string; type: "noul" | "score"; max: number };
export type Condition = { question: string; threshold: number };
export type Scene = {
  id: string;
  name: string;
  note?: string;
  conditions: Condition[];
  match: Match;
  action: Action;
  enabled?: boolean;
  endpoints?: string[];
  models?: string[];
};
export type Policy = {
  trusted_key_idle_days?: number;
  enabled: boolean;
  version: number;
  scenes: Scene[];
  preview_chars: number | null;
  retention_days: number | null;
  session_block_enabled?: boolean;
  session_block_ttl_seconds?: number;
};
export type PolicyResponse = Policy & { questions: Question[] };
