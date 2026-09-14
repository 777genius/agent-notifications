import Bowser from "bowser";
export type Product = "claude" | "codex" | "both";
export type Target = "unknown" | "macos" | "linux" | "windows" | "manual";
export type Intent = "install" | "update" | "configure";
export const products = [
  { value: "claude", label: "Claude Code" },
  { value: "codex", label: "Codex CLI · beta" },
  { value: "both", label: "Both agents" },
] as const;
export const targets = [
  { value: "unknown", label: "Choose target OS" },
  { value: "macos", label: "macOS" },
  { value: "linux", label: "Linux" },
  { value: "windows", label: "Windows · Git Bash" },
  { value: "manual", label: "Manual instructions" },
] as const;
export const intents = ["install", "update", "configure"] as const;
export const repo = "https://github.com/777genius/agent-notifications";
export function detectTarget(ua: string, touchPoints = 0): Target {
  const browser = Bowser.getParser(ua);
  if (
    browser.getPlatformType() === "mobile" ||
    browser.getPlatformType() === "tablet" ||
    /Android|iPhone|iPad|iPod/i.test(ua) ||
    (/Macintosh/.test(ua) && touchPoints > 1)
  )
    return "unknown";
  const os = browser.getOSName(true);
  return os === "macos"
    ? "macos"
    : os === "linux"
      ? "linux"
      : os === "windows"
        ? "windows"
        : "unknown";
}
export function command(
  product: Product,
  target: Target,
  intent: Intent,
  agentNotify = true,
): string | null {
  if (intent === "configure" || target === "unknown" || target === "manual")
    return null;
  const skip = agentNotify ? "" : " --skip-agent-notify";
  return `curl -fsSL https://raw.githubusercontent.com/777genius/agent-notifications/main/bin/bootstrap.sh | bash -s -- --product ${product}${skip}`;
}
