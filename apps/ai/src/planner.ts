import { createOpenAI } from "@ai-sdk/openai";
import { generateText, Output } from "ai";
import { z } from "zod";

// PlannedTaskSchema is the Zod mirror of the PlannedTask protobuf. The
// prompt below restates every constraint in plain English so the model
// has two places telling it the rules (schema + prompt). Zod rejects
// responses the model ignores.
// A discriminated union on `kind` translates into JSON Schema as a
// oneOf with per-branch required fields. That forces the structured
// output endpoint to emit a spec with the right shape for each kind —
// `record(string, any)` would accept {} and the model would happily
// return {}.
const nameSchema = z
  .string()
  .min(1)
  .regex(/^[a-z0-9_]+$/, "lowercase letters, digits, and underscores only");

const FetchTaskSchema = z.object({
  name: nameSchema,
  kind: z.literal("fetch"),
  spec: z.object({
    url: z.string().url().describe("Reachable HTTP GET endpoint."),
  }),
  depends_on: z.array(z.string()).default([]),
});

const TransformTaskSchema = z.object({
  name: nameSchema,
  kind: z.literal("transform"),
  spec: z.object({
    op: z
      .string()
      .min(1)
      .describe(
        "Operation name, e.g. extract_json_field, concat, filter, format_markdown_list.",
      ),
    // Extra per-op fields (field, where, ...) are carried as JSON inside
    // the spec. We keep the structured part minimal so the model can add
    // what each op needs.
    params: z
      .record(z.string(), z.any())
      .default({})
      .describe("Op-specific parameters, e.g. { field: 'revenue' }."),
  }),
  depends_on: z.array(z.string()).default([]),
});

const AITaskSchema = z.object({
  name: nameSchema,
  kind: z.literal("ai"),
  spec: z.object({
    instruction: z
      .string()
      .min(1)
      .describe(
        "One-sentence instruction for the AI worker describing what to do with upstream results.",
      ),
  }),
  depends_on: z.array(z.string()).default([]),
});

const PlannedTaskSchema = z.discriminatedUnion("kind", [
  FetchTaskSchema,
  TransformTaskSchema,
  AITaskSchema,
]);

const PlanSchema = z.object({
  tasks: z.array(PlannedTaskSchema).min(1).max(20),
});

export type PlannedTask = z.infer<typeof PlannedTaskSchema>;
export type Plan = z.infer<typeof PlanSchema>;

const SYSTEM_PROMPT = `You are a task planner for a small agent runtime. Given a natural-language goal, decompose it into a DAG of at most 20 concrete tasks.

Every task MUST have a non-empty spec object. A task with an empty spec is invalid — the runtime will not be able to execute it. Be specific and concrete.

Task kinds and their required spec shapes:

- kind "fetch": HTTP GET. spec MUST be {"url": "https://..."}. The URL must be a real, reachable endpoint — Wikipedia, SEC EDGAR, public data APIs, the company's press page, etc. Do NOT invent URLs; if unsure, reach for Wikipedia.

- kind "transform": deterministic pure-Go operation on upstream task results. spec MUST include {"op": "..."} and whatever parameters that op needs. Examples:
    {"op": "extract_json_field", "field": "financials.revenue"}
    {"op": "concat"}
    {"op": "filter", "where": "year == 2025"}
    {"op": "format_markdown_list"}

- kind "ai": focused LLM sub-task. spec MUST be {"instruction": "..."} describing in one sentence what the AI should do with its upstream results.

Other rules:
- "name" is a snake_case id, unique within the plan (e.g. "fetch_catl_wikipedia").
- "depends_on" lists names of upstream tasks. Empty for root tasks.
- DAG invariants: no cycles, every name in depends_on must refer to another task in the plan.
- Prefer shallow, parallel structures over deep chains. Keep plans to 2-8 tasks.
- Do NOT include tasks that cannot be executed by fetch/transform/ai.

Return the plan with full specs. No prose, no commentary.`;

// OpenRouter is OpenAI-API-compatible, so the vanilla @ai-sdk/openai
// provider works pointed at their base URL. The `name: "openrouter"`
// gives the ai-sdk telemetry a better label.
const openrouter = createOpenAI({
  baseURL: "https://openrouter.ai/api/v1",
  apiKey: process.env.OPENROUTER_API_KEY,
  name: "openrouter",
});

// Model id uses OpenRouter's "provider/model" naming. Override via env
// to try different models without a code change.
const MODEL_ID =
  process.env.OPENROUTER_MODEL ?? "anthropic/claude-sonnet-4.5";

// planGoal calls the LLM with structured output. Throws if the API key is
// missing or if the model returns something the schema rejects.
export async function planGoal(goal: string): Promise<Plan> {
  if (!goal.trim()) {
    throw new Error("goal is empty");
  }
  if (!process.env.OPENROUTER_API_KEY) {
    throw new Error(
      "OPENROUTER_API_KEY is not set; planner cannot call the model",
    );
  }

  const { output } = await generateText({
    model: openrouter(MODEL_ID),
    system: SYSTEM_PROMPT,
    prompt: `Goal: ${goal}`,
    output: Output.object({
      schema: PlanSchema,
      name: "TaskPlan",
      description: "A validated DAG of tasks that accomplish the goal.",
    }),
  });

  if (!output) {
    throw new Error("planner returned no structured output");
  }

  // Defensive re-validation: the schema already ran, but Output typing
  // comes through as possibly-undefined and we want the runtime guarantee
  // to match what we hand back over the wire.
  return PlanSchema.parse(output);
}
