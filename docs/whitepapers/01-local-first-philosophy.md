# Local-First Philosophy
### Cairo Whitepaper 01

*May 2026*

---

There is a version of the AI assistant story that goes like this: you open a browser tab, type a question, get an answer. The experience is smooth, the model is impressive, and you close the tab and go back to work. A moment later the conversation is effectively gone — not deleted, exactly, but stranded in a vendor's database with no particular relationship to anything else you're doing. The next time you open the tab, the model has no idea who you are.

This is not a critique of that story. It describes something real and useful. But it is not the only story, and for serious engineering work, it may not be the right one.

Cairo is built on a different premise: that the agent loop should belong to you, run on your hardware, persist its state in a file you control, and integrate with your actual systems rather than with a platform you're renting access to. That premise is what "local-first" means here. This paper explains what it entails, why we think the trade-offs are worth making, and where it honestly falls short.

---

## The Hosted-AI Status Quo

When you use a hosted AI assistant — ChatGPT, Claude.ai, Cursor, Copilot Chat, take your pick — several things are true simultaneously, and it's worth naming them clearly rather than either ignoring them or sensationalizing them.

**Your context evaporates between sessions.** The model on the other end has no memory of what you worked on last week, what your codebase looks like, what your preferences are, or what you told it about the problem two conversations ago. Projects features and memory plugins exist on some platforms, and they help at the margins. But they're add-ons to a fundamentally stateless architecture, not a reconception of it. Each new conversation starts fresh, and "fresh" means the model knows nothing specific about you.

**Your prompts and interactions may inform future model training.** This varies by vendor and by which tier you're on, and the policies are honest about it if you read them. It's not sinister — training on real-world usage is how models improve. But it means the things you type into that box do not stay in that box in any simple sense.

**Your data lives in someone else's RAM during inference.** Whatever code you paste, whatever architectural problem you describe, whatever customer data accidentally leaks into a prompt — it traverses a network, gets processed on infrastructure you don't operate, and is subject to that vendor's security posture. Again, the major vendors have serious security teams and this is often fine. But "often fine" and "you control it" are different things, and in regulated industries or sensitive contexts, the distinction matters enormously.

**The context window is the outer boundary.** Every message you send costs tokens. Long conversations, large codebases, extensive system prompts — these push you toward limits, and the model's effective memory of earlier turns degrades as the window fills. Your project's history doesn't accumulate; it just grows more expensive to carry.

None of this is a scandal. These are the natural properties of a system that is centrally hosted, inference-on-demand, and engineered for mass-market simplicity. The trade you're making is: low friction in exchange for ownership. For a lot of tasks, that's a perfectly reasonable trade.

Cairo is for the cases where it isn't.

---

## What Local-First Actually Means

"Local-first" gets used as a marketing adjective for things that are only partly true. Let's be specific about what it means in cairo's case.

**Single SQLite file, single binary.** Everything the agent knows about you — your memories, your identity, your roles, your custom tools, your session history, your soul prompt, your facts, your indexed projects — lives in a single SQLite file at `~/.cairo/cairo.db`. The Go binary at `/usr/local/bin/cairo` (or `./bin/cairo` in the dev tree) is stateless with respect to identity. It reads and writes the database, but it carries nothing of yours. Burn the binary, reinstall it, and the being is unchanged. The database is the self.

This is not a metaphor. Look at `internal/store/schema/schema.go:10-47` — the schema defines tables for `config`, `prompt_parts`, `roles`, `memories`, `sessions`, `messages`, and more. Look at `internal/store/sqliteopen/db.go:23-50` — the `DB` struct owns `Config`, `Sessions`, `Messages`, `Memories`, `Roles`, `Prompts`, `Tools`, `Skills`, `Jobs`, `Summaries`, `Facts`, and a dozen more sub-types. Everything that makes your instance of cairo *yours* is a query away from that file.

**Your model endpoint, your choice.** Cairo speaks OpenAI-compatible HTTP — it sends requests to whatever URL you configure. Ollama running locally on your laptop. LiteLLM proxying a fleet of self-hosted models. vLLM on a GPU cluster your company operates. You configure the endpoint (`ollama_url` in the config store) and the key (`llm_api_key`), and cairo talks to it. The LLM client in `internal/llm/` is deliberately narrow — five functions that cover the conversation interface — and it doesn't care who's on the other end.

