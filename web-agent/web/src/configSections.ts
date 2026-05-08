export interface ConfigFieldDef {
  key: string;
  label: string;
  kind?: 'text' | 'number' | 'boolean' | 'model' | 'select' | 'textarea' | 'role-model' | 'role-think';
  options?: string[];
  hint?: string;
}

export interface ConfigSectionDef {
  title: string;
  tagline: string;
  fields: ConfigFieldDef[];
}

export const CONFIG_SECTIONS: ConfigSectionDef[] = [
  {
    title: 'Identity',
    tagline: 'Who Cairo is and who you are to it.',
    fields: [
      { key: 'ai_name', label: 'ai_name' },
      { key: 'user_name', label: 'user_name' },
    ],
  },
  {
    title: 'LLM Backend',
    tagline: 'Endpoint, primary chat model, embeddings, and thinking defaults.',
    fields: [
      { key: 'ollama_url', label: 'ollama_url' },
      { key: 'llm_api_key', label: 'llm_api_key' },
      { key: 'model', label: 'model', kind: 'model' },
      { key: 'embed_model', label: 'embed_model', kind: 'model' },
      { key: 'embed_model_code', label: 'embed_model_code', kind: 'model' },
      { key: 'think', label: 'think', kind: 'boolean' },
      { key: 'think_budget', label: 'think_budget', kind: 'number' },
    ],
  },
  {
    title: 'Voice',
    tagline: 'Kokoro TTS endpoint and voice blend.',
    fields: [
      { key: 'kokoro_url', label: 'kokoro_url' },
      { key: 'kokoro_voice', label: 'kokoro_voice' },
    ],
  },
  {
    title: 'Memory',
    tagline: 'Recall limits, summarization, and embedding-space thresholds.',
    fields: [
      { key: 'memory_limit', label: 'memory_limit', kind: 'number' },
      { key: 'summary_model', label: 'summary_model', kind: 'model' },
      { key: 'summary_threshold', label: 'summary_threshold', kind: 'number' },
      { key: 'summary_batch_size', label: 'summary_batch_size', kind: 'number' },
      { key: 'summary_context', label: 'summary_context', kind: 'number' },
      { key: 'memory_dedup_threshold', label: 'dedup_threshold', kind: 'number' },
      { key: 'learn_max_chunk_tokens', label: 'learn_max_chunk_tokens', kind: 'number' },
      { key: 'summary_token_threshold', label: 'token_pressure_thresh', kind: 'number' },
    ],
  },
  {
    title: 'Display',
    tagline: 'Rendering preferences used by Cairo terminal views.',
    fields: [
      {
        key: 'glamour_style',
        label: 'glamour_style',
        kind: 'select',
        options: ['', 'dark', 'light', 'notty', 'auto'],
      },
    ],
  },
  {
    title: 'Limits',
    tagline: 'Hard limits on turn and run size.',
    fields: [{ key: 'max_turns', label: 'max_turns', kind: 'number' }],
  },
  {
    title: 'Search',
    tagline: 'External search backend configuration.',
    fields: [{ key: 'searxng_url', label: 'searxng_url' }],
  },
  {
    title: 'Safety',
    tagline: 'Permissioning and unsafe-mode toggles.',
    fields: [{ key: 'unsafe_mode', label: 'unsafe_mode', kind: 'boolean' }],
  },
  {
    title: 'Consider',
    tagline: 'Pre-turn inner dialogue, aspect models, and prompt body.',
    fields: [
      { key: 'consider.enabled', label: 'consider.enabled', kind: 'boolean' },
      { key: 'consider.model', label: 'consider.model', kind: 'model' },
      { key: 'consider.summary_model', label: 'consider.summary_model', kind: 'model' },
      { key: 'consider.include_user_history', label: 'include_user_history', kind: 'boolean' },
      { key: 'consider.template', label: 'aspect_body', kind: 'textarea' },
    ],
  },
  {
    title: 'Roles',
    tagline: 'Per-role model and thinking overrides. Empty inherits global.',
    fields: [
      { key: 'role:orchestrator:model', label: 'orchestrator.model', kind: 'role-model' },
      { key: 'role:orchestrator:think', label: 'orchestrator.think', kind: 'role-think' },
      { key: 'role:researcher:model', label: 'researcher.model', kind: 'role-model' },
      { key: 'role:researcher:think', label: 'researcher.think', kind: 'role-think' },
      { key: 'role:planner:model', label: 'planner.model', kind: 'role-model' },
      { key: 'role:planner:think', label: 'planner.think', kind: 'role-think' },
      { key: 'role:coder:model', label: 'coder.model', kind: 'role-model' },
      { key: 'role:coder:think', label: 'coder.think', kind: 'role-think' },
      { key: 'role:reviewer:model', label: 'reviewer.model', kind: 'role-model' },
      { key: 'role:reviewer:think', label: 'reviewer.think', kind: 'role-think' },
      { key: 'role:thinking_partner:model', label: 'thinking_partner.model', kind: 'role-model' },
      { key: 'role:thinking_partner:think', label: 'thinking_partner.think', kind: 'role-think' },
      { key: 'role:dream:model', label: 'dream.model', kind: 'role-model' },
      { key: 'role:dream:think', label: 'dream.think', kind: 'role-think' },
    ],
  },
];

export const THINK_OPTIONS = ['', 'on', 'off', 'low', 'medium', 'high'];

export function parseRoleKey(key: string): { name: string; field: 'model' | 'think' } | null {
  const parts = key.split(':');
  if (parts.length !== 3 || parts[0] !== 'role') return null;
  if (parts[2] !== 'model' && parts[2] !== 'think') return null;
  return { name: parts[1], field: parts[2] };
}
