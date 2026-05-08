export interface OllamaModelsResult {
  models: string[];
  source: string;
  error?: string;
}

export async function fetchOllamaModels(baseUrl: string): Promise<OllamaModelsResult> {
  const trimmed = (baseUrl || '').replace(/\/+$/, '');
  if (!trimmed) return { models: [], source: '', error: 'ollama_url is empty' };

  const url = `${trimmed}/api/tags`;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 8000);
  try {
    const response = await fetch(url, { signal: controller.signal });
    if (!response.ok) {
      return { models: [], source: trimmed, error: `${response.status} ${response.statusText}` };
    }
    const body = (await response.json()) as { models?: Array<{ name?: string }> };
    const names = (body.models || [])
      .map((m) => (m && typeof m.name === 'string' ? m.name : ''))
      .filter((name): name is string => name.length > 0)
      .sort((a, b) => a.localeCompare(b));
    return { models: names, source: trimmed };
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    return { models: [], source: trimmed, error: message };
  } finally {
    clearTimeout(timer);
  }
}
