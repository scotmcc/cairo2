import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

export interface CairoRole {
  name: string;
  description: string;
  model: string;
  think: string;
  basePromptKey: string;
  tools: string;
}

export interface CairoConsiderAspect {
  name: string;
  traits: string;
  enabled: boolean;
  position: number;
}

export interface CairoDbSnapshot {
  dbPath: string;
  config: Record<string, string>;
  roles: CairoRole[];
  considerAspects: CairoConsiderAspect[];
}

export interface CairoSessionSummary {
  id: number;
  name: string;
  insight: string;
  role: string;
  cwd: string;
  lastActive: string;
}

export interface CairoSessionsResult {
  dbPath: string;
  sessions: CairoSessionSummary[];
}

type BridgePayload =
  | { action: 'snapshot'; dbPath: string }
  | { action: 'list_sessions'; dbPath: string }
  | { action: 'rename_session'; dbPath: string; id: number; name: string }
  | { action: 'delete_session'; dbPath: string; id: number }
  | { action: 'set_config'; dbPath: string; key: string; value: string }
  | { action: 'set_role'; dbPath: string; name: string; field: 'model' | 'think'; value: string }
  | { action: 'upsert_aspect'; dbPath: string; name: string; traits: string; enabled: boolean }
  | { action: 'delete_aspect'; dbPath: string; name: string }
  | { action: 'set_aspect_enabled'; dbPath: string; name: string; enabled: boolean };

