import { Readable } from "node:stream";
import { pathToFileURL } from "node:url";

const [binary, sdk] = process.argv.slice(1);
const { query } = await import(pathToFileURL(sdk).href);

async function probeSession(options, inspect) {
  const prompts = new Readable({ objectMode: true, read() {} });
  const session = query({ prompt: prompts, options });
  try {
    await session.initializationResult();
    return inspect ? await inspect(session) : true;
  } catch {
    return null;
  } finally {
    prompts.push(null);
    session.close();
  }
}

const permissions = {
  readOnly: false,
  default: false,
  autoApprove: false,
  acceptEdits: false,
  yolo: false,
  bypassPermissions: false,
  bypassRestricted: false,
};
let models = [];

const base = await probeSession({
  pathToClaudeCodeExecutable: binary,
  persistSession: false,
  settingSources: [],
  tools: ["Read", "Glob", "Grep"],
  permissionMode: "default",
  permissionPrompts: "host",
  strictMcpConfig: true,
  settings: {
    sandbox: {
      enabled: true,
      failIfUnavailable: true,
      allowUnsandboxedCommands: false,
      autoAllowBashIfSandboxed: false,
      filesystem: { allowWrite: [] },
    },
    permissions: { deny: ["Edit", "Write", "Bash"] },
  },
  extraArgs: { restricted: null, "no-chrome": null },
}, async (session) => {
  permissions.default = true;
  permissions.readOnly = true;
  permissions.autoApprove = true;
  try {
    const list = await session.supportedModels();
    if (Array.isArray(list)) models = list;
  } catch {
    // Model catalog from the SDK is optional when live API probing is used.
  }
  if (typeof session.setPermissionMode === "function") {
    try {
      await session.setPermissionMode("acceptEdits");
      await session.setPermissionMode("default");
      permissions.acceptEdits = true;
    } catch {
      // Auto-approve still enforces host approval inside the sandbox.
    }
  }
  return true;
});

if (base) {
  const yoloBase = {
    pathToClaudeCodeExecutable: binary,
    persistSession: false,
    settingSources: [],
    tools: [],
    permissionMode: "bypassPermissions",
    allowDangerouslySkipPermissions: true,
    permissionPrompts: "host",
    strictMcpConfig: true,
    settings: { sandbox: { enabled: false, allowUnsandboxedCommands: true } },
  };
  if (await probeSession({ ...yoloBase, extraArgs: { restricted: null, "no-chrome": null } })) {
    permissions.yolo = true;
    permissions.bypassPermissions = true;
    permissions.bypassRestricted = true;
  } else if (await probeSession({ ...yoloBase, extraArgs: { "no-chrome": null } })) {
    permissions.yolo = true;
    permissions.bypassPermissions = true;
    permissions.bypassRestricted = false;
  }
  process.stdout.write(JSON.stringify({ probed: true, models, permissions }));
} else {
  process.exitCode = 1;
}
