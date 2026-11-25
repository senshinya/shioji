import { createHash } from "node:crypto";
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";

const { getSessionMessages, getSessionInfo, listSessions, forkSession } = await import(pathToFileURL(process.argv.at(-1)).href);
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
try {
  const input = await lines[Symbol.asyncIterator]().next();
  if (input.done) throw new Error("Missing history request");
  const request = JSON.parse(input.value);
  if (request.action === "fork") {
    const fork = await forkSession(request.thread, {
      dir: request.directory,
      upToMessageId: request.through,
    });
    process.stdout.write(JSON.stringify(fork));
  } else if (request.action === "list") {
    const sessions = await listSessions({
      dir: request.directory,
      includeWorktrees: false,
      limit: 51,
      offset: request.offset ?? 0,
    });
    process.stdout.write(JSON.stringify(sessions));
  } else if (request.action === "boundaries" || request.action === "read") {
    const info = request.action === "read" ? await getSessionInfo(request.thread, { dir: request.directory }) : undefined;
    if (info?.fileSize > 64 * 1024 * 1024) throw new Error("History exceeds the import limit");
    const records = await getSessionMessages(request.thread, { dir: request.directory, limit: 10001 });
    if (records.length > 10000) throw new Error("History exceeds the inspection limit");
    const boundaries = records.map((record) => {
      const body = record.message?.content;
      const user = record.type === "user" && !record.is_meta &&
        !record.isQueuedCommand && !record.isCompletedLocalCommand &&
        (!record.origin?.kind || record.origin.kind === "human") &&
        (typeof body === "string" || Array.isArray(body) && !body.some(({ type }) => type === "tool_result"));
      return {
        uuid: record.uuid,
        type: record.type,
        user,
        ...(request.action === "read" ? { message: record.message, timestamp: record.timestamp, toolUseResult: record.toolUseResult ?? record.tool_use_result } : {}),
        digest: createHash("sha256").update(JSON.stringify(record.message ?? null)).digest("hex"),
      };
    });
    if (request.action === "read") {
      process.stdout.write(JSON.stringify({ info, records: boundaries }));
    } else {
      process.stdout.write(JSON.stringify(boundaries));
    }
  } else {
    throw new Error("Unknown history request");
  }
} catch {
  // Local history may contain private paths and messages.
  process.exitCode = 1;
} finally {
  lines.close();
}