const PYTHON_BRIDGE = String.raw`
import json
import os
import re
import sqlite3
import sys
import traceback
from datetime import datetime


def die(message):
    print(json.dumps({"ok": False, "error": message}))
    sys.exit(0)


def table_exists(conn, name):
    row = conn.execute(
        "SELECT name FROM sqlite_master WHERE type='table' AND name=?",
        (name,),
    ).fetchone()
    return row is not None


def columns(conn, table):
    return {row[1] for row in conn.execute("PRAGMA table_info(%s)" % table)}


def connect(db_path):
    if not db_path:
        die("No Cairo database path was provided.")
    if not os.path.exists(db_path):
        die("Cairo database not found at %s" % db_path)
    conn = sqlite3.connect(db_path, timeout=10)
    conn.execute("PRAGMA foreign_keys = ON")
    conn.row_factory = sqlite3.Row
    return conn


def read_snapshot(conn, db_path):
    config = {}
    if table_exists(conn, "config"):
        for row in conn.execute("SELECT key, value FROM config ORDER BY key"):
            config[str(row["key"])] = "" if row["value"] is None else str(row["value"])

    roles = []
    if table_exists(conn, "roles"):
        cols = columns(conn, "roles")
        think_expr = "think" if "think" in cols else "'' AS think"
        rows = conn.execute(
            "SELECT name, description, model, base_prompt_key, tools, %s FROM roles ORDER BY name" % think_expr
        )
        for row in rows:
            roles.append({
                "name": "" if row["name"] is None else str(row["name"]),
                "description": "" if row["description"] is None else str(row["description"]),
                "model": "" if row["model"] is None else str(row["model"]),
                "think": "" if row["think"] is None else str(row["think"]),
                "basePromptKey": "" if row["base_prompt_key"] is None else str(row["base_prompt_key"]),
                "tools": "" if row["tools"] is None else str(row["tools"]),
            })

    aspects = []
    if table_exists(conn, "consider_aspects"):
        rows = conn.execute(
            "SELECT name, traits, enabled, position FROM consider_aspects ORDER BY position ASC, name ASC"
        )
        for row in rows:
            aspects.append({
                "name": "" if row["name"] is None else str(row["name"]),
                "traits": "" if row["traits"] is None else str(row["traits"]),
                "enabled": bool(row["enabled"]),
                "position": int(row["position"] or 0),
            })

    return {
        "ok": True,
        "dbPath": db_path,
        "config": config,
        "roles": roles,
        "considerAspects": aspects,
    }


def clean_message(text):
    text = "" if text is None else str(text)
    text = re.sub(r"\`\`\`.*?\`\`\`", " ", text, flags=re.S)
    text = re.sub(r"@[^\s:]+:\s*", " ", text)
    text = re.sub(r"\s+", " ", text).strip()
    text = re.sub(r"^Selection from \`[^\`]+\`:\s*", "", text)
    if not text:
        return ""
    if len(text) <= 110:
        return text
    clipped = text[:107].rsplit(" ", 1)[0].strip()
    return (clipped or text[:107]).strip() + "..."


def session_insight(conn, session_id):
    if not table_exists(conn, "messages"):
        return "No messages yet"
    rows = conn.execute(
        """
        SELECT role, content
        FROM messages
        WHERE session_id = ? AND role IN ('user', 'assistant')
        ORDER BY created_at DESC, id DESC
        LIMIT 16
        """,
        (session_id,),
    ).fetchall()
    for row in rows:
        if row["role"] == "user":
            cleaned = clean_message(row["content"])
            if cleaned:
                return cleaned
    for row in rows:
        cleaned = clean_message(row["content"])
        if cleaned:
            return cleaned
    return "No messages yet"


def format_time(value):
    try:
        return datetime.fromtimestamp(int(value)).strftime("%Y-%m-%d %H:%M")
    except Exception:
        return ""


def list_sessions(conn, db_path):
    sessions = []
    if table_exists(conn, "sessions"):
        rows = conn.execute(
            """
            SELECT id, COALESCE(name, '') AS name, cwd, role, last_active
            FROM sessions
            ORDER BY last_active DESC, id DESC
            """
        )
        for row in rows:
            sid = int(row["id"])
            sessions.append({
                "id": sid,
                "name": "" if row["name"] is None else str(row["name"]),
                "insight": session_insight(conn, sid),
                "role": "" if row["role"] is None else str(row["role"]),
                "cwd": "" if row["cwd"] is None else str(row["cwd"]),
                "lastActive": format_time(row["last_active"]),
            })
    return {"ok": True, "dbPath": db_path, "sessions": sessions}


def set_config(conn, key, value):
    conn.execute(
        """
        INSERT INTO config(key, value, updated_at)
        VALUES(?, ?, CAST(strftime('%s','now') AS INTEGER))
        ON CONFLICT(key) DO UPDATE SET
            value = excluded.value,
            updated_at = excluded.updated_at
        """,
        (key, value),
    )


def set_role(conn, name, field, value):
    if field not in ("model", "think"):
        die("Unsupported role field: %s" % field)
    if not table_exists(conn, "roles"):
        die("Cairo roles table does not exist yet.")

    cols = columns(conn, "roles")
    if field == "think" and "think" not in cols:
        die("This Cairo database does not have role think overrides yet.")

    now = int(conn.execute("SELECT CAST(strftime('%s','now') AS INTEGER)").fetchone()[0])
    existing = conn.execute("SELECT name FROM roles WHERE name=?", (name,)).fetchone()
    if existing:
        conn.execute(
            "UPDATE roles SET %s = ?, updated_at = ? WHERE name = ?" % field,
            (value, now, name),
        )
        return

    if "think" in cols:
        conn.execute(
            """
            INSERT INTO roles(name, description, model, base_prompt_key, tools, think, created_at, updated_at)
            VALUES(?, '', ?, '', '[]', ?, ?, ?)
            """,
            (name, value if field == "model" else "", value if field == "think" else "", now, now),
        )
    else:
        conn.execute(
            """
            INSERT INTO roles(name, description, model, base_prompt_key, tools, created_at, updated_at)
            VALUES(?, '', ?, '', '[]', ?, ?)
            """,
            (name, value if field == "model" else "", now, now),
        )


def upsert_aspect(conn, name, traits, enabled):
    if not table_exists(conn, "consider_aspects"):
        die("Cairo consider_aspects table does not exist yet.")
    row = conn.execute(
        "SELECT COALESCE(MAX(position), 0) + 1 FROM consider_aspects"
    ).fetchone()
    position = int(row[0] or 1)
    conn.execute(
        """
        INSERT INTO consider_aspects(name, traits, enabled, position)
        VALUES(?, ?, ?, ?)
        ON CONFLICT(name) DO UPDATE SET
            traits = excluded.traits,
            enabled = excluded.enabled
        """,
        (name, traits, 1 if enabled else 0, position),
    )


def main():
    try:
        payload = json.load(sys.stdin)
        action = payload.get("action")
        db_path = payload.get("dbPath", "")
        conn = connect(db_path)
        try:
            if action == "snapshot":
                print(json.dumps(read_snapshot(conn, db_path)))
                return
            if action == "list_sessions":
                print(json.dumps(list_sessions(conn, db_path)))
                return

            with conn:
                if action == "rename_session":
                    name = str(payload.get("name", "")).strip()
                    value = None if name == "" else name
                    conn.execute(
                        "UPDATE sessions SET name = ? WHERE id = ?",
                        (value, int(payload.get("id") or 0)),
                    )
                elif action == "delete_session":
                    sid = int(payload.get("id") or 0)
                    conn.execute("DELETE FROM sessions WHERE id = ?", (sid,))
                elif action == "set_config":
                    set_config(conn, str(payload.get("key", "")), str(payload.get("value", "")))
                elif action == "set_role":
                    set_role(
                        conn,
                        str(payload.get("name", "")),
                        str(payload.get("field", "")),
                        str(payload.get("value", "")),
                    )
                elif action == "upsert_aspect":
                    name = str(payload.get("name", "")).strip()
                    if not name:
                        die("Aspect name is required.")
                    upsert_aspect(
                        conn,
                        name,
                        str(payload.get("traits", "")).strip(),
                        bool(payload.get("enabled", True)),
                    )
                elif action == "delete_aspect":
                    conn.execute("DELETE FROM consider_aspects WHERE name=?", (str(payload.get("name", "")),))
                elif action == "set_aspect_enabled":
                    conn.execute(
                        "UPDATE consider_aspects SET enabled=? WHERE name=?",
                        (1 if bool(payload.get("enabled", False)) else 0, str(payload.get("name", ""))),
                    )
                else:
                    die("Unknown Cairo DB action: %s" % action)

            if action in ("rename_session", "delete_session"):
                print(json.dumps(list_sessions(conn, db_path)))
            else:
                print(json.dumps(read_snapshot(conn, db_path)))
        finally:
            conn.close()
    except Exception as exc:
        print(json.dumps({
            "ok": False,
            "error": str(exc),
            "trace": traceback.format_exc(),
        }))


main()
`;

