import type { Plugin } from "@opencode-ai/plugin";
import { loadConfig } from "./config.js";
import { connectDiscord } from "./discord.js";
import { SessionManager } from "./sessions.js";

const plugin: Plugin = async (input) => {
  let config: ReturnType<typeof loadConfig>;
  try {
    config = loadConfig();
  } catch (e: unknown) {
    const msg =
      e instanceof Error
        ? e.message
        : String(e);
    console.log(
      `[claude-notify] disabled: ${msg}`,
    );
    return {};
  }

  console.log(
    "[claude-notify] connecting to Discord...",
  );

  let dc: Awaited<
    ReturnType<typeof connectDiscord>
  >;
  try {
    dc = await connectDiscord(config);
  } catch (e) {
    console.error(
      "[claude-notify] Discord connection " +
        "failed:",
      e,
    );
    return {};
  }

  const sessions = new SessionManager(
    config,
    dc,
    input,
  );

  console.log("[claude-notify] plugin active");

  return {
    event: async ({ event }) => {
      switch (event.type) {
        case "session.idle":
          await sessions.handleIdle(event);
          break;
        case "session.status":
          await sessions.handleStatusChange(
            event,
          );
          break;
        case "session.deleted":
          await sessions.handleDeleted(event);
          break;
        case "session.error":
          await sessions.handleError(event);
          break;
      }
    },
  };
};

export default plugin;
