import Bowser from "bowser";
export type AgentProduct = "claude" | "codex" | "opencode";
export type Product = AgentProduct | "both";
export type Target = "unknown" | "macos" | "linux" | "windows" | "manual";
export type Intent = "install" | "update" | "configure";
export const products = [
  { value: "claude", label: "Claude Code" },
  { value: "codex", label: "Codex CLI" },
  { value: "both", label: "Claude + Codex" },
  { value: "opencode", label: "OpenCode" },
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
export const installerUrl =
  "https://777genius.github.io/agent-notifications/install.sh";
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
  product: Product | readonly AgentProduct[],
  target: Target,
  intent: Intent,
  agentNotify = true,
  openCodeChannels: { desktop: boolean; webhook: boolean } = { desktop: true, webhook: false },
): string | null {
  if (intent === "configure" || target === "unknown" || target === "manual")
    return null;
  const selected: readonly AgentProduct[] =
    typeof product === "string"
      ? product === "both"
        ? ["claude", "codex"]
        : [product]
      : product;
  if (!selected.length) return null;
  const hasOpenCode = selected.includes("opencode");
  if (hasOpenCode && !openCodeChannels.desktop && !openCodeChannels.webhook)
    return null;
  const hasClaude = selected.includes("claude");
  const hasCodex = selected.includes("codex");
  const skip = (hasClaude || hasCodex) && !agentNotify ? " --skip-agent-notify" : "";
  if (hasOpenCode) {
    const channels = `${openCodeChannels.desktop ? " --desktop" : ""}${openCodeChannels.webhook ? " --webhook" : ""}`;
    const productFlag = hasClaude || hasCodex
      ? `--products ${(["claude", "codex", "opencode"] as const).filter((value) => selected.includes(value)).join(",")}`
      : "--product opencode";
    const pipeline = `curl -fsSL ${installerUrl} | bash -s -- ${productFlag}${skip}${channels}`;
    return hasClaude || hasCodex ? `(set -o pipefail; ${pipeline})` : pipeline;
  }
  if (hasClaude && hasCodex)
    return `(set -o pipefail; curl -fsSL ${installerUrl} | bash -s -- --products claude,codex${skip})`;
  return `curl -fsSL ${installerUrl} | bash -s -- --product ${hasCodex ? "codex" : "claude"}${skip}`;
}