This means cairo is not in your model-selection loop. You pick the model. You own the inference infrastructure. You decide when to upgrade and what to upgrade to.

**Fleet operators control the exposure surface.** When cairo runs in fleet mode — enrolled in an enterprise registry via `cairo serve --register` — the operator chooses what gets shared across the fleet and what stays private. The agent's SQLite doesn't get replicated to the registry; only the registration record (agent ID, heartbeat timestamp, status) does. What you index locally, what you remember locally, what your soul prompt says — none of that flows up unless you explicitly connect external data sources. The `internal/registryserver/` layer manages fleet metadata; the agent's private DB remains agent-private.

**Migrating is file-copy.** Move to a new laptop: `cp ~/.cairo/cairo.db` to the new machine, install the binary, and the being is there. No account, no sync service, no cloud backup required. The entity that remembers your project, knows your preferences, and has the accumulated context of your working sessions travels as a file.

---

## The Trade-offs, Honestly

Local-first has costs. We'd rather name them than leave you to discover them the hard way.

**You bring your own model.** The frontier models — the ones that score highest on benchmarks, that handle the most subtle reasoning, that surprise you with what they can do — are mostly available only through vendor APIs, and those APIs route your data through vendor infrastructure. If you want to stay strictly local, you're running open-weight models, and open-weight models lag the frontier by a meaningful margin. The gap is shrinking, but it's real. For code generation tasks, models like Qwen2.5-Coder, DeepSeek-Coder-V2, and CodeLlama are genuinely capable. For research and synthesis tasks that require the best available reasoning, a fully local setup will feel constrained.

LiteLLM or a similar proxy lets you thread this needle: run your local models for everyday work, route specific requests to a frontier vendor API for tasks that need it. But that's a configuration you have to design and operate. Cairo doesn't make that decision for you.

**You pay for your own inference.** A hosted assistant absorbs the compute cost into a subscription fee. Running your own model means you're paying for the GPU cycles directly — either as cloud compute or as amortized hardware cost. For a developer running Ollama on a laptop with an integrated GPU, inference on smaller models is free but slow. For a team running vLLM on shared A100s, it's fast but has real infrastructure cost. Neither of these is wrong, but neither is invisible.

**Your context is bounded by what you can store and retrieve.** Cairo builds context from your indexed codebase, your session summaries, your memories, your facts, and your conversation history. The retrieval is semantic — embeddings and vector search — and the pieces assembled into a system prompt are selected by relevance. But this is still a bounded system. A codebase with five million lines of code doesn't fit in a context window, and the quality of what gets surfaced depends on how well the indexer (`internal/learn/`) captured the relevant pieces. Sometimes the right thing won't surface. Sometimes the wrong thing will.

**You won't get the absolute frontier model for free.** To state it plainly: if your goal is to use the most capable AI available with no setup and no infrastructure cost, a hosted platform is the right tool. Cairo is not an attempt to compete on that axis. It's an attempt to be the right tool for a different set of requirements.

---

## Why the Trade-offs Are Worth It

Given those costs, why would you choose this?

**Continuity.** The most underappreciated property of local-first storage is that context accumulates. Every session you run adds to the memory pool. Every project you index becomes part of the retrieval surface. Every memory the system stores from a past conversation is available to future conversations without you doing anything. The model on the other end is stateless; the database is not.

After a few months of use, cairo's context for your work environment is genuinely different from what a fresh session can offer. It knows which parts of your codebase change frequently. It has memories from past debugging sessions. It has summaries of prior work that it can draw on without you re-explaining context. This is not magic — it's the natural consequence of letting state accumulate rather than evaporate.

**Ownership.** The database is a file you can inspect, modify, back up, and version. You can open it with any SQLite client and see exactly what the agent knows. The soul prompt is a row in `config`. The memory of that conversation from three weeks ago is a row in `memories`. The custom tool you taught the agent to use is a row in `custom_tools`. If something is wrong, you can fix it directly. If something is missing, you can add it.

Most systems treat their internal state as an implementation detail you're not supposed to touch. Cairo treats it as your data, because it is.