export function defaultCairoDataDir(env: NodeJS.ProcessEnv = process.env): string {
  if (env.CAIRO_DATA_DIR) return resolveHome(env.CAIRO_DATA_DIR);
  return path.join(os.homedir(), '.cairo');
}

export function defaultCairoDbPath(env: NodeJS.ProcessEnv = process.env): string {
  return path.join(defaultCairoDataDir(env), 'cairo.db');
}

export function cairoDbExists(dbPath: string): boolean {
  try {
    return fs.statSync(dbPath).isFile();
  } catch {
    return false;
  }
}

export async function readSnapshot(dbPath: string): Promise<CairoDbSnapshot> {
  return runBridge<CairoDbSnapshot>({ action: 'snapshot', dbPath });
}

export async function listSessions(dbPath: string): Promise<CairoSessionsResult> {
  return runBridge<CairoSessionsResult>({ action: 'list_sessions', dbPath });
}

export async function renameSession(dbPath: string, id: number, name: string): Promise<CairoSessionsResult> {
  return runBridge<CairoSessionsResult>({ action: 'rename_session', dbPath, id, name });
}

export async function deleteSession(dbPath: string, id: number): Promise<CairoSessionsResult> {
  return runBridge<CairoSessionsResult>({ action: 'delete_session', dbPath, id });
}

export async function setConfig(dbPath: string, key: string, value: string): Promise<CairoDbSnapshot> {
  return runBridge<CairoDbSnapshot>({ action: 'set_config', dbPath, key, value });
}

export async function setRole(
  dbPath: string,
  name: string,
  field: 'model' | 'think',
  value: string,
): Promise<CairoDbSnapshot> {
  return runBridge<CairoDbSnapshot>({ action: 'set_role', dbPath, name, field, value });
}

export async function upsertAspect(
  dbPath: string,
  name: string,
  traits: string,
  enabled: boolean,
): Promise<CairoDbSnapshot> {
  return runBridge<CairoDbSnapshot>({ action: 'upsert_aspect', dbPath, name, traits, enabled });
}

export async function deleteAspect(dbPath: string, name: string): Promise<CairoDbSnapshot> {
  return runBridge<CairoDbSnapshot>({ action: 'delete_aspect', dbPath, name });
}

export async function setAspectEnabled(
  dbPath: string,
  name: string,
  enabled: boolean,
): Promise<CairoDbSnapshot> {
  return runBridge<CairoDbSnapshot>({ action: 'set_aspect_enabled', dbPath, name, enabled });
}

function resolveHome(input: string): string {
  if (input === '~') return os.homedir();
  if (input.startsWith('~/') || input.startsWith(`~${path.sep}`)) {
    return path.join(os.homedir(), input.slice(2));
  }
  return input;
}

async function runBridge<T>(payload: BridgePayload): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const child = spawn('python3', ['-c', PYTHON_BRIDGE], {
      stdio: ['pipe', 'pipe', 'pipe'],
    });

    let stdout = '';
    let stderr = '';
    let settled = false;
    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      child.kill('SIGKILL');
      reject(new Error('Timed out while accessing the Cairo database.'));
    }, 10_000);

    child.stdout.on('data', (chunk: Buffer) => {
      stdout += chunk.toString();
    });
    child.stderr.on('data', (chunk: Buffer) => {
      stderr += chunk.toString();
    });
    child.on('error', (err) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      reject(new Error(`Unable to run python3 for Cairo database access: ${err.message}`));
    });
    child.on('close', (code) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (code !== 0) {
        reject(new Error(stderr.trim() || `Cairo database bridge exited with code ${code}`));
        return;
      }
      try {
        const parsed = JSON.parse(stdout.trim() || '{}') as { ok?: boolean; error?: string } & T;
        if (!parsed.ok) {
          reject(new Error(parsed.error || 'Cairo database bridge failed.'));
          return;
        }
        resolve(parsed as T);
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        reject(new Error(`Could not parse Cairo database bridge output: ${message}`));
      }
    });

    child.stdin.end(JSON.stringify(payload));
  });
}
