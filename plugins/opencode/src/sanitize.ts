const SECRET_PATTERNS: Array<[RegExp, string]> = [
  [/([A-Z][A-Z_]{2,})=\S+/g, "$1=[REDACTED]"],
  [/(bearer\s+)\S+/gi, "$1[REDACTED]"],
  [/\w+:\/\/\S+:\S+@\S+/g, "[REDACTED_URI]"],
  [/AKIA[0-9A-Z]{16}/g, "[REDACTED_KEY]"],
  [/[A-Za-z0-9+/=]{41,}/g, "[REDACTED]"],
];

export function stripSecrets(s: string): string {
  for (const [pattern, replacement] of
    SECRET_PATTERNS) {
    s = s.replace(pattern, replacement);
  }
  return s;
}

export function truncate(
  s: string,
  maxLen: number,
): string {
  if (s.length <= maxLen) return s;
  return s.slice(0, maxLen) + "...";
}

export function preview(
  s: string,
  maxLen: number,
): string {
  return truncate(stripSecrets(s), maxLen).trim();
}
