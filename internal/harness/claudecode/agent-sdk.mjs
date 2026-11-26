import { randomUUID } from "node:crypto";
import { once } from "node:events";
import { createInterface } from "node:readline";
import { Readable } from "node:stream";
import { pathToFileURL } from "node:url";

const { query } = await import(pathToFileURL(process.argv.at(-1)).href);
const goalService = "shioji_goal";
const goalTool = "mcp__shioji_goal__report_goal";

async function emit(message) {
  if (!process.stdout.write(JSON.stringify(message) + "\n")) {
    await once(process.stdout, "drain");
  }
}

async function run() {
  const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
  const commands = lines[Symbol.asyncIterator]();
  const first = await commands.next();
  if (first.done) return;
  const start = JSON.parse(first.value);
  if (start.type !== "start") throw new Error("Expected a session start");

  let closed = false;
  let active;
  const approvals = new Map();
  const close = () => {
    if (closed) return;
    closed = true;
    for (const resolve of approvals.values()) resolve(null);
    active?.close();
    lines.close();
  };
  lines.once("close", close);

  const defaultTools = ["Read", "Glob", "Grep", "Edit", "Write", "Bash", "AskUserQuestion", "ExitPlanMode", "Agent"];
  const readOnlyTools = ["Read", "Glob", "Grep", "AskUserQuestion", "ExitPlanMode", "Agent"];
  const permissionModeFor = (config) => {
    if (config?.plan) return "plan";
    if (config?.permission === "yolo") return "bypassPermissions";
    if (config?.permission === "auto-approve" && start.accept_edits) return "acceptEdits";
    return "default";
  };
  const extraArgsFor = (config) => {
    const args = { "no-chrome": null, "replay-user-messages": null };
    if (config?.permission !== "yolo" || start.bypass_restricted) {
      args.restricted = null;
    }
    return args;
  };

  function open(thread, resume, config, mcp, revision) {
    const prompts = new Readable({ objectMode: true, read() {} });
    const connection = { thread, config, prompts, mcp, revision, skillCommands: new Set(),
      inputIDs: new Set(), pendingSteers: new Set(), running: false };
    const session = query({
      prompt: prompts,
      options: {
        pathToClaudeCodeExecutable: start.binary,
        ...(start.title
          ? { persistSession: false, maxTurns: 1 }
          : resume ? { resume: thread } : { sessionId: thread }),
        model: start.model,
        additionalDirectories: start.directories ?? [],
        settings: { ...start.settings, ...(config.effort ? { effortLevel: config.effort } : {}),
          ...(config.fast ? { fastMode: true } : {}) },
        settingSources: [],
        tools: start.title ? [] : (config.permission === "read-only" ? readOnlyTools : defaultTools),
        permissionMode: permissionModeFor(config),
        ...(config.permission === "yolo" ? { allowDangerouslySkipPermissions: true } : {}),
        permissionPrompts: "host",
        includePartialMessages: true,
        strictMcpConfig: true,
        mcpServers: mcp ?? {},
        extraArgs: extraArgsFor(config),
        async canUseTool(name, input, { signal, toolUseID, agentID }) {
          if (start.title || connection !== active || signal.aborted) {
            return { behavior: "deny", message: "Tool request cancelled." };
          }
          if (name === goalTool && connection.goalKey && input.key === connection.goalKey) {
            return { behavior: "allow", updatedInput: input };
          }
          const id = randomUUID();
          const decision = new Promise((resolve) => approvals.set(id, resolve));
          const cancel = () => {
            approvals.get(id)?.(null);
            if (!closed && connection === active) {
              void emit({ type: "approval_cancelled", session_id: thread, request_id: id }).catch(close);
            }
          };
          signal.addEventListener("abort", cancel, { once: true });
          try {
            await emit({ type: "approval", session_id: thread, request_id: id, tool_name: name, tool_use_id: toolUseID, agent_id: agentID, input });
            const reply = await decision;
            if (!reply || signal.aborted || closed || connection !== active) {
              return { behavior: "deny", message: "Tool request cancelled." };
            }
            void emit({ type: "approval_applied", session_id: thread, request_id: id }).catch(close);
            if (!reply.allow) return {
              behavior: "deny",
              message: name === "ExitPlanMode"
                ? "The client captured the proposed plan. Stop here and wait for the user's next turn."
                : "Tool request declined.",
            };
            const updatedInput = name === "AskUserQuestion"
              ? { questions: input.questions, answers: reply.answers }
              : input;
            return { behavior: "allow", updatedInput };
          } finally {
            approvals.delete(id);
            signal.removeEventListener("abort", cancel);
          }
        },
      },
    });
    connection.session = session;
    connection.close = () => {
      prompts.push(null);
      session.close();
    };
    return connection;
  }

  async function applyTools(connection, message) {
    const needsGoal = Boolean(message.goal_key);
    if (message.mcp_revision === connection.revision && needsGoal === Boolean(connection.goalServer)) return true;
    const mcp = message.mcp ?? connection.mcp ?? {};
    const servers = { ...mcp };
    if (needsGoal) {
      connection.goalServer = message.goal_server;
      if (!connection.goalServer) return false;
      servers[goalService] = connection.goalServer;
    }
    try {
      if (needsGoal || connection.goalServer || Object.keys(mcp).length || Object.keys(connection.mcp ?? {}).length) {
        const result = await connection.session.setMcpServers(servers);
        if (needsGoal && result?.errors?.[goalService]) return false;
      }
    } catch (error) {
      if (needsGoal) return false;
      throw error;
    }
    if (!needsGoal) connection.goalServer = undefined;
    connection.mcp = mcp;
    connection.revision = message.mcp_revision;
    void observeMcp(connection).catch(close);
    return true;
  }

  async function loadSkill(connection, message) {
    if (!message.skill_command || connection.skillCommands.has(message.skill_command)) return true;
    try {
      const result = await connection.session.reloadPlugins({ addPlugins: message.skill_plugins });
      if (!result.commands?.some(({ name }) => name === message.skill_command)) return false;
      connection.skillCommands.add(message.skill_command);
      return true;
    } catch {
      return false;
    }
  }

  function observeMcp(connection) {
    const revision = connection.revision;
    if (!revision || closed || active !== connection) return Promise.resolve();
    if (!Object.keys(connection.mcp ?? {}).length) {
      return emit({ type: "mcp_status", session_id: connection.thread, mcp_revision: revision, servers: [] });
    }
    return connection.session.mcpServerStatus().then((servers) => {
      if (closed || active !== connection || connection.revision !== revision) return;
      return emit({ type: "mcp_status", session_id: connection.thread, mcp_revision: revision, servers: servers.filter(({ name }) => name !== goalService).map(({ name, status, tools }) => ({
        name, status, tools: tools?.map(({ name, description }) => ({ name, description })),
      })) });
    }).catch(() => {});
  }

  async function stream(connection) {
    const { session, thread } = connection;
    let observingContext = false;
    try {
      for await (const message of session) {
        if (closed || active !== connection) return;
        observeSteers(connection, message);
        if (message.type === "result" && ownsResult(connection, message)) {
          connection.goalKey = undefined;
          connection.acceptsSteers = false;
          connection.running = false;
          if (connection.interruption) await connection.interruption;
        }
        await emit(message);
        if (start.title || message.type !== "result" || !ownsResult(connection, message) || message.is_error || !message.uuid || observingContext) continue;
        observingContext = true;
        // Summary is local. A slow observation must never hold a turn open.
        void session.getContextUsage({ detail: "summary" }).then((usage) => {
          if (closed || active !== connection) return;
          return emit({
            type: "context_usage",
            session_id: thread,
            uuid: message.uuid,
            used_tokens: Number.isSafeInteger(usage.totalTokens) ? usage.totalTokens : undefined,
            limit_tokens: Number.isSafeInteger(usage.rawMaxTokens) ? usage.rawMaxTokens : undefined,
          });
        }).catch(() => {
          // Gateways or older CLIs may not provide context counts.
        }).finally(() => {
          observingContext = false;
        });
      }
    } catch {
      if (!closed && active === connection) process.exitCode = 1;
    } finally {
      if (active === connection) close();
    }
  }

  function ownsResult(connection, message) {
    if (message.origin?.kind === "task-notification") return false;
    const ids = message.user_message_uuids?.length ? message.user_message_uuids : [message.user_message_uuid].filter(Boolean);
    if (ids.length) return ids.some((id) => connection.inputIDs.has(id));
    return !message.origin?.kind || message.origin.kind === "human";
  }

  function observeSteers(connection, message) {
    if (message.origin?.kind === "task-notification") return;
    if (message.type === "command_lifecycle") {
      if (["started", "cancelled", "discarded", "refused"].includes(message.state) &&
          connection.pendingSteers.delete(message.command_uuid) && message.state === "started") {
        connection.running = true;
      }
      return;
    }
    const echoed = message.type === "user" && message.isReplay
      ? [message.uuid] : message.user_message_uuids ?? [message.user_message_uuid];
    for (const id of echoed) {
      if (connection.pendingSteers.delete(id)) connection.running = true;
    }
  }

  try {
    active = open(start.thread, start.resume, start.configuration ?? {}, start.mcp, start.mcp_revision);
    const initialized = await active.session.initializationResult();
    if (closed) return;
    await emit({
      type: "ready",
      ready_commands: initialized?.commands?.map(({ name }) => name) ?? [],
    });
    void stream(active);
    void observeMcp(active).catch(close);

    for await (const line of commands) {
      if (closed) return;
      const message = JSON.parse(line);
      switch (message.type) {
        case "prompt":
          if (message.thread && active.thread !== message.thread) throw new Error("Prompt belongs to a different session");
          if (!await loadSkill(active, message) || !await applyTools(active, message)) {
            await emit({ type: "input_refused", session_id: active.thread, uuid: message.uuid });
            break;
          }
          const config = message.configuration ?? active.config;
          if (permissionModeFor(config) !== permissionModeFor(active.config)) {
            await active.session.setPermissionMode(permissionModeFor(config));
          }
          if (config.effort !== active.config.effort || config.fast !== active.config.fast) {
            await active.session.applyFlagSettings({ effortLevel: config.effort || null, fastMode: config.fast || null });
          }
          active.config = config;
          active.turn = message.uuid;
          active.inputIDs = new Set([message.uuid]);
          active.pendingSteers.clear();
          active.running = true;
          active.goalKey = message.goal_key;
          active.acceptsSteers = true;
          active.prompts.push({
            type: "user",
            uuid: message.uuid,
            session_id: active.thread,
            parent_tool_use_id: null,
            message: { role: "user", content: message.content },
          });
          break;
        case "steer":
          if (active.thread !== message.thread || active.turn !== message.turn || !active.acceptsSteers ||
              !await loadSkill(active, message) || !active.acceptsSteers) {
            await emit({ type: "command_lifecycle", session_id: active.thread, command_uuid: message.uuid, state: "refused" });
            break;
          }
          active.inputIDs.add(message.uuid);
          active.pendingSteers.add(message.uuid);
          active.prompts.push({
            type: "user",
            uuid: message.uuid,
            session_id: active.thread,
            priority: "next",
            parent_tool_use_id: null,
            message: { role: "user", content: message.content },
          });
          break;
        case "approval":
          approvals.get(message.request_id)?.(message);
          break;
        case "stop_work": {
          let ok = false;
          try {
            if (message.thread === active.thread) {
              await active.session.stopTask(message.task_id);
              ok = true;
            }
          } catch {
            // The worker still observes the native task lifecycle.
          }
          if (!closed) await emit({ type: "work_stopped", session_id: active.thread, request_id: message.request_id, ok });
          break;
        }
        case "interrupt": {
          const connection = active;
          if (connection.thread !== message.thread || connection.turn !== message.uuid ||
              !connection.running && !connection.pendingSteers.size) break;
          connection.acceptsSteers = false;
          connection.interruption = (async () => {
            const outcome = await connection.session.interrupt({ cancelQueued: true });
            if (outcome?.still_queued?.length) throw new Error("Interrupted session still holds queued input");
            await emit({ type: "interrupt_applied", session_id: connection.thread, uuid: message.uuid });
            for (const uuid of outcome?.cancelled ?? []) {
              connection.pendingSteers.delete(uuid);
              await emit({ type: "command_lifecycle", session_id: connection.thread, command_uuid: uuid, state: "cancelled" });
            }
          })();
          try {
            await connection.interruption;
          } finally {
            connection.interruption = undefined;
          }
          break;
        }
        case "activate_history": {
          const previous = active;
          active = open(message.thread, message.resume, previous.config, previous.mcp, previous.revision);
          previous.close();
          await active.session.initializationResult();
          if (closed) return;
          void stream(active);
          void observeMcp(active).catch(close);
          break;
        }
        default:
          throw new Error("Unexpected session command");
      }
    }
  } finally {
    close();
  }
}

await run().catch(() => {
  // Errors from the SDK can include provider configuration or prompt content.
  // Shioji reports initialization and runtime loss from the pipe lifecycle.
  process.exitCode = 1;
});
