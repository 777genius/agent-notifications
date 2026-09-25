export type FocusLevel = "exact" | "window" | "bestEffort" | "none";

export type TerminalSupportRow = {
  id: string;
  nameKey: string;
  logo: string;
  macos: FocusLevel;
  linux: FocusLevel;
  windows: FocusLevel;
  noteKeys?: readonly string[];
};

export const terminalSupport: readonly TerminalSupportRow[] = [
  { id: "ghostty", nameKey: "ghostty", logo: "ghostty.svg", macos: "exact", linux: "bestEffort", windows: "none", noteKeys: ["ghosttyAutomation", "ghosttyUnverified"] },
  { id: "iterm2", nameKey: "iterm2", logo: "iterm2.svg", macos: "exact", linux: "none", windows: "none", noteKeys: ["itermPythonApi"] },
  { id: "warp", nameKey: "warp", logo: "warp.svg", macos: "exact", linux: "exact", windows: "exact", noteKeys: ["warpDeepLink"] },
  { id: "kitty", nameKey: "kitty", logo: "kitty.svg", macos: "exact", linux: "window", windows: "none", noteKeys: ["kittyRemoteControl"] },
  { id: "wezterm", nameKey: "wezterm", logo: "wezterm.svg", macos: "exact", linux: "exact", windows: "none", noteKeys: ["wezPane"] },
  { id: "vscode", nameKey: "vscode", logo: "vscode.svg", macos: "window", linux: "window", windows: "window" },
  { id: "cursor", nameKey: "cursor", logo: "cursor.svg", macos: "window", linux: "bestEffort", windows: "window", noteKeys: ["genericFallback"] },
  { id: "jetbrains", nameKey: "jetbrains", logo: "jetbrains.svg", macos: "window", linux: "window", windows: "window", noteKeys: ["jetbrainsGeneric", "jetbrainsLinuxDetect"] },
  { id: "appleTerminal", nameKey: "appleTerminal", logo: "generic-terminal.svg", macos: "window", linux: "none", windows: "none", noteKeys: ["appleTerminalFallback"] },
  { id: "alacritty", nameKey: "alacritty", logo: "alacritty.svg", macos: "window", linux: "window", windows: "none" },
  { id: "hyper", nameKey: "hyper", logo: "hyper.svg", macos: "window", linux: "bestEffort", windows: "none", noteKeys: ["genericFallback"] },
  { id: "gnomeTerminal", nameKey: "gnomeTerminal", logo: "gnome-terminal.svg", macos: "none", linux: "window", windows: "none" },
  { id: "konsole", nameKey: "konsole", logo: "konsole.svg", macos: "none", linux: "window", windows: "none" },
  { id: "tilix", nameKey: "tilix", logo: "tilix.svg", macos: "none", linux: "window", windows: "none" },
  { id: "terminator", nameKey: "terminator", logo: "terminator.svg", macos: "none", linux: "window", windows: "none", noteKeys: ["terminatorTitle"] },
  { id: "xfce4Terminal", nameKey: "xfce4Terminal", logo: "xfce4-terminal.svg", macos: "none", linux: "window", windows: "none" },
  { id: "mateTerminal", nameKey: "mateTerminal", logo: "mate-terminal.png", macos: "none", linux: "window", windows: "none" },
  { id: "windowsTerminal", nameKey: "windowsTerminal", logo: "windows-terminal.png", macos: "none", linux: "none", windows: "window" },
  { id: "conhost", nameKey: "conhost", logo: "powershell.svg", macos: "none", linux: "none", windows: "window" },
  { id: "conemu", nameKey: "conemu", logo: "conemu.png", macos: "none", linux: "none", windows: "window" },
  { id: "tmux", nameKey: "tmux", logo: "tmux.svg", macos: "exact", linux: "none", windows: "none", noteKeys: ["tmuxItermCC"] },
  { id: "zellij", nameKey: "zellij", logo: "zellij.svg", macos: "exact", linux: "exact", windows: "none", noteKeys: ["zellijLinuxPane"] },
] as const;

export const focusLevelIcon: Record<FocusLevel, string> = {
  exact: "●",
  window: "◐",
  bestEffort: "◌",
  none: "—",
};
