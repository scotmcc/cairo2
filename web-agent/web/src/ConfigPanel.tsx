import { Loader2, Plus, RefreshCw, Save, Trash2 } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import type { CairoModelsResponse, CairoSnapshot } from '../../shared/protocol.js';
import {
  deleteCairoAspect,
  getCairoModels,
  getCairoSnapshot,
  setCairoAspectEnabled,
  setCairoConfig,
  setCairoRole,
  upsertCairoAspect,
} from './api.js';
import { CONFIG_SECTIONS, parseRoleKey, THINK_OPTIONS, type ConfigFieldDef } from './configSections.js';

interface ConfigPanelProps {
  onSnapshot?: (snapshot: CairoSnapshot) => void;
}

export function ConfigPanel({ onSnapshot }: ConfigPanelProps) {
  const [snapshot, setSnapshot] = useState<CairoSnapshot | null>(null);
  const [models, setModels] = useState<CairoModelsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState<string | null>(null);
  const [draft, setDraft] = useState<Record<string, string>>({});

  useEffect(() => {
    void refresh();
  }, []);

  async function refresh() {
    setLoading(true);
    setError('');
    try {
      const [snap, modelList] = await Promise.all([getCairoSnapshot(), getCairoModels()]);
      setSnapshot(snap);
      setModels(modelList);
      setDraft({});
      onSnapshot?.(snap);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }

  async function refreshModels() {
    try {
      setModels(await getCairoModels());
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  function handleEdit(key: string, value: string) {
    setDraft((current) => ({ ...current, [key]: value }));
  }

  async function handleSave(field: ConfigFieldDef) {
    if (!snapshot) return;
    setSaving(field.key);
    setError('');
    try {
      const value = currentValue(snapshot, draft, field);
      const role = parseRoleKey(field.key);
      let updated: CairoSnapshot;
      if (role) {
        updated = await setCairoRole(role.name, role.field, value);
      } else {
        updated = await setCairoConfig(field.key, value);
      }
      setSnapshot(updated);
      setDraft((current) => {
        const next = { ...current };
        delete next[field.key];
        return next;
      });
      onSnapshot?.(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(null);
    }
  }

  if (!snapshot && loading) {
    return (
      <section className="configPanel">
        <header className="configHeader">
          <Loader2 className="spin" size={18} />
          <span>Loading Cairo config…</span>
        </header>
      </section>
    );
  }

  if (!snapshot) {
    return (
      <section className="configPanel">
        <header className="configHeader">
          <strong>Configuration</strong>
          <button onClick={refresh}>
            <RefreshCw size={14} /> Retry
          </button>
        </header>
        {error && <div className="errorBanner">{error}</div>}
      </section>
    );
  }

  return (
    <section className="configPanel">
      <header className="configHeader">
        <div>
          <strong>Configuration</strong>
          <small>{snapshot.dbPath}</small>
        </div>
        <div className="configHeaderActions">
          <button onClick={refreshModels} title="Refresh model list">
            <RefreshCw size={14} /> Models
          </button>
          <button onClick={refresh} title="Refresh from cairo.db">
            <RefreshCw size={14} /> Reload
          </button>
        </div>
      </header>
      {error && <div className="errorBanner">{error}</div>}
      {models?.error && (
        <div className="warningBanner">Model list unavailable: {models.error}</div>
      )}
      <datalist id="cairo-models">
        {(models?.models || []).map((model) => (
          <option key={model} value={model} />
        ))}
      </datalist>

      <div className="configSections">
        {CONFIG_SECTIONS.map((section) => (
          <ConfigSection
            key={section.title}
            title={section.title}
            tagline={section.tagline}
            fields={section.fields}
            snapshot={snapshot}
            draft={draft}
            saving={saving}
            modelOptions={models?.models || []}
            onEdit={handleEdit}
            onSave={handleSave}
          />
        ))}

        <ConsiderAspectsSection snapshot={snapshot} setSnapshot={setSnapshot} setError={setError} />
      </div>
    </section>
  );
}

interface SectionProps {
  title: string;
  tagline: string;
  fields: ConfigFieldDef[];
  snapshot: CairoSnapshot;
  draft: Record<string, string>;
  saving: string | null;
  modelOptions: string[];
  onEdit: (key: string, value: string) => void;
  onSave: (field: ConfigFieldDef) => void;
}

function ConfigSection(props: SectionProps) {
  const { title, tagline, fields, snapshot, draft, saving, modelOptions, onEdit, onSave } = props;
  return (
    <section className="configSection">
      <header>
        <h3>{title}</h3>
        <p>{tagline}</p>
      </header>
      <div className="configFields">
        {fields.map((field) => {
          const value = currentValue(snapshot, draft, field);
          const baseline = baselineValue(snapshot, field);
          const dirty = value !== baseline;
          const isSaving = saving === field.key;
          return (
            <div className="configField" key={field.key}>
              <label htmlFor={`field-${field.key}`}>{field.label}</label>
              <FieldInput
                field={field}
                value={value}
                modelOptions={modelOptions}
                onChange={(next) => onEdit(field.key, next)}
              />
              <button
                disabled={!dirty || isSaving}
                onClick={() => onSave(field)}
                title={dirty ? 'Save' : 'No changes'}
              >
                {isSaving ? <Loader2 className="spin" size={14} /> : <Save size={14} />}
              </button>
            </div>
          );
        })}
      </div>
    </section>
  );
}

interface FieldInputProps {
  field: ConfigFieldDef;
  value: string;
  modelOptions: string[];
  onChange: (next: string) => void;
}

function FieldInput({ field, value, modelOptions, onChange }: FieldInputProps) {
  const id = `field-${field.key}`;
  if (field.kind === 'boolean') {
    return (
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">(unset)</option>
        <option value="true">true</option>
        <option value="false">false</option>
      </select>
    );
  }
  if (field.kind === 'number') {
    return (
      <input
        id={id}
        type="number"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  if (field.kind === 'select') {
    return (
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
        {(field.options || []).map((opt) => (
          <option key={opt} value={opt}>
            {opt || '(unset)'}
          </option>
        ))}
      </select>
    );
  }
  if (field.kind === 'model' || field.kind === 'role-model') {
    return (
      <input
        id={id}
        list="cairo-models"
        value={value}
        placeholder={modelOptions[0] || 'model name'}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  if (field.kind === 'role-think') {
    return (
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
        {THINK_OPTIONS.map((opt) => (
          <option key={opt} value={opt}>
            {opt || '(inherit)'}
          </option>
        ))}
      </select>
    );
  }
  if (field.kind === 'textarea') {
    return (
      <textarea id={id} value={value} onChange={(e) => onChange(e.target.value)} rows={4} />
    );
  }
  return <input id={id} value={value} onChange={(e) => onChange(e.target.value)} />;
}

interface AspectsProps {
  snapshot: CairoSnapshot;
  setSnapshot: (snap: CairoSnapshot) => void;
  setError: (msg: string) => void;
}

function ConsiderAspectsSection({ snapshot, setSnapshot, setError }: AspectsProps) {
  const [draftName, setDraftName] = useState('');
  const [draftTraits, setDraftTraits] = useState('');
  const [busy, setBusy] = useState(false);
  const sorted = useMemo(
    () => [...snapshot.considerAspects].sort((a, b) => a.position - b.position),
    [snapshot.considerAspects],
  );

  async function add() {
    const name = draftName.trim();
    if (!name) return;
    setBusy(true);
    try {
      const next = await upsertCairoAspect(name, draftTraits.trim(), true);
      setSnapshot(next);
      setDraftName('');
      setDraftTraits('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function toggle(name: string, enabled: boolean) {
    try {
      setSnapshot(await setCairoAspectEnabled(name, enabled));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function remove(name: string) {
    if (!confirm(`Delete consider aspect "${name}"?`)) return;
    try {
      setSnapshot(await deleteCairoAspect(name));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function saveTraits(name: string, traits: string) {
    try {
      setSnapshot(await upsertCairoAspect(name, traits, true));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <section className="configSection">
      <header>
        <h3>Consider Aspects</h3>
        <p>Personas Cairo considers before each turn.</p>
      </header>

      {sorted.length === 0 && <p className="muted">No aspects defined.</p>}

      <div className="aspects">
        {sorted.map((aspect) => (
          <AspectRow
            key={aspect.name}
            name={aspect.name}
            enabled={aspect.enabled}
            initialTraits={aspect.traits}
            onToggle={(enabled) => toggle(aspect.name, enabled)}
            onSave={(traits) => saveTraits(aspect.name, traits)}
            onDelete={() => remove(aspect.name)}
          />
        ))}
      </div>

      <div className="aspectAdd">
        <input
          placeholder="aspect name"
          value={draftName}
          onChange={(e) => setDraftName(e.target.value)}
        />
        <input
          placeholder="traits"
          value={draftTraits}
          onChange={(e) => setDraftTraits(e.target.value)}
        />
        <button disabled={busy || !draftName.trim()} onClick={add}>
          <Plus size={14} /> Add
        </button>
      </div>
    </section>
  );
}

interface AspectRowProps {
  name: string;
  enabled: boolean;
  initialTraits: string;
  onToggle: (enabled: boolean) => void;
  onSave: (traits: string) => void;
  onDelete: () => void;
}

function AspectRow({ name, enabled, initialTraits, onToggle, onSave, onDelete }: AspectRowProps) {
  const [draft, setDraft] = useState(initialTraits);
  useEffect(() => {
    setDraft(initialTraits);
  }, [initialTraits]);
  const dirty = draft !== initialTraits;

  return (
    <div className={`aspect ${enabled ? '' : 'disabled'}`}>
      <header>
        <strong>{name}</strong>
        <label>
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => onToggle(e.target.checked)}
          />
          enabled
        </label>
        <button className="iconButton" onClick={onDelete} title="Delete aspect">
          <Trash2 size={14} />
        </button>
      </header>
      <textarea value={draft} onChange={(e) => setDraft(e.target.value)} rows={3} />
      <div className="aspectActions">
        <button disabled={!dirty} onClick={() => onSave(draft)}>
          <Save size={14} /> Save
        </button>
      </div>
    </div>
  );
}

function baselineValue(snapshot: CairoSnapshot, field: ConfigFieldDef): string {
  const role = parseRoleKey(field.key);
  if (role) {
    const found = snapshot.roles.find((r) => r.name === role.name);
    if (!found) return '';
    return role.field === 'model' ? found.model : found.think;
  }
  return snapshot.config[field.key] ?? '';
}

function currentValue(
  snapshot: CairoSnapshot,
  draft: Record<string, string>,
  field: ConfigFieldDef,
): string {
  if (field.key in draft) return draft[field.key];
  return baselineValue(snapshot, field);
}