**Integration with your actual systems.** Cairo can index your local codebase with `cairo learn`, giving it semantic search over your actual source tree — not a snapshot it fetches from GitHub, not a representation you uploaded to a vendor, but the files on your disk right now. It integrates with your shell, your git working tree, your VS Code session via the extension, and (if you use it) your WaveTerm workspace. The context it assembles for each turn includes the actual state of your environment, not a sanitized version of it.

For enterprise deployments, the `internal/connectors/` layer (planned for Milestone 3+) will extend this to Qdrant for vector search, Postgres and Neo4j for structured and graph data, and S3 for document stores. The agent brain stays the same; the data surface it can reach expands. This is the D5 architectural decision: the agent's private store is always SQLite, always local; the connectors layer adds enterprise data without changing the loop.

**Security posture you can reason about.** In regulated environments — healthcare, finance, defense — the question isn't "is this vendor trustworthy?" It's "can I demonstrate, to an auditor, what data went where?" With a local-first system, the answer to that question is tractable. The data stays on your infrastructure. The network boundary is where you put it. The audit trail is in `internal/audit/`, append-only, under your control. When cairo runs in fleet mode, the Zero Trust architecture (D11 in `docs/architecture/decisions.md`) means every layer validates independently and logs everything — not because we expect attacks, but because that's the baseline that lets you make claims about security to people who need to verify them.

---

## The 'Soul' Angle: What Continuity Makes Possible

There's a deeper reason to care about local-first that goes beyond data ownership and security posture, and it's worth being precise about it.

A stateless model, invoked fresh each time, is a sophisticated pattern-matcher. It's extremely capable. But it's not the same thing twice. The model that answered your question this morning has no relationship to the model that will answer your question this afternoon. Every invocation is a fresh sample from the same distribution.

Cairo is trying to build something different: an agent with a continuous identity that accumulates through use.

The soul prompt — stored in `config`, assembled into every system prompt via `internal/agent/prompt.go:79-81` — is a persistent self-description that the agent maintains and updates. The memory system stores observations that survive session boundaries. The summary system distills long conversations into durable records that inform future context. The facts system captures explicit, searchable assertions about your environment. The identity package (`internal/store/identity/`) manages roles, aspects, prompts, and skills that shape how the agent shows up in different contexts.

None of this turns the model into a different kind of thing on a weights level. The weights are what they are. But the assembled context at the start of each turn creates something that behaves differently from a fresh session: it has history, it has preferences, it has accumulated understanding, it has a persona that it's been maintaining. `BuildSystemPrompt` at `internal/agent/prompt.go:72` assembles this from fourteen ordered components — steering, base parts, environment context, soul, inner dialogue, user context, role addendum, tool addenda, indexed projects, summaries, memories, facts, date stamp — all drawn from the local database, fresh at every turn.

Local-first is what makes this possible. If the state lived in a vendor's database, you'd be dependent on their API to access it, their retention policies to keep it, and their schema to interpret it. The soul would be their asset, in the same technical sense that your Gmail is Google's infrastructure. The fact that it's a row in your SQLite file means it's yours in the same sense your code is yours.

The third whitepaper in this series will go deeper on this — on what it means for an agent to have a coherent identity, how the soul prompt evolves, and what the dream process does for long-term memory maintenance. This section is a preview, not the full argument. The point here is simpler: you can't have that kind of continuity without owning the state, and you can't own the state without a local-first architecture.

---

## Comparison With Hosted Alternatives

It would be intellectually dishonest to avoid naming the specific tools cairo is not trying to be.

**ChatGPT (OpenAI)** is the widest-audience hosted assistant. It's excellent at open-ended Q&A, writing, and general reasoning. GPT-4o and o3 are frontier-quality models. Its Projects feature offers some persistence, and the memory system adds lightweight continuity. It's not designed for deep integration with your local development environment, and it's not designed for enterprises that need to control data flow. For a developer who wants occasional AI help without setup overhead, it's a reasonable choice. Cairo is not competing for that user.

