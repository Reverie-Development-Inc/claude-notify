import { EmbedBuilder } from "discord.js";

const COLOR_WORKING = 0x2ecc71;
const COLOR_WAITING = 0xf1c40f;
const COLOR_DISCONNECTED = 0xe74c3c;

export function waitingEmbed(
  project: string,
  sessionID: string,
  previewText: string,
  suggestions: string[],
): EmbedBuilder {
  let desc = previewText;
  if (suggestions.length > 0) {
    desc += "\n";
    for (
      let i = 0;
      i < suggestions.length;
      i++
    ) {
      desc +=
        `\n**${i + 1}.** ${suggestions[i]}`;
    }
    desc += "\n\nOr reply to this message.";
  }

  return new EmbedBuilder()
    .setTitle("Session: OpenCode is waiting...")
    .setDescription(desc)
    .setColor(COLOR_WAITING)
    .setFooter({
      text: `${project} #${sessionID.slice(0, 8)}`,
    })
    .setTimestamp();
}

export function workingEmbed(
  project: string,
  sessionID: string,
): EmbedBuilder {
  return new EmbedBuilder()
    .setTitle("Session: OpenCode is working...")
    .setColor(COLOR_WORKING)
    .setFooter({
      text: `${project} #${sessionID.slice(0, 8)}`,
    })
    .setTimestamp();
}

export function disconnectedEmbed(
  project: string,
  sessionID: string,
): EmbedBuilder {
  return new EmbedBuilder()
    .setTitle("Session: Disconnected")
    .setColor(COLOR_DISCONNECTED)
    .setFooter({
      text: `${project} #${sessionID.slice(0, 8)}`,
    })
    .setTimestamp();
}
