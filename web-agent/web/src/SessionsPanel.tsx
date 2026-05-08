import { Loader2, Pencil, RefreshCw, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { CairoSessionSummary } from '../../shared/protocol.js';
import { deleteCairoDbSession, getCairoSessions, renameCairoDbSession } from './api.js';

export function SessionsPanel() {
  const [sessions, setSessions] = useState<CairoSessionSummary[]>([]);
  const [dbPath, setDbPath] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [renaming, setRenaming] = useState<number | null>(null);
  const [draftName, setDraftName] = useState('');

  useEffect(() => {
    void refresh();
  }, []);

  async function refresh() {
    setLoading(true);
    setError('');
    try {
      const result = await getCairoSessions();
      setSessions(result.sessions);
      setDbPath(result.dbPath);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }

  async function commitRename(id: number) {
    try {
      const result = await renameCairoDbSession(id, draftName);
      setSessions(result.sessions);
      setRenaming(null);
      setDraftName('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function remove(id: number, name: string) {
    if (!confirm(`Delete Cairo session "${name || id}"? This removes the session from cairo.db.`)) return;
    try {
      const result = await deleteCairoDbSession(id);
      setSessions(result.sessions);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <section className="sessionsPanel">
      <header className="configHeader">
        <div>
          <strong>Cairo Sessions</strong>
          <small>{dbPath || 'cairo.db'}</small>
        </div>
        <button onClick={refresh} disabled={loading}>
          {loading ? <Loader2 className="spin" size={14} /> : <RefreshCw size={14} />}
          Reload
        </button>
      </header>
      {error && <div className="errorBanner">{error}</div>}

      {sessions.length === 0 && !loading && <p className="muted">No sessions yet.</p>}

      <ul className="cairoSessions">
        {sessions.map((session) => (
          <li key={session.id}>
            <header>
              {renaming === session.id ? (
                <input
                  value={draftName}
                  autoFocus
                  onChange={(e) => setDraftName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') void commitRename(session.id);
                    if (e.key === 'Escape') setRenaming(null);
                  }}
                  onBlur={() => void commitRename(session.id)}
                />
              ) : (
                <strong>{session.name || `Session ${session.id}`}</strong>
              )}
              <span className="badge">{session.role}</span>
              <button
                className="iconButton"
                title="Rename"
                onClick={() => {
                  setDraftName(session.name);
                  setRenaming(session.id);
                }}
              >
                <Pencil size={14} />
              </button>
              <button
                className="iconButton"
                title="Delete"
                onClick={() => remove(session.id, session.name)}
              >
                <Trash2 size={14} />
              </button>
            </header>
            <p className="muted">{session.insight}</p>
            <footer>
              <code>{session.cwd}</code>
              <span>{session.lastActive}</span>
            </footer>
          </li>
        ))}
      </ul>
    </section>
  );
}
