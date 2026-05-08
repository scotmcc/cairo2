import { randomUUID } from 'node:crypto';
import type {
  AgentSession,
  EventType,
  JsonValue,
  MessageRole,
  SessionEvent,
  SessionMessage,
  SessionStatus,
} from '../../shared/protocol.js';

type Listener = (event: SessionEvent) => void;

export class SessionStore {
  private readonly sessions = new Map<string, AgentSession>();
  private readonly listeners = new Map<string, Set<Listener>>();

  create(workspacePath: string): AgentSession {
    const now = new Date().toISOString();
    const session: AgentSession = {
      id: randomUUID(),
      createdAt: now,
      updatedAt: now,
      workspacePath,
      status: 'idle',
      messages: [],
      events: [],
    };
    this.sessions.set(session.id, session);
    this.addEvent(session.id, 'status', { status: 'idle' });
    return this.snapshot(session);
  }

  list(): AgentSession[] {
    return [...this.sessions.values()]
      .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
      .map((session) => this.snapshot(session));
  }

  get(id: string): AgentSession | undefined {
    const session = this.sessions.get(id);
    return session ? this.snapshot(session) : undefined;
  }

  mustGetMutable(id: string): AgentSession {
    const session = this.sessions.get(id);
    if (!session) throw new Error('session not found');
    return session;
  }

  addMessage(sessionId: string, role: MessageRole, content: string): SessionMessage {
    const session = this.mustGetMutable(sessionId);
    const message: SessionMessage = {
      id: randomUUID(),
      role,
      content,
      createdAt: new Date().toISOString(),
    };
    session.messages.push(message);
    session.updatedAt = message.createdAt;
    this.addEvent(sessionId, 'message', {
      id: message.id,
      role: message.role,
      content: message.content,
      createdAt: message.createdAt,
    });
    return message;
  }

  addEvent(sessionId: string, type: EventType, data: JsonValue): SessionEvent {
    const session = this.mustGetMutable(sessionId);
    const event: SessionEvent = {
      id: randomUUID(),
      type,
      data,
      createdAt: new Date().toISOString(),
    };
    session.events.push(event);
    session.updatedAt = event.createdAt;

    const listeners = this.listeners.get(sessionId);
    if (listeners) {
      for (const listener of listeners) listener(event);
    }
    return event;
  }

  setStatus(sessionId: string, status: SessionStatus): void {
    const session = this.mustGetMutable(sessionId);
    session.status = status;
    session.updatedAt = new Date().toISOString();
  }

  update(sessionId: string, updater: (session: AgentSession) => void): void {
    const session = this.mustGetMutable(sessionId);
    updater(session);
    session.updatedAt = new Date().toISOString();
  }

  remove(sessionId: string): boolean {
    const existed = this.sessions.delete(sessionId);
    this.listeners.delete(sessionId);
    return existed;
  }

  subscribe(sessionId: string, listener: Listener): () => void {
    if (!this.listeners.has(sessionId)) this.listeners.set(sessionId, new Set());
    this.listeners.get(sessionId)!.add(listener);
    return () => this.listeners.get(sessionId)?.delete(listener);
  }

  private snapshot(session: AgentSession): AgentSession {
    return {
      ...session,
      messages: session.messages.map((message) => ({ ...message })),
      events: session.events.map((event) => ({ ...event })),
    };
  }
}