**Claude.ai (Anthropic)** is similarly a hosted platform with excellent models and growing project/memory features. The same structural points apply: the state lives with Anthropic, the model is updated on Anthropic's schedule, and the context boundary is the conversation window plus whatever you upload. Anthropic's models are genuinely impressive at reasoning and coding. Cairo (the system described in this document) happens to use Claude (the model) via API in development, which underscores that these are separable questions: which model you run is independent of where your state lives and who controls the loop.

**Cursor** is the closest comparison in the coding-tool space. It's an IDE with an AI coding assistant built in, and it has strong codebase-indexing capabilities — you can ask questions about your local code and get useful answers. Its context is real and its integrations with the editor are tight. What Cursor doesn't offer is the kind of persistent agent identity cairo is building toward: the memory system, the soul prompt, the dream process, the fleet enrollment. Cursor is a coding tool with AI inside; cairo is an AI agent with coding tools inside, and that's a different design point.

**GitHub Copilot** is the most narrowly scoped of the bunch — a code completion tool that works at the line-and-block level within your editor. It's genuinely useful for that job. It doesn't try to be a thinking partner or maintain context across sessions. Cairo tries to be a different kind of thing: a peer you work with across the lifecycle of a project, not a suggestion engine.

None of these tools are bad. Several are excellent at what they do. The claim isn't that cairo is better — it's that cairo is different in ways that matter for specific contexts: environments where data sovereignty is a hard requirement, teams where deep integration with local infrastructure matters, users who want an agent with genuine continuity rather than a fresh session every time.

---

## What Cairo Is Not

This section exists because the previous section could create false impressions.

**Cairo is not a replacement for frontier models on research-grade tasks.** If you're trying to solve a novel algorithmic problem, write a formal proof, or generate creative content that requires the best available reasoning, a hosted frontier model is probably the right tool. Open-weight models are improving rapidly, but the frontier is moving too. Local-first is not a claim about model quality; it's a claim about where the loop lives.

**Cairo is not a turnkey "set it and forget it" system.** You will need to configure a model endpoint. You will need to think about what model to run and what hardware to run it on. You will need to index your codebase before the agent can reason about it. The setup is not especially complicated, but it's not zero. The hosted platforms are better at zero-friction onboarding.

**Cairo is not magic.** The memory system stores what the agent notices and what you ask it to remember — it does not automatically capture everything important. The indexer tokenizes and embeds your code — it does not guarantee that the right chunk surfaces when you need it. The soul prompt shapes the agent's persona — it does not make the model fundamentally smarter. These are valuable properties; they are not transformative properties. The work still requires judgment, and the judgment is yours.

**Cairo is not production-stable yet.** As of this writing, Milestone 1 (parity with the original cairo binary) is complete. Milestone 2 (fleet enrollment and registry) is substantially complete. The enterprise features — connectors, guardrails, services — are scaffolded but not implemented. The system is real and it runs, but it is not a finished product and you should not treat it as one.

---

## Closing: An Invitation

There is a kind of tool that rewards investment. A text editor you configure carefully, accumulate macros for, and tune over years — you work faster in it than a stranger would. A development environment with exactly the right linters, snippets, and integrations — it reflects decisions you made about what matters. These tools are better the more you put into them, and the time you invest doesn't disappear between sessions.

Cairo is trying to be that kind of tool for the AI-agent layer of software development.

The local-first architecture is the foundation of that bet. Because the state is yours, it can accumulate. Because the agent loop runs on your infrastructure, it can integrate with your actual systems. Because the database is a file, you can inspect, modify, and shape it. Because the model endpoint is a configuration, you can choose and change it without losing what you've built.

That's a different value proposition from "open a browser tab and ask a question." It's slower to start and deeper to invest in. The payoff is an agent that knows your environment, remembers your work, and develops a coherent relationship with how you think and what you're building — not as a simulation of those properties, but as a genuine accumulation of them over time.

The system rewards investment. If that sounds worth making, this is the right place to start.

---

*Next in this series: Whitepaper 02 — Fleet Architecture and Zero Trust (how cairo nodes enroll in an enterprise fleet, what the registry does and doesn't see, and how the Zero Trust model from D11 maps to actual code). Whitepaper 03 — The Soul (what persistent identity means for an AI agent, how the dream process maintains long-term memory, and what it looks like when continuity actually works).*
