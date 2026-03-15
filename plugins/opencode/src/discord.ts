import {
  Client,
  GatewayIntentBits,
  Partials,
  ChannelType,
  type Message,
  type MessageReaction,
  type PartialMessageReaction,
  type User,
  type PartialUser,
  type MessageReactionEventDetails,
} from "discord.js";
import type { Config } from "./config.js";
import { isAllowedUser } from "./config.js";
import { isKnownReaction } from "./reactions.js";

export interface DiscordClient {
  client: Client;
  dmChannelID: string | null;
  destroy(): void;
}

export async function connectDiscord(
  config: Config,
): Promise<DiscordClient> {
  const client = new Client({
    intents: [
      GatewayIntentBits.DirectMessages,
      GatewayIntentBits.DirectMessageReactions,
      GatewayIntentBits.GuildMessages,
      GatewayIntentBits.GuildMessageReactions,
    ],
    partials: [
      Partials.Channel,
      Partials.Message,
      Partials.Reaction,
    ],
  });

  let dmChannelID: string | null = null;

  const ready = new Promise<void>(
    (resolve, reject) => {
      client.once("ready", async () => {
        console.log(
          `[claude-notify] Discord connected ` +
            `as ${client.user?.tag}`,
        );
        try {
          const user =
            await client.users.fetch(
              config.ownerUserID,
            );
          const dm = await user.createDM();
          dmChannelID = dm.id;
        } catch (e) {
          console.error(
            "[claude-notify] Failed to open " +
              "DM channel:",
            e,
          );
        }
        resolve();
      });

      client.once("error", reject);

      setTimeout(
        () =>
          reject(
            new Error("Discord login timeout"),
          ),
        30_000,
      );
    },
  );

  await client.login(config.botToken);
  await ready;

  return {
    client,
    dmChannelID,
    destroy: () => client.destroy(),
  };
}

/**
 * Internal marker prefix for reaction events
 * passed through the callback.
 */
export const REACTION_PREFIX = "__REACTION__:";

/**
 * Register gateway handlers for replies and
 * reactions bound to a specific notification
 * message. Returns an unsubscribe function.
 *
 * Text replies are DM-only (channel mode is
 * reaction-only for MVP — avoids needing the
 * MessageContent privileged intent and prevents
 * treating guild chat as prompt injection).
 */
export function onReply(
  dc: DiscordClient,
  config: Config,
  notificationMsgID: string,
  callback: (text: string) => void,
): () => void {
  const handleMessage = async (
    msg: Message,
  ) => {
    if (msg.author.bot) return;
    if (!isAllowedUser(config, msg.author.id))
      return;

    // Text replies: DM only
    if (msg.channel.type !== ChannelType.DM)
      return;

    // Must be a reply-to our notification
    if (
      msg.reference?.messageId !==
      notificationMsgID
    )
      return;

    callback(msg.content);
  };

  const handleReaction = async (
    reaction: MessageReaction | PartialMessageReaction,
    user: User | PartialUser,
    _details: MessageReactionEventDetails,
  ) => {
    if (user.bot) return;
    if (!isAllowedUser(config, user.id))
      return;

    // Must be on our notification message
    if (
      reaction.message.id !==
      notificationMsgID
    )
      return;

    const emoji = reaction.emoji.name;
    if (
      !emoji ||
      !isKnownReaction(emoji)
    )
      return;

    callback(`${REACTION_PREFIX}${emoji}`);
  };

  dc.client.on(
    "messageCreate",
    handleMessage,
  );
  dc.client.on(
    "messageReactionAdd",
    handleReaction,
  );

  return () => {
    dc.client.off(
      "messageCreate",
      handleMessage,
    );
    dc.client.off(
      "messageReactionAdd",
      handleReaction,
    );
  };
}
