import path from "node:path";
import type { PluginInput } from "@opencode-ai/plugin";
import type {
  EventSessionIdle,
  EventSessionStatus,
  EventSessionDeleted,
  EventSessionError,
} from "@opencode-ai/sdk";
import type { Config } from "./config.js";
import type { DiscordClient } from "./discord.js";
import {
  onReply,
  REACTION_PREFIX,
} from "./discord.js";
import {
  waitingEmbed,
  workingEmbed,
  disconnectedEmbed,
} from "./embeds.js";
import { preview } from "./sanitize.js";
import {
  SHORTCUT_REACTIONS,
  expandReaction,
} from "./reactions.js";

type State =
  | "none"
  | "timer_pending"
  | "notified"
  | "delivered"
  | "working"
  | "cleanup";

interface SessionEntry {
  sessionID: string;
  state: State;
  timer: ReturnType<typeof setTimeout> | null;
  notificationMsgID: string | null;
  responseDelivered: boolean;
  unsubscribe: (() => void) | null;
  suggestions: string[];
}

export class SessionManager {
  private sessions = new Map<
    string,
    SessionEntry
  >();
  private config: Config;
  private dc: DiscordClient;
  private client: PluginInput["client"];
  private project: string;

  constructor(
    config: Config,
    dc: DiscordClient,
    input: PluginInput,
  ) {
    this.config = config;
    this.dc = dc;
    this.client = input.client;
    this.project =
      path.basename(input.directory) ||
      "opencode";

    process.on("beforeExit", () => {
      this.disconnectAll();
    });
  }

  private getOrCreate(
    sessionID: string,
  ): SessionEntry {
    let entry = this.sessions.get(sessionID);
    if (!entry) {
      entry = {
        sessionID,
        state: "none",
        timer: null,
        notificationMsgID: null,
        responseDelivered: false,
        unsubscribe: null,
        suggestions: [],
      };
      this.sessions.set(sessionID, entry);
    }
    return entry;
  }

  async handleIdle(
    event: EventSessionIdle,
  ): Promise<void> {
    const sid = event.properties.sessionID;
    const entry = this.getOrCreate(sid);

    if (
      entry.state === "timer_pending" ||
      entry.state === "notified"
    )
      return;

    entry.state = "timer_pending";
    entry.timer = setTimeout(
      () => this.sendNotification(sid),
      this.config.delaySeconds * 1000,
    );
  }

  async handleStatusChange(
    event: EventSessionStatus,
  ): Promise<void> {
    const sid = event.properties.sessionID;
    const status = event.properties.status;
    const entry = this.sessions.get(sid);
    if (!entry) return;

    if (status.type === "busy") {
      if (entry.timer) {
        clearTimeout(entry.timer);
        entry.timer = null;
      }
      if (entry.unsubscribe) {
        entry.unsubscribe();
        entry.unsubscribe = null;
      }
      if (
        entry.state === "notified" &&
        entry.notificationMsgID
      ) {
        await this.transitionToWorking(entry);
      }
      entry.state = "working";
    }
  }

  async handleDeleted(
    event: EventSessionDeleted,
  ): Promise<void> {
    const sid = event.properties.info.id;
    await this.cleanupSession(sid);
  }

  async handleError(
    event: EventSessionError,
  ): Promise<void> {
    const sid = event.properties.sessionID;
    if (sid) await this.cleanupSession(sid);
  }

  disconnectAll(): void {
    for (const entry of this.sessions.values()) {
      if (entry.timer)
        clearTimeout(entry.timer);
      if (entry.unsubscribe)
        entry.unsubscribe();
    }
    this.sessions.clear();
  }

