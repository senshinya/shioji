import { Readable } from "node:stream";
import { pathToFileURL } from "node:url";

const [binary, sdk] = process.argv.slice(1);
const { query } = await import(pathToFileURL(sdk).href);
const prompts = new Readable({ objectMode: true, read() {} });
const session = query({
  prompt: prompts,
  options: {
    pathToClaudeCodeExecutable: binary, persistSession: false,
    settingSources: [], tools: [], permissionMode: "default", strictMcpConfig: true,
    extraArgs: { restricted: null, "no-chrome": null },
  },
});
try {
  await session.initializationResult();
  process.stdout.write(JSON.stringify(await session.supportedModels()));
} catch {
  process.exitCode = 1;
} finally {
  prompts.push(null);
  session.close();
}
