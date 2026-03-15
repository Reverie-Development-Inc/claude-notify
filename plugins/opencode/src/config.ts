export interface Config {
  botToken: string;
  ownerUserID: string;
  channelID: string | null;
  allowedUsers: string[];
  delaySeconds: number;
  previewLength: number;
}

export function loadConfig(): Config {
  const token =
    process.env.CLAUDE_NOTIFY_BOT_TOKEN;
  const userID =
    process.env.CLAUDE_NOTIFY_USER_ID;

  if (!token) {
    throw new Error(
      "CLAUDE_NOTIFY_BOT_TOKEN is required. " +
        "Set it in your environment (never " +
        "commit tokens to version control).",
    );
  }
  if (!userID) {
    throw new Error(
      "CLAUDE_NOTIFY_USER_ID is required.",
    );
  }

  const allowed =
    process.env.CLAUDE_NOTIFY_ALLOWED_USERS;

  return {
    botToken: token,
    ownerUserID: userID,
    channelID:
      process.env.CLAUDE_NOTIFY_CHANNEL_ID || null,
    allowedUsers: allowed
      ? allowed
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean)
      : [],
    delaySeconds: parseInt(
      process.env.CLAUDE_NOTIFY_DELAY_SECONDS ||
        "300",
      10,
    ),
    previewLength: parseInt(
      process.env.CLAUDE_NOTIFY_PREVIEW_LENGTH ||
        "500",
      10,
    ),
  };
}

export function isAllowedUser(
  config: Config,
  userID: string,
): boolean {
  if (userID === config.ownerUserID) return true;
  return config.allowedUsers.includes(userID);
}
