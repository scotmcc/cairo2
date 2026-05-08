import fs from 'node:fs';
import path from 'node:path';
import type { WorkspaceSummary } from '../../shared/protocol.js';

export function validateWorkspacePath(input: string | undefined, roots: string[]): string {
  const candidate = path.resolve(input || roots[0] || process.cwd());
  const stat = statDirectory(candidate);
  if (!stat.ok) {
    throw new WorkspaceValidationError(stat.message);
  }

  const realCandidate = fs.realpathSync.native(candidate);
  const realRoots = roots.map((root) => {
    try {
      return fs.realpathSync.native(path.resolve(root));
    } catch {
      return path.resolve(root);
    }
  });

  if (!realRoots.some((root) => isPathWithin(realCandidate, root))) {
    throw new WorkspaceValidationError('workspace path is outside CAIRO_WORKSPACE_ROOTS');
  }

  return realCandidate;
}

export function listWorkspaces(roots: string[]): WorkspaceSummary[] {
  return roots.map((root) => {
    const resolved = path.resolve(root);
    let real = resolved;
    try {
      real = fs.realpathSync.native(resolved);
    } catch {
      // The validator rejects missing paths; list still shows configured roots.
    }
    return { path: real, name: path.basename(real) || real };
  });
}

export class WorkspaceValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'WorkspaceValidationError';
  }
}

function isPathWithin(candidate: string, root: string): boolean {
  const relative = path.relative(root, candidate);
  return relative === '' || (relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative));
}

function statDirectory(candidate: string): { ok: true } | { ok: false; message: string } {
  try {
    const stat = fs.statSync(candidate);
    if (!stat.isDirectory()) {
      return { ok: false, message: 'workspace path must be a directory' };
    }
    return { ok: true };
  } catch {
    return { ok: false, message: 'workspace path does not exist' };
  }
}
