export const REACTION_MAP: Record<string, string> =
  {
    "\u2705": "Yes, continue",
    "\u274C": "No, stop here",
    "\uD83D\uDC40":
      "Show me what you have so far",
  };

export const SHORTCUT_REACTIONS = [
  "\u2705", // checkmark
  "\u274C", // X
  "\uD83D\uDC40", // eyes
];

export const NUMBER_REACTIONS = [
  "1\uFE0F\u20E3",
  "2\uFE0F\u20E3",
  "3\uFE0F\u20E3",
  "4\uFE0F\u20E3",
  "5\uFE0F\u20E3",
];

export function expandReaction(
  emoji: string,
  suggestions: string[],
): string | null {
  const mapped = REACTION_MAP[emoji];
  if (mapped) return mapped;

  const numIdx = NUMBER_REACTIONS.indexOf(emoji);
  if (
    numIdx >= 0 &&
    numIdx < suggestions.length
  ) {
    return suggestions[numIdx];
  }

  return null;
}

export function isKnownReaction(
  emoji: string,
): boolean {
  return (
    emoji in REACTION_MAP ||
    NUMBER_REACTIONS.includes(emoji)
  );
}
