function trimmedNonEmptyLines(value: string): string[] {
  return value.split(/\r?\n/).map((item) => item.trim()).filter(Boolean);
}

export function uniqueLines(value: string): string[] {
  return [...new Set(trimmedNonEmptyLines(value))];
}

export function argumentLines(value: string): string[] {
  return trimmedNonEmptyLines(value);
}
