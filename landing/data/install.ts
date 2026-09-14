import Bowser from "bowser";
export type Product = "claude" | "codex" | "both";
export type Target = "unknown" | "macos" | "linux" | "windows" | "manual";
export type Intent = "install" | "update" | "configure";
export const products = [
  { value: "claude", label: "Claude Code" },
  { value: "codex", label: "Codex CLI" },
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
const tagParser =
  'import json,re,sys; v=json.load(sys.stdin).get("tag_name",""); re.fullmatch(r"v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)",v) or sys.exit("Invalid stable release tag"); sys.stdout.buffer.write((v+"\\n").encode("ascii"))';
const commitParser =
  'import json,re,sys; v=json.load(sys.stdin).get("sha",""); re.fullmatch(r"[0-9a-f]{40}",v) or sys.exit("Invalid release commit"); sys.stdout.buffer.write((v+"\\n").encode("ascii"))';
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
): string | null {
  if (intent === "configure" || target === "unknown" || target === "manual")
    return null;
  return [
    "(",
    "  set -euo pipefail",
    "  repo=777genius/agent-notifications",
    `  tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | python3 -I -c '${tagParser}')`,
    `  commit=$(curl -fsSL "https://api.github.com/repos/$repo/commits/$tag" | python3 -I -c '${commitParser}')`,
    '  raw="https://raw.githubusercontent.com/$repo/$commit/bin"',
    `  curl -fsSL "$raw/bootstrap.sh" | env BOOTSTRAP_RELEASE_TAG="$tag" BOOTSTRAP_RELEASE_COMMIT="$commit" INSTALL_SCRIPT_URL="$raw/install.sh" bash -s -- --product ${product}`,
    ")",
  ].join("\n");
}