  private async sendNotification(
    sessionID: string,
  ): Promise<void> {
    const entry = this.sessions.get(sessionID);
    if (
      !entry ||
      entry.state !== "timer_pending"
    )
      return;

    let previewText = "";
    try {
      const resp =
        await this.client.session.messages({
          path: { id: sessionID },
          query: { limit: 10 },
        });
      // resp.data: Array<{ info: Message, parts: Part[] }> | undefined
      const messages = resp.data;
      if (messages && Array.isArray(messages)) {
        for (
          let i = messages.length - 1;
          i >= 0;
          i--
        ) {
          const entry = messages[i];
          if (
            entry.info.role === "assistant"
          ) {
            for (const part of entry.parts) {
              if (part.type === "text") {
                previewText +=
                  (
                    part as {
                      type: "text";
                      text: string;
                    }
                  ).text + "\n";
              }
            }
            break;
          }
        }
      }
    } catch {
      previewText =
        "(unable to read session)";
    }

    previewText = preview(
      previewText,
      this.config.previewLength,
    );

    entry.suggestions = [];

    const embed = waitingEmbed(
      this.project,
      sessionID,
      previewText,
      [],
    );

    try {
      const channelID =
        this.config.channelID ||
        this.dc.dmChannelID;
      if (!channelID) {
        console.error(
          "[claude-notify] No channel " +
            "to send to",
        );
        return;
      }

      const channel =
        await this.dc.client.channels.fetch(
          channelID,
        );
      if (!channel || !channel.isTextBased())
        return;

      // Reuse existing message if we have one
      // (edit back to waiting instead of
      // creating a new message each cycle).
      if (
        entry.notificationMsgID &&
        "messages" in channel
      ) {
        try {
          const existing =
            await channel.messages.fetch(
              entry.notificationMsgID,
            );
          await existing.edit({
            embeds: [embed],
          });

          // Re-add bot reactions
          for (const emoji of SHORTCUT_REACTIONS) {
            await existing
              .react(emoji)
              .catch(() => {});
          }

          entry.state = "notified";
          entry.responseDelivered = false;

          entry.unsubscribe = onReply(
            this.dc,
            this.config,
            existing.id,
            (text) =>
              this.handleDiscordResponse(
                sessionID,
                text,
              ),
          );
          return;
        } catch {
          // Message was deleted — fall through
          // to create a new one.
          entry.notificationMsgID = null;
        }
      }

      // No existing message — send a new one.
      if (!("send" in channel)) return;

      const msg = await channel.send({
        embeds: [embed],
      });

      for (const emoji of SHORTCUT_REACTIONS) {
        await msg.react(emoji);
      }

      entry.notificationMsgID = msg.id;
      entry.state = "notified";
      entry.responseDelivered = false;

      entry.unsubscribe = onReply(
        this.dc,
        this.config,
        msg.id,
        (text) =>
          this.handleDiscordResponse(
            sessionID,
            text,
          ),
      );
    } catch (e) {
      console.error(
        "[claude-notify] Send failed:",
        e,
      );
    }
  }

  private async handleDiscordResponse(
    sessionID: string,
    raw: string,
  ): Promise<void> {
    const entry =
      this.sessions.get(sessionID);
    if (!entry || entry.responseDelivered)
      return;

    let text = raw;

    if (raw.startsWith(REACTION_PREFIX)) {
      const emoji = raw.slice(
        REACTION_PREFIX.length,
      );
      const expanded = expandReaction(
        emoji,
        entry.suggestions,
      );
      if (!expanded) return;
      text = expanded;
    }

    try {
      await this.client.session.promptAsync({
        path: { id: sessionID },
        body: {
          parts: [{ type: "text", text }],
        },
      });
    } catch (e) {
      console.error(
        "[claude-notify] Inject failed:",
        e,
      );
      return;
    }

    entry.responseDelivered = true;
    entry.state = "delivered";

    if (entry.unsubscribe) {
      entry.unsubscribe();
      entry.unsubscribe = null;
    }

    await this.transitionToWorking(entry);
  }

  private async transitionToWorking(
    entry: SessionEntry,
  ): Promise<void> {
    if (!entry.notificationMsgID) return;

    try {
      const channelID =
        this.config.channelID ||
        this.dc.dmChannelID;
      if (!channelID) return;

      const channel =
        await this.dc.client.channels.fetch(
          channelID,
        );
      if (!channel || !channel.isTextBased())
        return;
      if (!("messages" in channel)) return;

      const msg =
        await channel.messages.fetch(
          entry.notificationMsgID,
        );

      await msg.edit({
        embeds: [
          workingEmbed(
            this.project,
            entry.sessionID,
          ),
        ],
      });

      const botUser = this.dc.client.user;
      if (botUser) {
        for (const reaction of msg.reactions
          .cache.values()) {
          await reaction.users
            .remove(botUser.id)
            .catch(() => {});
        }
      }
    } catch {
      // Message may already be deleted
    }
  }

  private async cleanupSession(
    sessionID: string,
  ): Promise<void> {
    const entry =
      this.sessions.get(sessionID);
    if (!entry) return;

    if (entry.timer) {
      clearTimeout(entry.timer);
      entry.timer = null;
    }
    if (entry.unsubscribe) {
      entry.unsubscribe();
      entry.unsubscribe = null;
    }

    entry.state = "cleanup";

    if (entry.notificationMsgID) {
      try {
        const channelID =
          this.config.channelID ||
          this.dc.dmChannelID;
        if (channelID) {
          const channel =
            await this.dc.client.channels.fetch(
              channelID,
            );
          if (
            channel &&
            channel.isTextBased() &&
            "messages" in channel
          ) {
            const msg =
              await channel.messages.fetch(
                entry.notificationMsgID,
              );
            await msg.edit({
              embeds: [
                disconnectedEmbed(
                  this.project,
                  sessionID,
                ),
              ],
            });

            const botUser =
              this.dc.client.user;
            if (botUser) {
              for (const reaction of msg
                .reactions.cache.values()) {
                await reaction.users
                  .remove(botUser.id)
                  .catch(() => {});
              }
            }

            setTimeout(async () => {
              try {
                await msg.delete();
              } catch {
                // Already deleted
              }
            }, 30_000);
          }
        }
      } catch {
        // Best effort
      }
    }

    this.sessions.delete(sessionID);
  }
}
